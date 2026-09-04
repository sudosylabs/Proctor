// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func hasPendingCandidateCorrectionAcknowledgement(ctx context.Context, executor sqlxExecutor, attemptID, sittingID,
	admissionRevisionID, currentRevisionID string,
) (bool, error) {
	var pending bool
	err := executor.Get(ctx, &pending, `SELECT EXISTS (
		SELECT 1 FROM exam_sitting_live_corrections live
		JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id
		JOIN exam_revisions admission ON admission.id=? AND admission.exam_id=live.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=? AND current_revision.exam_id=live.exam_id
		WHERE live.exam_sitting_id=? AND correction.number>admission.number
		AND correction.number<=current_revision.number AND correction.publication_kind='live_correction'
		AND correction.candidate_correction_acknowledgement_required=true
		AND NOT EXISTS (SELECT 1 FROM exam_attempt_correction_acknowledgements acknowledgement
			WHERE acknowledgement.exam_attempt_id=? AND acknowledgement.correction_revision_id=correction.id))`,
		admissionRevisionID, currentRevisionID, sittingID, attemptID)
	if err != nil {
		return false, fmt.Errorf("inspect pending Exam correction acknowledgement: %w", err)
	}
	return pending, nil
}

func (s *sqlExamAttemptStore) ResolveCorrectionAcknowledgementTarget(ctx context.Context, input store.ExamAttemptCorrectionAcknowledgement) (*store.ExamAttemptCorrectionAcknowledgementTarget, error) {
	if err := validateCorrectionAcknowledgement(&input, false); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Exam correction acknowledgement target", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptCorrectionAcknowledgementTarget, error) {
		return s.resolveCorrectionAcknowledgementTarget(ctx, tx, &input)
	})
}

func (s *sqlExamAttemptStore) AcknowledgeCorrection(ctx context.Context, input *store.ExamAttemptCorrectionAcknowledgement, command *store.CommandIdempotency) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
	if err := validateCorrectionAcknowledgement(input, true); err != nil || command == nil {
		if err != nil {
			return nil, err
		}
		return nil, store.NewErrInvalidInput("exam_attempt_correction_acknowledgement", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "acknowledge Exam correction", idempotentMutation[*store.ExamAttemptCorrectionAcknowledgementResult]{
		command: command,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
			return s.acknowledgeCorrection(ctx, tx, input)
		},
		encode: func(value *store.ExamAttemptCorrectionAcknowledgementResult) ([]byte, error) {
			return encodeCommandOutcome(value)
		},
		decode: func(version int, data []byte) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Exam correction acknowledgement outcome version %d", version)
			}
			var value store.ExamAttemptCorrectionAcknowledgementResult
			if err := decodeCommandOutcome(data, &value); err != nil {
				return nil, err
			}
			if err := validateCorrectionAcknowledgementResult(&value, input); err != nil {
				return nil, err
			}
			return &value, nil
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamAttemptCorrectionAcknowledgementResult) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
			if _, err := s.resolveCorrectionAcknowledgementTarget(ctx, tx, input); err != nil {
				return nil, err
			}
			return value, nil
		},
		freshAuditEventID: func(value *store.ExamAttemptCorrectionAcknowledgementResult) (string, error) {
			if value == nil || !model.IsValidId(value.MutationAuditEventID) {
				return "", errors.New("missing correction acknowledgement mutation audit")
			}
			return value.MutationAuditEventID, nil
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func validateCorrectionAcknowledgement(input *store.ExamAttemptCorrectionAcknowledgement, mutation bool) error {
	if input == nil || !input.Access.AttemptID.IsValid() || !input.ParticipationID.IsValid() || input.Generation < 1 ||
		!input.CorrectionRevisionID.IsValid() || !input.ExpectedCurrentRevisionID.IsValid() {
		return store.NewErrInvalidInput("exam_attempt_correction_acknowledgement", "selector", nil)
	}
	if mutation && (input.AuditEvent == nil || !input.AuditEvent.ID.IsZero() || input.AuditEvent.Status != model.AuditStatusAttempt) {
		return store.NewErrInvalidInput("exam_attempt_correction_acknowledgement", "audit", nil)
	}
	return nil
}

func (s *sqlExamAttemptStore) resolveCorrectionAcknowledgementTarget(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptCorrectionAcknowledgement) (*store.ExamAttemptCorrectionAcknowledgementTarget, error) {
	guard, err := s.lockCandidateGuard(ctx, tx, input.Access)
	if err != nil {
		return nil, err
	}
	var retainedCurrentRevisionID string
	err = tx.Get(ctx, &retainedCurrentRevisionID, `SELECT current_revision_id
		FROM exam_attempt_correction_acknowledgements
		WHERE exam_attempt_id=? AND correction_revision_id=?`, guard.AttemptID, input.CorrectionRevisionID.String())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if (err == nil && retainedCurrentRevisionID != input.ExpectedCurrentRevisionID.String()) ||
		(errors.Is(err, sql.ErrNoRows) && guard.RevisionID != input.ExpectedCurrentRevisionID.String()) {
		return nil, store.NewErrConflict("exam_attempt_correction_acknowledgement", "exam_sitting_revision_selection", nil)
	}
	var validParticipation bool
	if err = tx.Get(ctx, &validParticipation, `SELECT EXISTS (
		SELECT 1 FROM exam_attempt_participations p JOIN exam_attempt_connections c
		ON c.participation_id=p.id AND c.exam_attempt_id=p.exam_attempt_id
		WHERE p.id=? AND p.exam_attempt_id=? AND p.generation=? AND p.state='active'
		AND c.id=? AND c.state='open' AND p.session_id=? AND c.session_id=?)`, input.ParticipationID.String(),
		guard.AttemptID, input.Generation, input.Access.ConnectionID.String(), input.Access.SessionID.String(), input.Access.SessionID.String()); err != nil {
		return nil, err
	}
	if !validParticipation {
		return nil, store.NewErrConflict("exam_attempt_correction_acknowledgement", "attempt_participation_generation", nil)
	}
	var target struct {
		ExamID      string `db:"exam_id"`
		SittingID   string `db:"exam_sitting_id"`
		ClassID     string `db:"class_id"`
		CandidateID string `db:"candidate_user_id"`
	}
	if err = tx.Get(ctx, &target, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_revisions admission ON admission.id=a.admission_revision_id AND admission.exam_id=a.exam_id
		JOIN exam_revisions correction ON correction.id=? AND correction.exam_id=a.exam_id AND correction.sealed=true
		JOIN exam_sitting_live_corrections live ON live.exam_sitting_id=a.exam_sitting_id AND live.correction_revision_id=correction.id
		WHERE a.id=? AND correction.number>admission.number AND correction.number<=(
			SELECT number FROM exam_revisions WHERE id=s.exam_revision_id AND exam_id=a.exam_id)
		AND correction.publication_kind='live_correction' AND correction.candidate_correction_acknowledgement_required=true`,
		input.CorrectionRevisionID.String(), guard.AttemptID); errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("exam_attempt_correction_acknowledgement", "exam_correction_acknowledgement_target", nil)
	} else if err != nil {
		return nil, err
	}
	attemptID, err := model.ParseExamAttemptID(guard.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(target.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(target.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	classID, err := model.ParseClassID(target.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "class_id", err)
	}
	candidateID, err := model.ParseUserID(target.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	return &store.ExamAttemptCorrectionAcknowledgementTarget{AttemptID: attemptID, ExamID: examID, SittingID: sittingID,
		ClassID: classID, CandidateUserID: candidateID, CorrectionRevisionID: input.CorrectionRevisionID,
		CurrentRevisionID: input.ExpectedCurrentRevisionID}, nil
}

func (s *sqlExamAttemptStore) acknowledgeCorrection(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptCorrectionAcknowledgement) (*store.ExamAttemptCorrectionAcknowledgementResult, error) {
	lockKey := "proctor:exam-attempt-correction-acknowledgement:" + input.Access.AttemptID.String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, lockKey); err != nil {
		return nil, fmt.Errorf("lock Exam correction acknowledgement: %w", err)
	}
	target, err := s.resolveCorrectionAcknowledgementTarget(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	var existing struct {
		CurrentRevisionID string    `db:"current_revision_id"`
		AcknowledgedAt    time.Time `db:"acknowledged_at"`
		AuditEventID      string    `db:"audit_event_id"`
	}
	err = tx.Get(ctx, &existing, `SELECT current_revision_id,acknowledged_at,audit_event_id FROM exam_attempt_correction_acknowledgements
		WHERE exam_attempt_id=? AND correction_revision_id=?`, target.AttemptID.String(), target.CorrectionRevisionID.String())
	if err == nil {
		currentRevisionID, parseErr := model.ParseExamRevisionID(existing.CurrentRevisionID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_attempt_correction_acknowledgement", "current_revision_id", parseErr)
		}
		return &store.ExamAttemptCorrectionAcknowledgementResult{AttemptID: target.AttemptID,
			CorrectionRevisionID: target.CorrectionRevisionID,
			CurrentRevisionID:    currentRevisionID,
			AcknowledgedAt:       model.TimeUTC(existing.AcknowledgedAt), MutationAuditEventID: existing.AuditEventID, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var oldestPending string
	err = tx.Get(ctx, &oldestPending, `SELECT correction.id FROM exam_sitting_live_corrections live
		JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id
		JOIN exam_attempts a ON a.id=? AND a.exam_sitting_id=live.exam_sitting_id AND a.exam_id=live.exam_id
		JOIN exam_revisions admission ON admission.id=a.admission_revision_id AND admission.exam_id=a.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=? AND current_revision.exam_id=a.exam_id
		WHERE correction.number>admission.number AND correction.number<=current_revision.number
		AND correction.candidate_correction_acknowledgement_required=true
		AND NOT EXISTS (SELECT 1 FROM exam_attempt_correction_acknowledgements acknowledgement
			WHERE acknowledgement.exam_attempt_id=a.id AND acknowledgement.correction_revision_id=correction.id)
		ORDER BY correction.number LIMIT 1 FOR SHARE OF correction`, target.AttemptID.String(), target.CurrentRevisionID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("exam_attempt_correction_acknowledgement", "exam_correction_acknowledgement_order", nil)
	}
	if err != nil {
		return nil, err
	}
	if oldestPending != target.CorrectionRevisionID.String() {
		return nil, store.NewErrConflict("exam_attempt_correction_acknowledgement", "exam_correction_acknowledgement_order", nil)
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return nil, err
	}
	databaseNow = model.TimeUTC(databaseNow)
	auditEvent, err := insertAuditEventAt(ctx, tx, input.AuditEvent, databaseNow)
	if err != nil {
		return nil, fmt.Errorf("insert Exam correction acknowledgement audit: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_correction_acknowledgements
		(exam_attempt_id,exam_id,exam_sitting_id,correction_revision_id,current_revision_id,audit_event_id,acknowledged_at)
		VALUES (?,?,?,?,?,?,?)`, target.AttemptID.String(), target.ExamID.String(), target.SittingID.String(),
		target.CorrectionRevisionID.String(), target.CurrentRevisionID.String(), auditEvent.ID.String(), databaseNow); err != nil {
		return nil, fmt.Errorf("insert Exam correction acknowledgement: %w", err)
	}
	result := &store.ExamAttemptCorrectionAcknowledgementResult{AttemptID: target.AttemptID,
		CorrectionRevisionID: target.CorrectionRevisionID, CurrentRevisionID: target.CurrentRevisionID,
		AcknowledgedAt: databaseNow, MutationAuditEventID: auditEvent.ID.String()}
	if err = completeCorrectionAcknowledgementAudit(ctx, tx, result, auditEvent.ID.String(), databaseNow); err != nil {
		return nil, err
	}
	return result, nil
}

func completeCorrectionAcknowledgementAudit(ctx context.Context, tx *sqlxTxWrapper,
	result *store.ExamAttemptCorrectionAcknowledgementResult, auditID string, at time.Time,
) error {
	data := map[string]any{"exam_attempt_id": result.AttemptID.String(), "correction_revision_id": result.CorrectionRevisionID.String(),
		"current_revision_id": result.CurrentRevisionID.String(), "acknowledged_at": model.MillisFromTime(result.AcknowledgedAt)}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, model.MillisFromTime(at))
	return err
}

func validateCorrectionAcknowledgementResult(value *store.ExamAttemptCorrectionAcknowledgementResult, input *store.ExamAttemptCorrectionAcknowledgement) error {
	if value == nil || value.AttemptID != input.Access.AttemptID || value.CorrectionRevisionID != input.CorrectionRevisionID ||
		value.CurrentRevisionID != input.ExpectedCurrentRevisionID || value.AcknowledgedAt.IsZero() ||
		!model.IsValidId(value.MutationAuditEventID) {
		return invalidPersistedState("exam_attempt_correction_acknowledgement", "outcome", errors.New("invalid acknowledgement outcome"))
	}
	return nil
}

type candidateLiveCorrectionRow struct {
	RevisionID              string         `db:"revision_id"`
	RevisionNumber          int64          `db:"revision_number"`
	EffectiveAt             time.Time      `db:"effective_at"`
	Summary                 string         `db:"candidate_correction_summary"`
	ChangedAreas            pq.StringArray `db:"candidate_correction_changed_areas"`
	AcknowledgementRequired bool           `db:"candidate_correction_acknowledgement_required"`
	AcknowledgedAt          sql.NullTime   `db:"acknowledged_at"`
}

func listCandidateLiveCorrections(ctx context.Context, executor sqlxExecutor, attemptID model.ExamAttemptID,
	currentRevisionID model.ExamRevisionID,
) ([]model.CandidateLiveCorrection, bool, error) {
	var rows []candidateLiveCorrectionRow
	if err := executor.Select(ctx, &rows, `SELECT correction.id AS revision_id,correction.number AS revision_number,
		correction.published_at AS effective_at,correction.candidate_correction_summary,
		correction.candidate_correction_changed_areas,correction.candidate_correction_acknowledgement_required,
		acknowledgement.acknowledged_at
		FROM exam_attempts attempt_record
		JOIN exam_revisions admission ON admission.id=attempt_record.admission_revision_id AND admission.exam_id=attempt_record.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=? AND current_revision.exam_id=attempt_record.exam_id
		JOIN exam_sitting_live_corrections live ON live.exam_sitting_id=attempt_record.exam_sitting_id AND live.exam_id=attempt_record.exam_id
		JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id AND correction.sealed=true
		LEFT JOIN exam_attempt_correction_acknowledgements acknowledgement ON acknowledgement.exam_attempt_id=attempt_record.id
			AND acknowledgement.correction_revision_id=correction.id
		WHERE attempt_record.id=? AND correction.number>admission.number AND correction.number<=current_revision.number
		ORDER BY correction.number LIMIT ?`, currentRevisionID.String(), attemptID.String(), model.ExamSittingMaximumLiveCorrections+1); err != nil {
		return nil, false, err
	}
	if len(rows) > model.ExamSittingMaximumLiveCorrections {
		return nil, false, invalidPersistedState("exam_correction", "candidate_projection", errors.New("correction limit exceeded"))
	}
	result := make([]model.CandidateLiveCorrection, len(rows))
	for index, row := range rows {
		revisionID, err := model.ParseExamRevisionID(row.RevisionID)
		if err != nil {
			return nil, false, invalidPersistedState("exam_correction", "revision_id", err)
		}
		areas := make([]model.ExamCorrectionChangedArea, len(row.ChangedAreas))
		for areaIndex, area := range row.ChangedAreas {
			areas[areaIndex] = model.ExamCorrectionChangedArea(area)
		}
		state := model.CorrectionAcknowledgementNotRequired
		acknowledgedAt := model.OptionalTime{}
		if row.AcknowledgementRequired {
			state = model.CorrectionAcknowledgementPending
			if row.AcknowledgedAt.Valid {
				state = model.CorrectionAcknowledgementAcknowledged
				acknowledgedAt = model.OptionalTimeFrom(row.AcknowledgedAt.Time)
			}
		} else if row.AcknowledgedAt.Valid {
			return nil, false, invalidPersistedState("exam_correction", "acknowledgement", errors.New("notice-only correction was acknowledged"))
		}
		correction := model.CandidateLiveCorrection{RevisionID: revisionID, RevisionNumber: row.RevisionNumber,
			EffectiveAt: model.TimeUTC(row.EffectiveAt), Summary: row.Summary, ChangedAreas: areas,
			AcknowledgementRequired: row.AcknowledgementRequired, AcknowledgementState: state, AcknowledgedAt: acknowledgedAt}
		if err = correction.Validate(); err != nil {
			return nil, false, invalidPersistedState("exam_correction", "candidate_projection", err)
		}
		result[index] = correction
	}
	pending := false
	for _, correction := range result {
		if correction.AcknowledgementState == model.CorrectionAcknowledgementPending {
			pending = true
			break
		}
	}
	return result, pending, nil
}
