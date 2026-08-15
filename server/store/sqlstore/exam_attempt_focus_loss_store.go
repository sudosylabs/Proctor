// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type focusLossAccessRow struct {
	ExamID                 string         `db:"exam_id"`
	SittingID              string         `db:"sitting_id"`
	ClassID                string         `db:"class_id"`
	CandidateID            string         `db:"candidate_user_id"`
	AttemptID              string         `db:"attempt_id"`
	AdmissionRevisionID    string         `db:"admission_revision_id"`
	AttemptState           string         `db:"attempt_state"`
	AttemptCreatedAt       time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt       time.Time      `db:"attempt_updated_at"`
	AttemptRevision        int64          `db:"attempt_revision"`
	AttemptSubmittedAt     sql.NullTime   `db:"attempt_submitted_at"`
	ParticipationID        string         `db:"participation_id"`
	ParticipationState     string         `db:"participation_state"`
	Generation             int64          `db:"generation"`
	RenewalSequence        int64          `db:"renewal_sequence"`
	CredentialHash         string         `db:"continuity_credential_hash"`
	ParticipationStartedAt time.Time      `db:"participation_started_at"`
	ParticipationUpdatedAt time.Time      `db:"participation_updated_at"`
	LeaseExpiresAt         time.Time      `db:"lease_expires_at"`
	ParticipationEndedAt   sql.NullTime   `db:"participation_ended_at"`
	ParticipationEndReason sql.NullString `db:"participation_end_reason"`
	ConnectionID           string         `db:"connection_id"`
	ConnectionSessionID    string         `db:"connection_session_id"`
	ConnectionState        string         `db:"connection_state"`
	ConnectionOpenedAt     time.Time      `db:"connection_opened_at"`
	ConnectionClosedAt     sql.NullTime   `db:"connection_closed_at"`
	ConnectionCloseReason  sql.NullString `db:"connection_close_reason"`
	SittingState           string         `db:"sitting_state"`
	ScheduledEndAt         time.Time      `db:"scheduled_end_at"`
	PolicyCanonical        []byte         `db:"policy_canonical"`
	SessionArchivedAt      sql.NullTime   `db:"session_archived_at"`
	SessionRevokedAt       sql.NullTime   `db:"session_revoked_at"`
	SessionIdleExpiresAt   time.Time      `db:"session_idle_expires_at"`
	SessionExpiresAt       time.Time      `db:"session_expires_at"`
	UserArchivedAt         sql.NullTime   `db:"user_archived_at"`
	UserDisabledAt         sql.NullTime   `db:"user_disabled_at"`
	DatabaseNow            time.Time      `db:"database_now"`
}

type focusLossEvaluationRow struct {
	AttemptID                       string         `db:"exam_attempt_id"`
	ParticipationID                 string         `db:"participation_id"`
	Generation                      int64          `db:"generation"`
	AcceptedSequence                int64          `db:"accepted_sequence"`
	LastSignalID                    sql.NullString `db:"last_signal_id"`
	LastConnectionID                sql.NullString `db:"last_connection_id"`
	LastDurationMilliseconds        sql.NullInt64  `db:"last_duration_milliseconds"`
	LastSource                      sql.NullString `db:"last_source"`
	LastReceivedAt                  sql.NullTime   `db:"last_received_at"`
	LastCollectionEnabled           bool           `db:"last_collection_enabled"`
	LastQualified                   bool           `db:"last_qualified"`
	LastMissingBefore               int64          `db:"last_missing_before"`
	UnresolvedMissingCount          int64          `db:"unresolved_missing_count"`
	LastWindowIncidentCount         int            `db:"last_window_incident_count"`
	LastThresholdCrossed            bool           `db:"last_threshold_crossed"`
	LastPolicyOutcome               sql.NullString `db:"last_policy_outcome"`
	RetainedEvidenceCount           int            `db:"retained_evidence_count"`
	OverflowCount                   int64          `db:"overflow_count"`
	OverflowFirstReceivedAt         sql.NullTime   `db:"overflow_first_received_at"`
	OverflowLastReceivedAt          sql.NullTime   `db:"overflow_last_received_at"`
	OverflowMaximumDurationMillis   sql.NullInt64  `db:"overflow_maximum_duration_milliseconds"`
	DiagnosticCount                 int64          `db:"diagnostic_count"`
	FlagID                          sql.NullString `db:"integrity_flag_id"`
	WarningCreated                  bool           `db:"warning_created"`
	LastFlagReturned                bool           `db:"last_flag_returned"`
	LastFlagCreated                 bool           `db:"last_flag_created"`
	LastWarningCreated              bool           `db:"last_warning_created"`
	LastManagerNotificationRequired bool           `db:"last_manager_notification_required"`
	LastConnectionClosed            bool           `db:"last_connection_closed"`
	LastSuspensionID                sql.NullString `db:"last_suspension_id"`
}

const focusLossEvaluationColumns = `exam_attempt_id,participation_id,generation,accepted_sequence,last_signal_id,
	last_connection_id,last_duration_milliseconds,last_source,last_received_at,last_collection_enabled,last_qualified,
	last_missing_before,unresolved_missing_count,last_window_incident_count,last_threshold_crossed,last_policy_outcome,retained_evidence_count,
	overflow_count,overflow_first_received_at,overflow_last_received_at,overflow_maximum_duration_milliseconds,
	diagnostic_count,integrity_flag_id,warning_created,last_flag_returned,last_flag_created,last_warning_created,
	last_manager_notification_required,last_connection_closed,last_suspension_id`

func validateFocusLossAccess(access store.ExamAttemptFocusLossAccess) error {
	if !access.AttemptID.IsValid() || !access.ParticipationID.IsValid() || access.Generation < 1 ||
		!access.CandidateUserID.IsValid() || !access.SessionID.IsValid() || !access.ConnectionID.IsValid() ||
		!model.IsValidTokenHash(access.ContinuityCredentialHash) {
		return store.NewErrInvalidInput("focus_loss", "access", nil)
	}
	return nil
}

func validateFocusLossSignal(input *store.ExamAttemptFocusLossSignal) error {
	if input == nil || validateFocusLossAccess(input.Access) != nil || input.SchemaVersion != model.FocusLossSignalSchemaVersion ||
		!input.SignalID.IsValid() || !input.EvidenceID.IsValid() || !input.FlagID.IsValid() || !input.SuspensionID.IsValid() ||
		input.Sequence < 1 || input.DurationMilliseconds < 1 ||
		input.DurationMilliseconds > model.FocusLossMaximumDurationMilliseconds || !input.Source.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("focus_loss", "signal", nil)
	}
	return nil
}

func (s *sqlExamAttemptStore) ResolveFocusLossTarget(ctx context.Context, access store.ExamAttemptFocusLossAccess) (*store.ExamAttemptFocusLossTarget, error) {
	if err := validateFocusLossAccess(access); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Focus Loss target", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptFocusLossTarget, error) {
		row, err := lockFocusLossAccess(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		if activeErr := requireActiveFocusLossAccess(row, access); activeErr != nil {
			state, stateErr := lockFocusLossEvaluation(ctx, tx, access.AttemptID, access.Generation)
			if stateErr != nil || requireFocusLossReplayAccess(row, state, access) != nil {
				return nil, activeErr
			}
		}
		return focusLossTarget(row)
	})
}

func (s *sqlExamAttemptStore) RecordFocusLoss(ctx context.Context, input *store.ExamAttemptFocusLossSignal) (*store.ExamAttemptFocusLossResult, error) {
	if err := validateFocusLossSignal(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "record Focus Loss", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptFocusLossResult, error) {
		access, err := lockFocusLossAccess(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		policySet, err := model.DecodeExamPolicySet(access.PolicyCanonical)
		if err != nil {
			return nil, invalidPersistedState("exam_revision", "policy_canonical", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_focus_loss_evaluations
			(exam_attempt_id,participation_id,generation,updated_at) VALUES (?,?,?,?) ON CONFLICT DO NOTHING`,
			input.Access.AttemptID.String(), input.Access.ParticipationID.String(), input.Access.Generation, access.DatabaseNow); err != nil {
			return nil, fmt.Errorf("initialize Focus Loss evaluation: %w", err)
		}
		state, err := lockFocusLossEvaluation(ctx, tx, input.Access.AttemptID, input.Access.Generation)
		if err != nil {
			return nil, err
		}
		if state.ParticipationID != input.Access.ParticipationID.String() {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
		}
		if input.Sequence <= state.AcceptedSequence {
			if input.Sequence != state.AcceptedSequence || !sameRetainedFocusLossClaim(state, input) {
				return nil, store.NewErrConflict("focus_loss", "focus_loss_sequence", nil)
			}
			if err = requireFocusLossReplayAccess(access, state, input.Access); err != nil {
				return nil, err
			}
			result, loadErr := loadFocusLossResult(ctx, tx, access, state, true)
			if loadErr != nil {
				return nil, loadErr
			}
			if auditErr := completeFocusLossAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); auditErr != nil {
				return nil, auditErr
			}
			return result, nil
		}
		if err = requireActiveFocusLossAccess(access, input.Access); err != nil {
			return nil, err
		}

		signal, err := model.NewFocusLossSignal(input.SignalID, input.Access.AttemptID, input.Access.ParticipationID,
			input.Access.Generation, input.Sequence, input.DurationMilliseconds, input.Source, access.DatabaseNow)
		if err != nil {
			return nil, store.NewErrInvalidInput("focus_loss", "signal", nil).Wrap(err)
		}
		evaluation, err := model.NewFocusLossEvaluation(policySet.FocusLoss, *signal)
		if err != nil {
			return nil, invalidPersistedState("focus_loss", "evaluation", err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending
			WHERE exam_attempt_id=? AND generation=? AND received_at<?`, input.Access.AttemptID.String(), input.Access.Generation,
			evaluation.WindowStartsAt()); err != nil {
			return nil, fmt.Errorf("expire Focus Loss evaluation window: %w", err)
		}
		if evaluation.RetainInWindow() {
			if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_focus_loss_pending
				(exam_attempt_id,participation_id,generation,sequence,signal_id,evidence_id,duration_milliseconds,source,missing_before,received_at)
				VALUES (?,?,?,?,?,?,?,?,0,?)`, input.Access.AttemptID.String(), input.Access.ParticipationID.String(), input.Access.Generation,
				input.Sequence, input.SignalID.String(), input.EvidenceID.String(), input.DurationMilliseconds, nullableFocusLossSource(input.Source),
				access.DatabaseNow); err != nil {
				return nil, fmt.Errorf("append Focus Loss pending claim: %w", translateError("focus_loss", input.SignalID.String(), err))
			}
		}
		var windowCount int
		if err = tx.Get(ctx, &windowCount, `SELECT count(*) FROM exam_attempt_focus_loss_pending
			WHERE exam_attempt_id=? AND generation=?`, input.Access.AttemptID.String(), input.Access.Generation); err != nil {
			return nil, fmt.Errorf("count Focus Loss evaluation window: %w", err)
		}
		decision, err := evaluation.Decide(model.FocusLossEvaluationState{AcceptedSequence: state.AcceptedSequence,
			WindowIncidentCount: windowCount, UnresolvedMissingCount: state.UnresolvedMissingCount,
			DiagnosticCount: state.DiagnosticCount, HasOpenFlag: state.FlagID.Valid, CandidateWarningCreated: state.WarningCreated})
		if err != nil {
			return nil, invalidPersistedState("focus_loss", "evaluation_state", err)
		}
		if evaluation.RetainInWindow() {
			updated, updateErr := tx.Exec(ctx, `UPDATE exam_attempt_focus_loss_pending SET missing_before=?
				WHERE exam_attempt_id=? AND generation=? AND sequence=?`, decision.MissingBefore, input.Access.AttemptID.String(),
				input.Access.Generation, input.Sequence)
			if updateErr != nil {
				return nil, fmt.Errorf("retain Focus Loss gap uncertainty: %w", updateErr)
			}
			if affected, rowsErr := updated.RowsAffected(); rowsErr != nil || affected != 1 {
				if rowsErr != nil {
					return nil, fmt.Errorf("inspect Focus Loss gap retention: %w", rowsErr)
				}
				return nil, invalidPersistedState("focus_loss", "pending_signal", errors.New("current qualifying signal is missing"))
			}
		}
		if decision.CreateFlag {
			flag, flagErr := model.NewIntegrityFlag(input.FlagID, input.Access.AttemptID, input.Access.Generation,
				model.IntegrityPolicyFocusLoss, access.DatabaseNow)
			if flagErr != nil {
				return nil, flagErr
			}
			state.FlagID = sql.NullString{String: flag.ID.String(), Valid: true}
			if _, err = tx.Exec(ctx, `INSERT INTO integrity_flags (id,exam_attempt_id,generation,policy_kind,state,created_at)
				VALUES (?,?,?,?,?,?)`, flag.ID.String(), flag.AttemptID.String(), flag.Generation, flag.Kind, flag.State, flag.CreatedAt); err != nil {
				return nil, fmt.Errorf("create Focus Loss Flag: %w", translateError("integrity_flag", input.FlagID.String(), err))
			}
		}
		if decision.ConsumeWindow {
			if err = consumeFocusLossBucket(ctx, tx, input.Access, &state); err != nil {
				return nil, err
			}
		}
		state.UnresolvedMissingCount = decision.UnresolvedMissingCount
		state.DiagnosticCount = decision.DiagnosticCount
		if decision.CreateCandidateWarning {
			state.WarningCreated = true
		}
		connectionClosed := false
		var suspensionID sql.NullString
		if decision.Suspend {
			suspensionID = sql.NullString{String: input.SuspensionID.String(), Valid: true}
			connectionClosed, err = suspendForFocusLoss(ctx, tx, access, state.FlagID.String, input.SuspensionID)
			if err != nil {
				return nil, err
			}
		}
		outcome := any(nil)
		if decision.PolicyOutcome != "" {
			outcome = string(decision.PolicyOutcome)
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_focus_loss_evaluations SET
			accepted_sequence=?,last_signal_id=?,last_connection_id=?,last_duration_milliseconds=?,last_source=?,last_received_at=?,
			last_collection_enabled=?,last_qualified=?,last_missing_before=?,unresolved_missing_count=?,last_window_incident_count=?,last_threshold_crossed=?,
			last_policy_outcome=?,retained_evidence_count=?,overflow_count=?,overflow_first_received_at=?,overflow_last_received_at=?,
			overflow_maximum_duration_milliseconds=?,diagnostic_count=?,integrity_flag_id=?,warning_created=?,last_flag_returned=?,
			last_flag_created=?,last_warning_created=?,last_manager_notification_required=?,last_connection_closed=?,last_suspension_id=?,updated_at=?
			WHERE exam_attempt_id=? AND generation=?`, input.Sequence, input.SignalID.String(), input.Access.ConnectionID.String(),
			input.DurationMilliseconds, nullableFocusLossSource(input.Source), access.DatabaseNow, decision.CollectionEnabled, decision.Qualified, decision.MissingBefore,
			state.UnresolvedMissingCount, decision.WindowIncidentCount, decision.ThresholdCrossed, outcome, state.RetainedEvidenceCount, state.OverflowCount,
			focusSQLNullableTime(state.OverflowFirstReceivedAt), focusSQLNullableTime(state.OverflowLastReceivedAt), focusSQLNullableInt64(state.OverflowMaximumDurationMillis),
			state.DiagnosticCount, focusSQLNullableString(state.FlagID), state.WarningCreated, decision.ThresholdCrossed, decision.CreateFlag, decision.CreateCandidateWarning,
			decision.NotifyManagers, connectionClosed, focusSQLNullableString(suspensionID), access.DatabaseNow,
			input.Access.AttemptID.String(), input.Access.Generation); err != nil {
			return nil, fmt.Errorf("retain Focus Loss outcome: %w", err)
		}
		state, err = lockFocusLossEvaluation(ctx, tx, input.Access.AttemptID, input.Access.Generation)
		if err != nil {
			return nil, err
		}
		result, err := loadFocusLossResult(ctx, tx, access, state, false)
		if err != nil {
			return nil, err
		}
		if err = completeFocusLossAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func lockFocusLossAccess(ctx context.Context, tx *sqlxTxWrapper, input store.ExamAttemptFocusLossAccess) (focusLossAccessRow, error) {
	var zero focusLossAccessRow
	var row focusLossAccessRow
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id AS sitting_id,s.class_id,
		a.candidate_user_id,a.id AS attempt_id,a.admission_revision_id,a.state AS attempt_state,
		a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.revision AS attempt_revision,a.submitted_at AS attempt_submitted_at,
		p.id AS participation_id,p.state AS participation_state,p.generation,p.renewal_sequence,p.continuity_credential_hash,
		p.started_at AS participation_started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,
		p.ended_at AS participation_ended_at,p.end_reason AS participation_end_reason,
		co.id AS connection_id,co.session_id AS connection_session_id,co.state AS connection_state,co.opened_at AS connection_opened_at,
		co.closed_at AS connection_closed_at,co.close_reason AS connection_close_reason,s.state AS sitting_state,s.scheduled_end_at,
		r.policy_canonical,se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
		se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
		u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_revisions r ON r.id=a.admission_revision_id AND r.sealed=true
		JOIN exam_attempt_participations p ON p.id=? AND p.exam_attempt_id=a.id
		JOIN exam_attempt_connections co ON co.id=? AND co.participation_id=p.id AND co.exam_attempt_id=a.id
		JOIN sessions se ON se.id=co.session_id JOIN users u ON u.id=se.user_id
		WHERE a.id=? AND a.candidate_user_id=? FOR UPDATE OF a,p,co FOR SHARE OF s,r,se,u`,
		input.ParticipationID.String(), input.ConnectionID.String(), input.AttemptID.String(), input.CandidateUserID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return zero, focusLossAccessDenied(input.AttemptID)
	}
	if err != nil {
		return zero, fmt.Errorf("lock Focus Loss access: %w", err)
	}
	if err = tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read Focus Loss decision time: %w", err)
	}
	row.DatabaseNow = model.TimeUTC(row.DatabaseNow)
	if row.ConnectionSessionID != input.SessionID.String() || row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid ||
		!row.DatabaseNow.Before(row.SessionIdleExpiresAt) || !row.DatabaseNow.Before(row.SessionExpiresAt) ||
		row.UserArchivedAt.Valid || row.UserDisabledAt.Valid {
		return zero, focusLossAccessDenied(input.AttemptID)
	}
	if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if row.Generation != input.Generation {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	}
	if (row.SittingState != string(model.ExamSittingOpen) && row.SittingState != string(model.ExamSittingPaused)) ||
		!row.DatabaseNow.Before(row.ScheduledEndAt) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	return row, nil
}

func requireActiveFocusLossAccess(row focusLossAccessRow, input store.ExamAttemptFocusLossAccess) error {
	if row.AttemptState != string(model.ExamAttemptActive) {
		return store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	if row.ParticipationState != string(model.AttemptParticipationActive) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if !row.DatabaseNow.Before(row.LeaseExpiresAt) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if row.ConnectionState != string(model.AttemptConnectionOpen) {
		return store.NewErrConflict("attempt_connection", "attempt_connection_closed", nil)
	}
	return nil
}

func requireFocusLossReplayAccess(row focusLossAccessRow, state focusLossEvaluationRow, input store.ExamAttemptFocusLossAccess) error {
	activeErr := requireActiveFocusLossAccess(row, input)
	if activeErr == nil {
		return nil
	}
	if !state.LastSuspensionID.Valid || !state.LastConnectionID.Valid || state.LastConnectionID.String != input.ConnectionID.String() ||
		row.AttemptState != string(model.ExamAttemptSuspended) || row.ParticipationState != string(model.AttemptParticipationEnded) ||
		row.ParticipationEndReason.String != string(model.AttemptParticipationEndPolicySuspended) ||
		row.ConnectionState != string(model.AttemptConnectionClosed) ||
		row.ConnectionCloseReason.String != string(model.AttemptConnectionClosePolicySuspended) {
		return activeErr
	}
	return nil
}

func focusLossAccessDenied(attemptID model.ExamAttemptID) error {
	return store.NewErrNotFound("exam_attempt_access", attemptID.String())
}

func focusLossTarget(row focusLossAccessRow) (*store.ExamAttemptFocusLossTarget, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "class_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "id", err)
	}
	return &store.ExamAttemptFocusLossTarget{ExamID: examID, SittingID: sittingID, ClassID: classID, CandidateUserID: candidateID,
		AttemptID: attemptID, ParticipationID: participationID, Generation: row.Generation}, nil
}

func lockFocusLossEvaluation(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID, generation int64) (focusLossEvaluationRow, error) {
	var row focusLossEvaluationRow
	if err := tx.Get(ctx, &row, `SELECT `+focusLossEvaluationColumns+` FROM exam_attempt_focus_loss_evaluations
		WHERE exam_attempt_id=? AND generation=? FOR UPDATE`, attemptID.String(), generation); err != nil {
		return row, fmt.Errorf("lock Focus Loss evaluation: %w", err)
	}
	return row, nil
}

func sameRetainedFocusLossClaim(state focusLossEvaluationRow, input *store.ExamAttemptFocusLossSignal) bool {
	return state.LastDurationMilliseconds.Valid && state.LastDurationMilliseconds.Int64 == input.DurationMilliseconds &&
		state.LastSource.String == string(input.Source)
}

type focusLossPendingRow struct {
	SignalID       string         `db:"signal_id"`
	EvidenceID     string         `db:"evidence_id"`
	Sequence       int64          `db:"sequence"`
	DurationMillis int64          `db:"duration_milliseconds"`
	Source         sql.NullString `db:"source"`
	MissingBefore  int64          `db:"missing_before"`
	ReceivedAt     time.Time      `db:"received_at"`
}

func consumeFocusLossBucket(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptFocusLossAccess, state *focusLossEvaluationRow) error {
	var pending []focusLossPendingRow
	if err := tx.Select(ctx, &pending, `SELECT signal_id,evidence_id,sequence,duration_milliseconds,source,missing_before,received_at
		FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=? ORDER BY received_at,sequence`,
		access.AttemptID.String(), access.Generation); err != nil {
		return fmt.Errorf("load consumed Focus Loss bucket: %w", err)
	}
	for _, episode := range pending {
		if state.RetainedEvidenceCount < model.FocusLossMaximumEvidenceEpisodes {
			if _, err := tx.Exec(ctx, `INSERT INTO integrity_evidence
				(id,exam_attempt_id,participation_id,integrity_flag_id,generation,policy_kind,focus_loss_signal_id,sequence,
				duration_milliseconds,source,missing_before,observed_at,recorded_at)
				VALUES (?,?,?,?,?,'focus_loss',?,?,?,?,?,?,statement_timestamp())`, episode.EvidenceID, access.AttemptID.String(),
				access.ParticipationID.String(), state.FlagID.String, access.Generation, episode.SignalID, episode.Sequence,
				episode.DurationMillis, focusSQLNullableString(episode.Source), episode.MissingBefore, episode.ReceivedAt); err != nil {
				return fmt.Errorf("retain Focus Loss evidence: %w", translateError("integrity_evidence", episode.EvidenceID, err))
			}
			state.RetainedEvidenceCount++
			continue
		}
		if state.OverflowCount < math.MaxInt64 {
			state.OverflowCount++
		}
		if !state.OverflowFirstReceivedAt.Valid {
			state.OverflowFirstReceivedAt = sql.NullTime{Time: episode.ReceivedAt, Valid: true}
		}
		state.OverflowLastReceivedAt = sql.NullTime{Time: episode.ReceivedAt, Valid: true}
		if !state.OverflowMaximumDurationMillis.Valid || episode.DurationMillis > state.OverflowMaximumDurationMillis.Int64 {
			state.OverflowMaximumDurationMillis = sql.NullInt64{Int64: episode.DurationMillis, Valid: true}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=?`,
		access.AttemptID.String(), access.Generation); err != nil {
		return fmt.Errorf("consume Focus Loss bucket: %w", err)
	}
	return nil
}

func suspendForFocusLoss(ctx context.Context, tx *sqlxTxWrapper, row focusLossAccessRow, flagID string, suspensionID model.AttemptSuspensionID) (bool, error) {
	attempt, participation, connection, err := row.enforcementDomain()
	if err != nil {
		return false, err
	}
	domainFlagID, err := model.ParseIntegrityFlagID(flagID)
	if err != nil {
		return false, invalidPersistedState("integrity_flag", "id", err)
	}
	if err = model.SuspendExamAttemptForFocusLoss(attempt, participation, connection, row.DatabaseNow); err != nil {
		return false, fmt.Errorf("apply Focus Loss suspension: %w", err)
	}
	suspension, err := model.NewPolicyAttemptSuspension(suspensionID, attempt.ID, participation.ID, domainFlagID,
		row.Generation, model.AttemptSuspensionCandidateReasonFocusLossPolicy, row.DatabaseNow)
	if err != nil {
		return false, fmt.Errorf("construct Focus Loss Suspension: %w", err)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,revision=?
		WHERE id=? AND revision=? AND state=?`, attempt.State, attempt.UpdatedAt, attempt.Revision,
		attempt.ID.String(), row.AttemptRevision, model.ExamAttemptActive)
	if err != nil {
		return false, fmt.Errorf("suspend Exam Attempt for Focus Loss: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return false, fmt.Errorf("inspect Focus Loss Attempt suspension: %w", rowsErr)
		}
		return false, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	result, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=?
		WHERE id=? AND exam_attempt_id=? AND generation=? AND state=?`, participation.State, participation.UpdatedAt,
		participation.EndedAt.Time, participation.EndReason, participation.ID.String(), attempt.ID.String(), participation.Generation,
		model.AttemptParticipationActive)
	if err != nil {
		return false, fmt.Errorf("end Attempt Participation for Focus Loss: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return false, fmt.Errorf("inspect Focus Loss Participation end: %w", rowsErr)
		}
		return false, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	result, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state=?,closed_at=?,close_reason=?
		WHERE id=? AND exam_attempt_id=? AND participation_id=? AND state=?`, connection.State, connection.ClosedAt.Time,
		connection.CloseReason, connection.ID.String(), attempt.ID.String(), participation.ID.String(), model.AttemptConnectionOpen)
	if err != nil {
		return false, fmt.Errorf("close Attempt Connection for Focus Loss: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		if err != nil {
			return false, fmt.Errorf("inspect Focus Loss Connection close: %w", err)
		}
		return false, store.NewErrConflict("attempt_connection", "attempt_connection_open", nil)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_suspensions
		(id,exam_attempt_id,participation_id,integrity_flag_id,generation,suspension_attempt_revision,state,source,candidate_reason,started_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, suspension.ID.String(), suspension.AttemptID.String(), suspension.ParticipationID.String(),
		suspension.FlagID.String(), suspension.Generation, row.AttemptRevision+1, suspension.State, suspension.Source,
		suspension.CandidateReason, suspension.StartedAt); err != nil {
		return false, fmt.Errorf("create Focus Loss Suspension: %w", translateError("attempt_suspension", suspensionID.String(), err))
	}
	return true, nil
}

func (row focusLossAccessRow) enforcementDomain() (*model.ExamAttempt, *model.AttemptParticipation, *model.AttemptConnection, error) {
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.AttemptSubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("exam_attempt", "value", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_participation", "id", err)
	}
	participation := &model.AttemptParticipation{ID: participationID, AttemptID: attemptID,
		State: model.AttemptParticipationState(row.ParticipationState), Generation: row.Generation, RenewalSequence: row.RenewalSequence,
		ContinuityCredentialHash: row.CredentialHash, StartedAt: model.TimeUTC(row.ParticipationStartedAt),
		UpdatedAt: model.TimeUTC(row.ParticipationUpdatedAt), LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt),
		EndedAt: OptionalTimeFromNullTime(row.ParticipationEndedAt), EndReason: model.AttemptParticipationEndReason(row.ParticipationEndReason.String)}
	if err = participation.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_participation", "value", err)
	}
	connectionID, err := model.ParseAttemptConnectionID(row.ConnectionID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_connection", "id", err)
	}
	sessionID, err := model.ParseSessionID(row.ConnectionSessionID)
	if err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_connection", "session_id", err)
	}
	connection := &model.AttemptConnection{ID: connectionID, AttemptID: attemptID, ParticipationID: participationID,
		SessionID: sessionID, State: model.AttemptConnectionState(row.ConnectionState), OpenedAt: model.TimeUTC(row.ConnectionOpenedAt),
		ClosedAt: OptionalTimeFromNullTime(row.ConnectionClosedAt), CloseReason: model.AttemptConnectionCloseReason(row.ConnectionCloseReason.String)}
	if err = connection.Validate(); err != nil {
		return nil, nil, nil, invalidPersistedState("attempt_connection", "value", err)
	}
	return attempt, participation, connection, nil
}

func nullableFocusLossSource(source model.FocusLossSource) any {
	if source == "" {
		return nil
	}
	return string(source)
}

func focusSQLNullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func focusSQLNullableTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func focusSQLNullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func loadFocusLossResult(ctx context.Context, tx *sqlxTxWrapper, access focusLossAccessRow, state focusLossEvaluationRow, duplicate bool) (*store.ExamAttemptFocusLossResult, error) {
	target, err := focusLossTarget(access)
	if err != nil {
		return nil, err
	}
	signalID, err := model.ParseFocusLossSignalID(state.LastSignalID.String)
	if err != nil {
		return nil, invalidPersistedState("focus_loss", "signal_id", err)
	}
	signal, err := model.NewFocusLossSignal(signalID, target.AttemptID, target.ParticipationID, target.Generation,
		state.AcceptedSequence, state.LastDurationMilliseconds.Int64, model.FocusLossSource(state.LastSource.String), state.LastReceivedAt.Time)
	if err != nil {
		return nil, invalidPersistedState("focus_loss", "signal", err)
	}
	result := &store.ExamAttemptFocusLossResult{ExamID: target.ExamID, SittingID: target.SittingID, ClassID: target.ClassID,
		CandidateUserID: target.CandidateUserID, AttemptID: target.AttemptID, ParticipationID: target.ParticipationID,
		Generation: target.Generation, Signal: signal, AcceptedSequence: state.AcceptedSequence, DatabaseTime: signal.ReceivedAt,
		CollectionEnabled: state.LastCollectionEnabled, Qualified: state.LastQualified, MissingBefore: state.LastMissingBefore,
		WindowIncidentCount: state.LastWindowIncidentCount, ThresholdCrossed: state.LastThresholdCrossed,
		PolicyOutcome: model.IntegrityThresholdOutcome(state.LastPolicyOutcome.String), RetainedEvidenceCount: state.RetainedEvidenceCount,
		DiagnosticCount: state.DiagnosticCount, FlagCreated: state.LastFlagCreated, CandidateWarningCreated: state.LastWarningCreated,
		ManagerNotificationRequired: state.LastManagerNotificationRequired, ConnectionClosed: state.LastConnectionClosed, Duplicate: duplicate}
	if state.OverflowCount > 0 {
		result.Overflow = &model.FocusLossEvidenceOverflow{AttemptID: target.AttemptID, ParticipationID: target.ParticipationID,
			Generation: target.Generation, Count: state.OverflowCount, FirstReceivedAt: model.TimeUTC(state.OverflowFirstReceivedAt.Time),
			LastReceivedAt: model.TimeUTC(state.OverflowLastReceivedAt.Time), MaximumDurationMilliseconds: state.OverflowMaximumDurationMillis.Int64}
		if err = result.Overflow.Validate(); err != nil {
			return nil, invalidPersistedState("focus_loss", "overflow", err)
		}
	}
	if state.LastFlagReturned {
		result.Flag, err = loadFocusLossFlag(ctx, tx, state.FlagID.String)
		if err != nil {
			return nil, err
		}
	}
	if state.LastSuspensionID.Valid {
		if err = loadFocusLossSuspensionResult(ctx, tx, access, state.LastSuspensionID.String, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func loadFocusLossFlag(ctx context.Context, tx *sqlxTxWrapper, id string) (*model.IntegrityFlag, error) {
	var row struct {
		ID         string    `db:"id"`
		AttemptID  string    `db:"attempt_id"`
		Kind       string    `db:"kind"`
		State      string    `db:"state"`
		Generation int64     `db:"generation"`
		CreatedAt  time.Time `db:"created_at"`
	}
	if err := tx.Get(ctx, &row, `SELECT id,exam_attempt_id AS attempt_id,generation,policy_kind AS kind,state,created_at
		FROM integrity_flags WHERE id=?`, id); err != nil {
		return nil, fmt.Errorf("load Focus Loss Flag: %w", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("integrity_flag", "id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("integrity_flag", "exam_attempt_id", err)
	}
	flag := &model.IntegrityFlag{ID: flagID, AttemptID: attemptID, Generation: row.Generation, Kind: model.IntegrityPolicyKind(row.Kind), State: model.IntegrityFlagState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt)}
	if err = flag.Validate(); err != nil {
		return nil, invalidPersistedState("integrity_flag", "value", err)
	}
	return flag, nil
}

func loadFocusLossSuspensionResult(ctx context.Context, tx *sqlxTxWrapper, access focusLossAccessRow, suspensionID string, result *store.ExamAttemptFocusLossResult) error {
	var row struct {
		AdmissionRevisionID string         `db:"admission_revision_id"`
		AttemptState        string         `db:"attempt_state"`
		AttemptCreatedAt    time.Time      `db:"attempt_created_at"`
		AttemptUpdatedAt    time.Time      `db:"attempt_updated_at"`
		AttemptRevision     int64          `db:"attempt_revision"`
		SubmittedAt         sql.NullTime   `db:"submitted_at"`
		ParticipationState  string         `db:"participation_state"`
		RenewalSequence     int64          `db:"renewal_sequence"`
		StartedAt           time.Time      `db:"started_at"`
		ParticipationAt     time.Time      `db:"participation_updated_at"`
		LeaseExpiresAt      time.Time      `db:"lease_expires_at"`
		EndedAt             sql.NullTime   `db:"ended_at"`
		EndReason           sql.NullString `db:"end_reason"`
		ConnectionState     string         `db:"connection_state"`
		ConnectionOpenedAt  time.Time      `db:"connection_opened_at"`
		ConnectionClosedAt  sql.NullTime   `db:"connection_closed_at"`
		ConnectionReason    sql.NullString `db:"connection_reason"`
		FlagID              string         `db:"flag_id"`
		SuspensionState     string         `db:"suspension_state"`
		SuspensionSource    string         `db:"suspension_source"`
		CandidateReason     string         `db:"candidate_reason"`
		SuspensionStartedAt time.Time      `db:"suspension_started_at"`
		SuspensionEndedAt   sql.NullTime   `db:"suspension_ended_at"`
		ReallowedBy         sql.NullString `db:"reallowed_by_user_id"`
		PrivateReason       sql.NullString `db:"private_reason"`
	}
	if err := tx.Get(ctx, &row, `SELECT a.admission_revision_id,a.state AS attempt_state,a.created_at AS attempt_created_at,
		a.updated_at AS attempt_updated_at,a.revision AS attempt_revision,a.submitted_at,p.state AS participation_state,
		p.renewal_sequence,p.started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,p.ended_at,p.end_reason,
		c.state AS connection_state,c.opened_at AS connection_opened_at,c.closed_at AS connection_closed_at,c.close_reason AS connection_reason,
		su.integrity_flag_id AS flag_id,su.state AS suspension_state,su.source AS suspension_source,su.candidate_reason,
		su.started_at AS suspension_started_at,su.ended_at AS suspension_ended_at,su.reallowed_by_user_id,su.private_reason
		FROM exam_attempts a JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id AND p.id=?
		JOIN exam_attempt_connections c ON c.exam_attempt_id=a.id AND c.participation_id=p.id AND c.id=?
		JOIN exam_attempt_suspensions su ON su.exam_attempt_id=a.id AND su.participation_id=p.id AND su.id=?
		WHERE a.id=?`, result.ParticipationID.String(), access.ConnectionID, suspensionID, result.AttemptID.String()); err != nil {
		return fmt.Errorf("load Focus Loss suspension outcome: %w", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	attempt := &model.ExamAttempt{ID: result.AttemptID, ExamID: result.ExamID, SittingID: result.SittingID, CandidateUserID: result.CandidateUserID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return invalidPersistedState("exam_attempt", "value", err)
	}
	domainParticipation := &model.AttemptParticipation{ID: result.ParticipationID, AttemptID: result.AttemptID,
		State: model.AttemptParticipationState(row.ParticipationState), Generation: result.Generation, RenewalSequence: row.RenewalSequence,
		ContinuityCredentialHash: access.CredentialHash, StartedAt: model.TimeUTC(row.StartedAt), UpdatedAt: model.TimeUTC(row.ParticipationAt),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	if err = domainParticipation.Validate(); err != nil {
		return invalidPersistedState("attempt_participation", "value", err)
	}
	participation := &store.ExamAttemptParticipationView{ID: domainParticipation.ID, AttemptID: domainParticipation.AttemptID,
		State: domainParticipation.State, Generation: domainParticipation.Generation, RenewalSequence: domainParticipation.RenewalSequence,
		StartedAt: domainParticipation.StartedAt, UpdatedAt: domainParticipation.UpdatedAt, LeaseExpiresAt: domainParticipation.LeaseExpiresAt,
		EndedAt: domainParticipation.EndedAt, EndReason: domainParticipation.EndReason}
	connectionID, err := model.ParseAttemptConnectionID(access.ConnectionID)
	if err != nil {
		return invalidPersistedState("attempt_connection", "id", err)
	}
	sessionID, err := model.ParseSessionID(access.ConnectionSessionID)
	if err != nil {
		return invalidPersistedState("attempt_connection", "session_id", err)
	}
	domainConnection := &model.AttemptConnection{ID: connectionID, AttemptID: result.AttemptID, ParticipationID: result.ParticipationID,
		SessionID: sessionID, State: model.AttemptConnectionState(row.ConnectionState), OpenedAt: model.TimeUTC(row.ConnectionOpenedAt),
		ClosedAt: OptionalTimeFromNullTime(row.ConnectionClosedAt), CloseReason: model.AttemptConnectionCloseReason(row.ConnectionReason.String)}
	if err = domainConnection.Validate(); err != nil {
		return invalidPersistedState("attempt_connection", "value", err)
	}
	connection := &store.ExamAttemptManagerConnection{ID: connectionID, State: domainConnection.State,
		OpenedAt: domainConnection.OpenedAt, ClosedAt: domainConnection.ClosedAt, CloseReason: domainConnection.CloseReason}
	suspID, err := model.ParseAttemptSuspensionID(suspensionID)
	if err != nil {
		return invalidPersistedState("attempt_suspension", "id", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.FlagID)
	if err != nil {
		return invalidPersistedState("attempt_suspension", "integrity_flag_id", err)
	}
	domainSuspension := &model.AttemptSuspension{ID: suspID, AttemptID: result.AttemptID, ParticipationID: result.ParticipationID,
		FlagID: flagID, Generation: result.Generation, State: model.AttemptSuspensionState(row.SuspensionState),
		Source: model.AttemptSuspensionSource(row.SuspensionSource), CandidateReason: model.AttemptSuspensionCandidateReason(row.CandidateReason),
		StartedAt: model.TimeUTC(row.SuspensionStartedAt), EndedAt: OptionalTimeFromNullTime(row.SuspensionEndedAt)}
	if row.ReallowedBy.Valid {
		domainSuspension.ReallowedByUserID, err = model.ParseUserID(row.ReallowedBy.String)
		if err != nil {
			return invalidPersistedState("attempt_suspension", "reallowed_by_user_id", err)
		}
	}
	if row.PrivateReason.Valid {
		domainSuspension.PrivateReason = row.PrivateReason.String
	}
	if err = domainSuspension.Validate(); err != nil {
		return invalidPersistedState("attempt_suspension", "value", err)
	}
	suspension := &store.ExamAttemptSuspensionView{ID: domainSuspension.ID, AttemptID: domainSuspension.AttemptID,
		ParticipationID: domainSuspension.ParticipationID, FlagID: domainSuspension.FlagID, Generation: domainSuspension.Generation,
		State: domainSuspension.State, Source: domainSuspension.Source, CandidateReason: domainSuspension.CandidateReason,
		StartedAt: domainSuspension.StartedAt, EndedAt: domainSuspension.EndedAt, ReallowedByUserID: domainSuspension.ReallowedByUserID}
	result.Attempt, result.Participation, result.Connection, result.Suspension = attempt, participation, connection, suspension
	return nil
}

func completeFocusLossAudit(ctx context.Context, tx *sqlxTxWrapper, result *store.ExamAttemptFocusLossResult, auditID string, auditAt int64) error {
	data, err := model.EncodeAuditData(map[string]any{"exam_id": result.ExamID.String(), "exam_sitting_id": result.SittingID.String(),
		"exam_attempt_id": result.AttemptID.String(), "participation_id": result.ParticipationID.String(), "generation": result.Generation,
		"signal_id": result.Signal.ID.String(), "accepted_sequence": result.AcceptedSequence, "replayed": result.Duplicate})
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", data, auditAt); err != nil {
		return fmt.Errorf("complete Focus Loss audit: %w", err)
	}
	return nil
}
