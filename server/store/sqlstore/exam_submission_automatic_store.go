// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type automaticExamSubmissionTargetRow struct {
	ExamID            string `db:"exam_id"`
	SittingID         string `db:"exam_sitting_id"`
	ClassID           string `db:"class_id"`
	AcademicUnitID    string `db:"academic_unit_id"`
	CandidateID       string `db:"candidate_id"`
	AttemptID         string `db:"attempt_id"`
	WorkspaceID       string `db:"workspace_id"`
	CurrentRevisionID string `db:"current_revision_id"`
	ParticipationID   string `db:"participation_id"`
	ConnectionID      string `db:"connection_id"`
	Generation        int64  `db:"generation"`
}

func (s *SQLExamSubmissionStore) ListAutomaticSealTargets(ctx context.Context,
	options store.ExamSubmissionAutomaticSealListOptions,
) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	if !options.SittingID.IsValid() || (!options.AfterAttemptID.IsZero() && !options.AfterAttemptID.IsValid()) ||
		options.Limit < 1 || options.Limit > 201 {
		return nil, store.NewErrInvalidInput("exam_submission", "automatic_seal_list", nil)
	}
	var rows []automaticExamSubmissionTargetRow
	if err := s.GetMaster().Select(ctx, &rows, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,e.academic_unit_id,
		a.candidate_user_id AS candidate_id,a.id AS attempt_id,w.id AS workspace_id,
		s.exam_revision_id AS current_revision_id,
		p.id AS participation_id,p.generation,c.id AS connection_id
		FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exams e ON e.id=a.exam_id
		JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		JOIN LATERAL (SELECT id,generation FROM exam_attempt_participations
			WHERE exam_attempt_id=a.id ORDER BY generation DESC LIMIT 1) p ON true
		JOIN LATERAL (SELECT id FROM exam_attempt_connections
			WHERE exam_attempt_id=a.id AND participation_id=p.id ORDER BY opened_at DESC,id DESC LIMIT 1) c ON true
		WHERE a.exam_sitting_id=? AND s.state='closing' AND a.state IN ('active','ready','suspended') AND a.id>?
		AND NOT EXISTS (SELECT 1 FROM exam_submissions sub WHERE sub.exam_attempt_id=a.id AND sub.sealed=true)
		ORDER BY a.id LIMIT ?`, options.SittingID.String(), options.AfterAttemptID.String(), options.Limit); err != nil {
		return nil, fmt.Errorf("list automatic Exam Submission targets: %w", err)
	}
	result := make([]store.ExamSubmissionAutomaticSealTarget, 0, len(rows))
	for _, row := range rows {
		target, err := automaticExamSubmissionTarget(row)
		if err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, nil
}

func automaticExamSubmissionTarget(row automaticExamSubmissionTargetRow) (store.ExamSubmissionAutomaticSealTarget, error) {
	var target store.ExamSubmissionAutomaticSealTarget
	var err error
	if target.ExamID, err = model.ParseExamID(row.ExamID); err != nil {
		return target, invalidPersistedState("exam_submission", "exam_id", err)
	}
	if target.SittingID, err = model.ParseExamSittingID(row.SittingID); err != nil {
		return target, invalidPersistedState("exam_submission", "exam_sitting_id", err)
	}
	if target.ClassID, err = model.ParseClassID(row.ClassID); err != nil {
		return target, invalidPersistedState("exam_submission", "class_id", err)
	}
	if target.AcademicUnitID, err = model.ParseAcademicUnitID(row.AcademicUnitID); err != nil {
		return target, invalidPersistedState("exam_submission", "academic_unit_id", err)
	}
	if target.CandidateUserID, err = model.ParseUserID(row.CandidateID); err != nil {
		return target, invalidPersistedState("exam_submission", "candidate_user_id", err)
	}
	if target.AttemptID, err = model.ParseExamAttemptID(row.AttemptID); err != nil {
		return target, invalidPersistedState("exam_submission", "exam_attempt_id", err)
	}
	if target.WorkspaceID, err = model.ParseExamAttemptWorkspaceID(row.WorkspaceID); err != nil {
		return target, invalidPersistedState("exam_submission", "workspace_id", err)
	}
	if target.CurrentRevisionID, err = model.ParseExamRevisionID(row.CurrentRevisionID); err != nil {
		return target, invalidPersistedState("exam_submission", "exam_revision_id", err)
	}
	if target.ParticipationID, err = model.ParseAttemptParticipationID(row.ParticipationID); err != nil {
		return target, invalidPersistedState("exam_submission", "participation_id", err)
	}
	if target.ConnectionID, err = model.ParseAttemptConnectionID(row.ConnectionID); err != nil {
		return target, invalidPersistedState("exam_submission", "connection_id", err)
	}
	target.Generation = row.Generation
	if !validAutomaticExamSubmissionTarget(target) {
		return target, invalidPersistedState("exam_submission", "automatic_target", errors.New("invalid target"))
	}
	return target, nil
}

func validAutomaticExamSubmissionTarget(target store.ExamSubmissionAutomaticSealTarget) bool {
	return target.ExamID.IsValid() && target.SittingID.IsValid() && target.ClassID.IsValid() &&
		target.AcademicUnitID.IsValid() && target.CandidateUserID.IsValid() && target.AttemptID.IsValid() &&
		target.WorkspaceID.IsValid() && target.CurrentRevisionID.IsValid() && target.ParticipationID.IsValid() &&
		target.Generation > 0 && target.ConnectionID.IsValid()
}

type automaticExamSubmissionRow struct {
	automaticExamSubmissionTargetRow
	AdmissionRevisionID string         `db:"admission_revision_id"`
	AttemptState        string         `db:"attempt_state"`
	AttemptCreatedAt    time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt    time.Time      `db:"attempt_updated_at"`
	AttemptSubmittedAt  sql.NullTime   `db:"attempt_submitted_at"`
	AttemptRevision     int64          `db:"attempt_revision"`
	SittingState        string         `db:"sitting_state"`
	ScheduledEndAt      time.Time      `db:"scheduled_end_at"`
	WorkspaceCursor     int64          `db:"workspace_cursor"`
	ParticipationState  string         `db:"participation_state"`
	RenewalSequence     int64          `db:"renewal_sequence"`
	CredentialHash      string         `db:"continuity_credential_hash"`
	ParticipationStart  time.Time      `db:"participation_started_at"`
	ParticipationUpdate time.Time      `db:"participation_updated_at"`
	LeaseExpiresAt      time.Time      `db:"lease_expires_at"`
	ParticipationEnd    sql.NullTime   `db:"participation_ended_at"`
	ParticipationReason sql.NullString `db:"participation_end_reason"`
	SessionID           string         `db:"session_id"`
	ConnectionState     string         `db:"connection_state"`
	ConnectionOpenedAt  time.Time      `db:"connection_opened_at"`
	ConnectionClosedAt  sql.NullTime   `db:"connection_closed_at"`
	ConnectionReason    sql.NullString `db:"connection_close_reason"`
	PolicyCanonical     []byte         `db:"policy_canonical"`
}

func (s *SQLExamSubmissionStore) SealForSittingClose(ctx context.Context,
	input *store.ExamSubmissionAutomaticSeal,
) (*store.ExamSubmissionAutomaticSealResult, error) {
	if input == nil || !validAutomaticExamSubmissionTarget(input.Target) || !input.SubmissionID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_submission", "automatic_seal", nil)
	}
	prepared := *input
	return runSQLTransaction(ctx, s.GetMaster().Begin, "seal automatic Exam Submission", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSubmissionAutomaticSealResult, error) {
		return sealAutomaticExamSubmission(ctx, tx, &prepared)
	})
}

func (s *SQLExamSubmissionStore) PrepareAutomaticSeal(ctx context.Context,
	target store.ExamSubmissionAutomaticSealTarget,
) (*store.ExamSubmissionAutomaticSealPreparation, error) {
	if !validAutomaticExamSubmissionTarget(target) {
		return nil, store.NewErrInvalidInput("exam_submission", "automatic_seal_target", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve automatic Exam Submission replay", func(ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.ExamSubmissionAutomaticSealPreparation, error) {
		row, err := lockAutomaticExamSubmission(ctx, tx, target.AttemptID)
		if err != nil {
			return nil, err
		}
		current, err := automaticExamSubmissionTarget(row.automaticExamSubmissionTargetRow)
		if err != nil {
			return nil, err
		}
		if current != target {
			return nil, store.NewErrNotFound("exam_submission_automatic_target", target.AttemptID.String())
		}
		var databaseNow time.Time
		if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read automatic Exam Submission preparation time: %w", err)
		}
		return &store.ExamSubmissionAutomaticSealPreparation{Replayed: row.AttemptState == string(model.ExamAttemptSubmitted),
			SealAt: model.TimeFromMillis(model.MillisFromTime(databaseNow))}, nil
	})
}

func sealAutomaticExamSubmission(ctx context.Context, tx *sqlxTxWrapper,
	input *store.ExamSubmissionAutomaticSeal,
) (*store.ExamSubmissionAutomaticSealResult, error) {
	recipient, err := lockMailRecipientUser(ctx, tx, input.Target.CandidateUserID)
	if err != nil {
		return nil, err
	}
	row, err := lockAutomaticExamSubmission(ctx, tx, input.Target.AttemptID)
	if err != nil {
		return nil, err
	}
	target, err := automaticExamSubmissionTarget(row.automaticExamSubmissionTargetRow)
	if err != nil {
		return nil, err
	}
	if target != input.Target {
		return nil, store.NewErrNotFound("exam_submission_automatic_target", input.Target.AttemptID.String())
	}
	if row.AttemptState == string(model.ExamAttemptSubmitted) {
		return replayAutomaticExamSubmission(ctx, tx, input, target)
	}
	if input.ExpectedRecipientRevision < 1 || recipient.Revision != input.ExpectedRecipientRevision {
		return nil, store.NewErrConflict("exam_submission", "receipt_recipient_changed", nil)
	}
	payloadKeyID, err := validateExamSubmissionReceiptMail(input.Notice, recipient, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionAutomaticallySealed)
	if err != nil {
		return nil, err
	}
	if payloadKeyID != "" {
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return nil, err
		}
	}
	if row.SittingState != string(model.ExamSittingClosing) ||
		(row.AttemptState != string(model.ExamAttemptActive) && row.AttemptState != string(model.ExamAttemptReady) &&
			row.AttemptState != string(model.ExamAttemptSuspended)) {
		return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	integrity, err := lockAutomaticExamSubmissionIntegrity(ctx, tx, target.AttemptID)
	if err != nil {
		return nil, err
	}
	policy, err := model.DecodeExamPolicySet(row.PolicyCanonical)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "admission_policy", err)
	}
	unresolved := integrity.TotalUnresolved
	if policy.FocusLoss.Enabled {
		if unresolved == math.MaxInt64 {
			return nil, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
		}
		unresolved++
	}
	focusUnresolved := unresolved
	missingCorrections, err := listAutomaticPendingCorrectionAcknowledgements(ctx, tx, row)
	if err != nil {
		return nil, err
	}
	pendingAcknowledgements := int64(len(missingCorrections))
	if pendingAcknowledgements > math.MaxInt64-unresolved {
		return nil, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
	}
	unresolved += pendingAcknowledgements
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return nil, fmt.Errorf("read automatic Exam Submission time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	browserUnresolved, browserActivity, err := settleAutomaticBrowserActivity(ctx, tx, target.AttemptID, databaseNow)
	if err != nil {
		return nil, err
	}
	if browserUnresolved > math.MaxInt64-unresolved {
		return nil, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
	}
	unresolved += browserUnresolved
	manifestRows, entries, err := loadAuthoritativeExamSubmissionManifest(ctx, tx, target.WorkspaceID.String())
	if err != nil {
		return nil, err
	}
	manifest, err := model.NewExamSubmissionManifest(row.WorkspaceCursor, entries)
	if err != nil {
		return nil, invalidPersistedState("exam_submission_manifest", "value", err)
	}
	attempt, participation, connection, err := row.domain(target)
	if err != nil {
		return nil, err
	}
	beforeParticipation, beforeConnection := participation.State, connection.State
	sealAt := model.TimeFromMillis(input.AuditAt)
	if sealAt.After(databaseNow) {
		return nil, store.NewErrConflict("exam_submission", "seal_time", nil)
	}
	if err = model.SealExamAttemptForSittingClose(attempt, participation, connection, sealAt); err != nil {
		return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: input.SubmissionID,
		AttemptID: target.AttemptID, ExamRevisionID: target.CurrentRevisionID, WorkspaceID: target.WorkspaceID, Manifest: manifest,
		FinalFocusLossSequence: integrity.LatestAcceptedSequence, UnresolvedIntegrityCount: unresolved,
		BrowserActivity: browserActivity,
		Provenance:      model.ExamSubmissionSittingClosed, SubmittedAt: sealAt})
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_submission", "value", nil).Wrap(err)
	}
	if err = insertAutomaticExamSubmission(ctx, tx, submission, target, manifestRows, entries); err != nil {
		return nil, err
	}
	focusReason := model.IntegrityDiscrepancyFocusLossSequenceGap
	if policy.FocusLoss.Enabled {
		focusReason = model.IntegrityDiscrepancyFocusLossSourceNotFinalized
		if integrity.TotalUnresolved > 0 {
			focusReason = model.IntegrityDiscrepancyFocusLossSequenceGapAndSourceNotFinalized
		}
	}
	if err = insertTerminalIntegrityDiscrepancies(ctx, tx, submission, automaticIntegrityDiscrepancyTarget(target),
		terminalIntegrityDiscrepancies{FocusUnresolved: focusUnresolved,
			FocusReason: focusReason, BrowserUnresolved: browserUnresolved, BrowserActivity: browserActivity,
			MissingCorrections: missingCorrections}); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=?`, target.AttemptID.String()); err != nil {
		return nil, fmt.Errorf("clear automatic Exam Submission pending Focus Loss: %w", err)
	}
	if err = persistAutomaticSubmittedExamAttempt(ctx, tx, attempt, participation, connection, beforeParticipation, beforeConnection); err != nil {
		return nil, err
	}
	outcome := automaticExamSubmissionOutcome(submission, target)
	if err = completeExamSubmissionAudit(ctx, tx, outcome, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return nil, err
	}
	if err = insertExamSubmissionReceiptMail(ctx, tx, input.Notice, payloadKeyID); err != nil {
		return nil, err
	}
	result, err := examSubmissionSealResult(outcome)
	if err != nil {
		return nil, err
	}
	return &store.ExamSubmissionAutomaticSealResult{ExamSubmissionSealResult: *result,
		ConnectionClosed: beforeConnection == model.AttemptConnectionOpen}, nil
}

func lockAutomaticExamSubmission(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) (automaticExamSubmissionRow, error) {
	var row automaticExamSubmissionRow
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,e.academic_unit_id,
		a.candidate_user_id AS candidate_id,a.id AS attempt_id,w.id AS workspace_id,
		s.exam_revision_id AS current_revision_id,
		p.id AS participation_id,p.generation,c.id AS connection_id,a.admission_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,
		a.submitted_at AS attempt_submitted_at,a.revision AS attempt_revision,s.state AS sitting_state,
		s.scheduled_end_at,w.cursor AS workspace_cursor,
		p.state AS participation_state,p.renewal_sequence,p.continuity_credential_hash,
		p.started_at AS participation_started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,
		p.ended_at AS participation_ended_at,p.end_reason AS participation_end_reason,c.session_id,c.state AS connection_state,
		c.opened_at AS connection_opened_at,c.closed_at AS connection_closed_at,c.close_reason AS connection_close_reason,
		r.policy_canonical
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exams e ON e.id=a.exam_id JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		LEFT JOIN exam_submissions sub ON sub.exam_attempt_id=a.id AND sub.sealed=true
		JOIN exam_revisions r ON r.id=s.exam_revision_id AND r.exam_id=a.exam_id AND r.sealed=true
		JOIN LATERAL (SELECT * FROM exam_attempt_participations WHERE exam_attempt_id=a.id ORDER BY generation DESC LIMIT 1) p ON true
		JOIN LATERAL (SELECT * FROM exam_attempt_connections WHERE exam_attempt_id=a.id AND participation_id=p.id ORDER BY opened_at DESC,id DESC LIMIT 1) c ON true
		WHERE a.id=? FOR UPDATE OF a,s,w,p,c FOR SHARE OF e,r`, attemptID.String())
	if err != nil {
		return row, translateError("exam_attempt", attemptID.String(), err)
	}
	return row, nil
}

func listAutomaticPendingCorrectionAcknowledgements(ctx context.Context, tx *sqlxTxWrapper,
	row automaticExamSubmissionRow,
) ([]model.ExamRevisionID, error) {
	var raw []string
	if err := tx.Select(ctx, &raw, `SELECT correction.id FROM exam_sitting_live_corrections live
		JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id
		JOIN exam_revisions admission ON admission.id=? AND admission.exam_id=live.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=? AND current_revision.exam_id=live.exam_id
		WHERE live.exam_sitting_id=? AND correction.number>admission.number AND correction.number<=current_revision.number
		AND correction.candidate_correction_acknowledgement_required=true
		AND NOT EXISTS (SELECT 1 FROM exam_attempt_correction_acknowledgements acknowledgement
			WHERE acknowledgement.exam_attempt_id=? AND acknowledgement.correction_revision_id=correction.id)
		ORDER BY correction.number`,
		row.AdmissionRevisionID, row.CurrentRevisionID, row.SittingID, row.AttemptID); err != nil {
		return nil, fmt.Errorf("list automatic Exam Submission pending correction acknowledgements: %w", err)
	}
	result := make([]model.ExamRevisionID, len(raw))
	for index, value := range raw {
		id, err := model.ParseExamRevisionID(value)
		if err != nil {
			return nil, invalidPersistedState("exam_submission", "pending_correction_revision_id", err)
		}
		result[index] = id
	}
	return result, nil
}

func settleAutomaticBrowserActivity(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID,
	databaseNow time.Time,
) (int64, model.BrowserActivitySubmission, error) {
	var sources []browserActivitySubmissionSourceRow
	if err := tx.Select(ctx, &sources, `SELECT source.id::text,source.participation_id,source.generation,source.state,
		source.highest_contiguous,source.highest_seen,source.ended_at,
		(SELECT count(*) FROM browser_activity_events event WHERE event.source_session_id=source.id
			AND event.sequence>source.highest_contiguous AND event.sequence<=source.highest_seen) AS received_beyond_contiguous
		FROM browser_activity_sources source WHERE source.exam_attempt_id=? ORDER BY source.started_at,source.id FOR UPDATE OF source`, attemptID.String()); err != nil {
		return 0, model.BrowserActivitySubmission{}, fmt.Errorf("lock automatic Exam Submission Browser Activity: %w", err)
	}
	if len(sources) == 0 {
		return 0, model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionNotApplicable}, nil
	}
	latest := len(sources) - 1
	if sources[latest].State == "current" {
		result, err := tx.Exec(ctx, `UPDATE browser_activity_sources SET state='gapped',ended_at=?
			WHERE id=?::uuid AND exam_attempt_id=? AND state='current'`, databaseNow, sources[latest].ID, attemptID.String())
		if err != nil {
			return 0, model.BrowserActivitySubmission{}, fmt.Errorf("finalize automatic Exam Submission Browser Activity source: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source_fence", rowsErr)
		}
		sources[latest].State = "gapped"
	}
	if sources[latest].State != "gapped" {
		return 0, model.BrowserActivitySubmission{}, invalidPersistedState("browser_activity", "source_state",
			errors.New("automatic Submission Browser Activity source is unexpectedly closed"))
	}
	var finalSequence *int64
	if sources[latest].HighestSeen > 0 {
		value := sources[latest].HighestSeen
		finalSequence = &value
	}
	declaration := model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionGapped,
		SourceSessionID: model.BrowserSourceSessionID(sources[latest].ID), FinalSequence: finalSequence,
		GapReason: model.BrowserActivityGapSourceNotFinalized}
	if err := declaration.Validate(); err != nil {
		return 0, model.BrowserActivitySubmission{}, invalidPersistedState("browser_activity", "submission", err)
	}
	unresolved, err := browserActivitySourceUnresolved(sources)
	if err != nil {
		return 0, model.BrowserActivitySubmission{}, err
	}
	return unresolved, declaration, nil
}

type automaticExamSubmissionIntegrity struct{ LatestAcceptedSequence, TotalUnresolved int64 }

func lockAutomaticExamSubmissionIntegrity(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID) (automaticExamSubmissionIntegrity, error) {
	var rows []struct{ Generation, Accepted, Unresolved int64 }
	if err := tx.Select(ctx, &rows, `SELECT generation,accepted_sequence AS accepted,unresolved_missing_count AS unresolved
		FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? ORDER BY generation FOR UPDATE`, attemptID.String()); err != nil {
		return automaticExamSubmissionIntegrity{}, fmt.Errorf("lock automatic Exam Submission integrity: %w", err)
	}
	var result automaticExamSubmissionIntegrity
	for _, row := range rows {
		if row.Unresolved > math.MaxInt64-result.TotalUnresolved {
			return result, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
		}
		result.TotalUnresolved += row.Unresolved
		result.LatestAcceptedSequence = row.Accepted
	}
	return result, nil
}

func (row automaticExamSubmissionRow) domain(target store.ExamSubmissionAutomaticSealTarget) (*model.ExamAttempt, *model.AttemptParticipation, *model.AttemptConnection, error) {
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	sessionID, err := model.ParseSessionID(row.SessionID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_connection", "session_id", err)
	}
	attempt := &model.ExamAttempt{ID: target.AttemptID, ExamID: target.ExamID, SittingID: target.SittingID,
		CandidateUserID: target.CandidateUserID, AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState),
		CreatedAt: model.TimeUTC(row.AttemptCreatedAt), UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt),
		SubmittedAt: OptionalTimeFromNullTime(row.AttemptSubmittedAt), Revision: row.AttemptRevision}
	participation := &model.AttemptParticipation{ID: target.ParticipationID, AttemptID: target.AttemptID, SessionID: sessionID,
		State: model.AttemptParticipationState(row.ParticipationState), Generation: target.Generation,
		RenewalSequence: row.RenewalSequence, ContinuityCredentialHash: row.CredentialHash,
		StartedAt: model.TimeUTC(row.ParticipationStart), UpdatedAt: model.TimeUTC(row.ParticipationUpdate),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.ParticipationEnd),
		EndReason: model.AttemptParticipationEndReason(row.ParticipationReason.String)}
	connection := &model.AttemptConnection{ID: target.ConnectionID, AttemptID: target.AttemptID,
		ParticipationID: target.ParticipationID, SessionID: sessionID, State: model.AttemptConnectionState(row.ConnectionState),
		OpenedAt: model.TimeUTC(row.ConnectionOpenedAt), ClosedAt: OptionalTimeFromNullTime(row.ConnectionClosedAt),
		CloseReason: model.AttemptConnectionCloseReason(row.ConnectionReason.String)}
	if err = attempt.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "value", err)
	}
	if err = participation.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_participation", "value", err)
	}
	if err = connection.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_connection", "value", err)
	}
	return attempt, participation, connection, nil
}

func insertAutomaticExamSubmission(ctx context.Context, tx *sqlxTxWrapper, submission *model.ExamSubmission,
	target store.ExamSubmissionAutomaticSealTarget, rows []examSubmissionManifestPersistenceRow,
	entries []model.ExamSubmissionManifestEntry,
) error {
	if _, err := tx.Exec(ctx, `INSERT INTO exam_submissions
		(id,exam_attempt_id,exam_revision_id,workspace_id,participation_id,generation,connection_id,manifest_schema_version,
		workspace_cursor,manifest_digest,manifest_entry_count,manifest_total_file_bytes,final_focus_loss_sequence,
		browser_activity_state,browser_activity_source_session_id,browser_activity_final_sequence,browser_activity_gap_reason,
		integrity_state,unresolved_integrity_count,provenance,submitted_at,sealed)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,false)`, submission.ID.String(), submission.AttemptID.String(),
		submission.ExamRevisionID.String(), submission.WorkspaceID.String(), target.ParticipationID.String(), target.Generation, target.ConnectionID.String(),
		submission.ManifestSchemaVersion, submission.WorkspaceCursor, submission.ManifestDigest, submission.ManifestEntryCount,
		submission.ManifestTotalFileBytes, submission.FinalFocusLossSequence, string(submission.BrowserActivity.State),
		nullableString(string(submission.BrowserActivity.SourceSessionID)), nullableInt64Pointer(submission.BrowserActivity.FinalSequence),
		nullableString(string(submission.BrowserActivity.GapReason)), string(submission.IntegrityState),
		submission.UnresolvedIntegrityCount, string(submission.Provenance), submission.SubmittedAt); err != nil {
		return fmt.Errorf("insert automatic Exam Submission: %w", translateError("exam_submission", submission.ID.String(), err))
	}
	for index, entry := range entries {
		persisted := rows[index]
		if _, err := tx.Exec(ctx, `INSERT INTO exam_submission_manifest_entries
			(submission_id,workspace_id,entry_id,kind,path,content_version,media_type,size_bytes,sha256,storage_origin,
			starter_object_id,attempt_object_id,workspace_object_id)
			VALUES (?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?)`,
			submission.ID.String(), submission.WorkspaceID.String(), entry.EntryID.String(), string(entry.Kind), entry.Path,
			entry.ContentVersion.String(), entry.MediaType, nullableSubmissionSize(entry), entry.SHA256, string(entry.StorageOrigin),
			entry.StarterObjectID.String(), entry.AttemptObjectID.String(), persisted.WorkspaceObjectID); err != nil {
			return fmt.Errorf("insert automatic Exam Submission manifest Entry: %w", translateError("exam_submission_manifest_entry", entry.EntryID.String(), err))
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_submissions SET sealed=true WHERE id=? AND sealed=false`, submission.ID.String()); err != nil {
		return fmt.Errorf("seal automatic Exam Submission header: %w", err)
	}
	return nil
}

func persistAutomaticSubmittedExamAttempt(ctx context.Context, tx *sqlxTxWrapper, attempt *model.ExamAttempt,
	participation *model.AttemptParticipation, connection *model.AttemptConnection,
	beforeParticipation model.AttemptParticipationState, beforeConnection model.AttemptConnectionState,
) error {
	result, err := tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,submitted_at=?,revision=?
		WHERE id=? AND state IN ('active','ready','suspended') AND revision=?`, string(attempt.State), attempt.UpdatedAt,
		attempt.SubmittedAt.Time, attempt.Revision, attempt.ID.String(), attempt.Revision-1)
	if err != nil {
		return fmt.Errorf("automatically submit Exam Attempt: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return store.NewErrConflict("exam_attempt", "exam_attempt_state", rowsErr)
	}
	if beforeParticipation == model.AttemptParticipationActive {
		result, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=?
			WHERE id=? AND exam_attempt_id=? AND generation=? AND state='active'`, string(participation.State), participation.UpdatedAt,
			participation.EndedAt.Time, string(participation.EndReason), participation.ID.String(), participation.AttemptID.String(), participation.Generation)
		if err != nil {
			return fmt.Errorf("end automatically submitted Participation: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return store.NewErrConflict("attempt_participation", "attempt_participation_expired", rowsErr)
		}
	}
	if beforeConnection == model.AttemptConnectionOpen {
		result, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state=?,closed_at=?,close_reason=?
			WHERE id=? AND exam_attempt_id=? AND participation_id=? AND state='open'`, string(connection.State),
			connection.ClosedAt.Time, string(connection.CloseReason), connection.ID.String(), connection.AttemptID.String(), connection.ParticipationID.String())
		if err != nil {
			return fmt.Errorf("close automatically submitted Connection: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return store.NewErrConflict("attempt_connection", "attempt_connection_closed", rowsErr)
		}
	}
	return nil
}

func automaticExamSubmissionOutcome(submission *model.ExamSubmission, target store.ExamSubmissionAutomaticSealTarget) examSubmissionSealOutcomeV1 {
	return examSubmissionSealOutcomeV1{Receipt: store.ExamSubmissionReceipt{SubmissionID: submission.ID,
		AttemptID: submission.AttemptID, ExamRevisionID: submission.ExamRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: submission.WorkspaceCursor,
		ManifestDigest: submission.ManifestDigest, SubmittedAt: submission.SubmittedAt}, ExamID: target.ExamID.String(),
		SittingID: target.SittingID.String(), ClassID: target.ClassID.String(), CandidateID: target.CandidateUserID.String(),
		ParticipationID: target.ParticipationID.String(), Generation: target.Generation, ConnectionID: target.ConnectionID.String()}
}

func replayAutomaticExamSubmission(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSubmissionAutomaticSeal,
	target store.ExamSubmissionAutomaticSealTarget,
) (*store.ExamSubmissionAutomaticSealResult, error) {
	var row examSubmissionHeaderRow
	if err := tx.Get(ctx, &row, examSubmissionHeaderSelect+` WHERE exam_attempt_id=? AND sealed=true FOR UPDATE`, target.AttemptID.String()); err != nil {
		return nil, translateError("exam_submission", target.AttemptID.String(), err)
	}
	submission, err := row.model()
	if err != nil {
		return nil, err
	}
	var causal struct {
		ParticipationID string `db:"participation_id"`
		ConnectionID    string `db:"connection_id"`
		Generation      int64  `db:"generation"`
	}
	if err = tx.Get(ctx, &causal, `SELECT participation_id,connection_id,generation FROM exam_submissions WHERE id=?`, submission.ID.String()); err != nil {
		return nil, err
	}
	if causal.ParticipationID != target.ParticipationID.String() || causal.ConnectionID != target.ConnectionID.String() ||
		causal.Generation != target.Generation || submission.ExamRevisionID != target.CurrentRevisionID {
		return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	outcome := automaticExamSubmissionOutcome(submission, target)
	if err = completeExamSubmissionAudit(ctx, tx, outcome, input.AuditEventID, input.AuditAt, true, ""); err != nil {
		return nil, err
	}
	result, err := examSubmissionSealResult(outcome)
	if err != nil {
		return nil, err
	}
	result.Replayed = true
	return &store.ExamSubmissionAutomaticSealResult{ExamSubmissionSealResult: *result}, nil
}
