// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

// SQLExamSubmissionStore is the PostgreSQL adapter for voluntary terminal
// sealing and immutable manager inspection.
type SQLExamSubmissionStore struct{ *SQLStore }

// NewSQLExamSubmissionStore constructs an independently usable Submission
// adapter for root registration and multi-node conformance tests.
func NewSQLExamSubmissionStore(sqlStore *SQLStore) store.ExamSubmissionStore {
	return &SQLExamSubmissionStore{SQLStore: sqlStore}
}

type examSubmissionSealAccessRow struct {
	ExamID                  string         `db:"exam_id"`
	SittingID               string         `db:"exam_sitting_id"`
	ClassID                 string         `db:"class_id"`
	AcademicPeriodID        string         `db:"academic_period_id"`
	CandidateID             string         `db:"candidate_user_id"`
	AdmissionRevisionID     string         `db:"admission_revision_id"`
	CurrentRevisionID       string         `db:"current_revision_id"`
	AttemptState            string         `db:"attempt_state"`
	AttemptCreatedAt        time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt        time.Time      `db:"attempt_updated_at"`
	AttemptSubmittedAt      sql.NullTime   `db:"attempt_submitted_at"`
	AttemptRevision         int64          `db:"attempt_revision"`
	SittingState            string         `db:"sitting_state"`
	ScheduledEndAt          time.Time      `db:"scheduled_end_at"`
	WorkspaceID             string         `db:"workspace_id"`
	WorkspaceCursor         int64          `db:"workspace_cursor"`
	ParticipationState      string         `db:"participation_state"`
	ParticipationGeneration int64          `db:"participation_generation"`
	RenewalSequence         int64          `db:"renewal_sequence"`
	CredentialHash          string         `db:"continuity_credential_hash"`
	ParticipationStartedAt  time.Time      `db:"participation_started_at"`
	ParticipationUpdatedAt  time.Time      `db:"participation_updated_at"`
	LeaseExpiresAt          time.Time      `db:"lease_expires_at"`
	ParticipationEndedAt    sql.NullTime   `db:"participation_ended_at"`
	ParticipationEndReason  sql.NullString `db:"participation_end_reason"`
	ConnectionState         string         `db:"connection_state"`
	ConnectionOpenedAt      time.Time      `db:"connection_opened_at"`
	ConnectionClosedAt      sql.NullTime   `db:"connection_closed_at"`
	ConnectionCloseReason   sql.NullString `db:"connection_close_reason"`
	SessionArchivedAt       sql.NullTime   `db:"session_archived_at"`
	SessionRevokedAt        sql.NullTime   `db:"session_revoked_at"`
	SessionIdleExpiresAt    time.Time      `db:"session_idle_expires_at"`
	SessionExpiresAt        time.Time      `db:"session_expires_at"`
	UserArchivedAt          sql.NullTime   `db:"user_archived_at"`
	UserDisabledAt          sql.NullTime   `db:"user_disabled_at"`
	ClassArchivedAt         sql.NullTime   `db:"class_archived_at"`
	LevelArchivedAt         sql.NullTime   `db:"level_archived_at"`
	ProgrammeArchivedAt     sql.NullTime   `db:"programme_archived_at"`
	UnitArchivedAt          sql.NullTime   `db:"unit_archived_at"`
	PeriodArchivedAt        sql.NullTime   `db:"period_archived_at"`
}

func validExamSubmissionSealAccess(access store.ExamSubmissionSealAccess) bool {
	return access.AttemptID.IsValid() && access.ParticipationID.IsValid() && access.Generation > 0 &&
		access.ConnectionID.IsValid() && access.CandidateUserID.IsValid() && access.SessionID.IsValid() &&
		model.IsValidTokenHash(access.ContinuityCredentialHash) && access.ExpectedWorkspaceCursor >= 0 &&
		access.ExpectedCurrentRevisionID.IsValid() && access.FinalFocusLossSequence >= 0 && access.BrowserActivity.ValidateClient() == nil
}

func (s *SQLExamSubmissionStore) ResolveSealTarget(ctx context.Context, access store.ExamSubmissionSealAccess) (*store.ExamSubmissionSealTarget, error) {
	if !validExamSubmissionSealAccess(access) {
		return nil, store.NewErrInvalidInput("exam_submission", "seal_access", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Exam Submission seal target", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSubmissionSealTarget, error) {
		row, _, databaseNow, err := lockExamSubmissionSealAccess(ctx, tx, access, true)
		if err != nil {
			return nil, err
		}
		target, targetErr := examSubmissionSealTarget(row)
		if targetErr != nil {
			return nil, targetErr
		}
		target.Replayed = row.AttemptState == string(model.ExamAttemptSubmitted)
		target.SealAt = model.TimeFromMillis(model.MillisFromTime(databaseNow))
		return target, nil
	})
}

func lockExamSubmissionSealAccess(ctx context.Context, tx *sqlxTxWrapper, access store.ExamSubmissionSealAccess,
	allowCommittedCausal bool,
) (examSubmissionSealAccessRow, examSubmissionIntegrityTail, time.Time, error) {
	var zero examSubmissionSealAccessRow
	if !validExamSubmissionSealAccess(access) {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrInvalidInput("exam_submission", "seal_access", nil)
	}
	var periodID string
	if err := tx.Get(ctx, &periodID, `SELECT cl.academic_period_id FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN classes cl ON cl.id=s.class_id WHERE a.id=? AND a.candidate_user_id=?`,
		access.AttemptID.String(), access.CandidateUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrNotFound("exam_submission_access", access.AttemptID.String())
		}
		return zero, examSubmissionIntegrityTail{}, time.Time{}, fmt.Errorf("resolve Exam Submission enrollment fence: %w", err)
	}
	if err := lockClassEnrollment(ctx, tx, access.CandidateUserID.String(), periodID); err != nil {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, err
	}
	var row examSubmissionSealAccessRow
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,cl.academic_period_id,a.candidate_user_id,
		a.admission_revision_id,COALESCE(retained.exam_revision_id,s.exam_revision_id) AS current_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,
		a.updated_at AS attempt_updated_at,a.submitted_at AS attempt_submitted_at,a.revision AS attempt_revision,
		s.state AS sitting_state,s.scheduled_end_at,w.id AS workspace_id,w.cursor AS workspace_cursor,
		p.state AS participation_state,p.generation AS participation_generation,p.renewal_sequence,
		p.continuity_credential_hash,p.started_at AS participation_started_at,p.updated_at AS participation_updated_at,
		p.lease_expires_at,p.ended_at AS participation_ended_at,p.end_reason AS participation_end_reason,
		c.state AS connection_state,c.opened_at AS connection_opened_at,c.closed_at AS connection_closed_at,
		c.close_reason AS connection_close_reason,se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
		se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
		u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at,cl.archived_at AS class_archived_at,
		pl.archived_at AS level_archived_at,pr.archived_at AS programme_archived_at,
		au.archived_at AS unit_archived_at,ap.archived_at AS period_archived_at
		FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		LEFT JOIN exam_submissions retained ON retained.exam_attempt_id=a.id AND retained.sealed=true
		JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		JOIN exam_attempt_participations p ON p.id=? AND p.exam_attempt_id=a.id
		JOIN exam_attempt_connections c ON c.id=? AND c.exam_attempt_id=a.id AND c.participation_id=p.id
		JOIN sessions se ON se.id=? AND se.user_id=a.candidate_user_id AND c.session_id=se.id
		JOIN users u ON u.id=a.candidate_user_id JOIN classes cl ON cl.id=s.class_id
		JOIN programme_levels pl ON pl.id=cl.programme_level_id JOIN programmes pr ON pr.id=pl.programme_id
		JOIN academic_units au ON au.id=pr.academic_unit_id
		JOIN academic_periods ap ON ap.id=cl.academic_period_id AND ap.institution_id=au.institution_id
		WHERE a.id=? AND a.candidate_user_id=?
		FOR UPDATE OF a,p,c,w FOR SHARE OF s,se,u,cl,pl,pr,au,ap`, access.ParticipationID.String(),
		access.ConnectionID.String(), access.SessionID.String(), access.AttemptID.String(), access.CandidateUserID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrNotFound("exam_submission_access", access.AttemptID.String())
		}
		return zero, examSubmissionIntegrityTail{}, time.Time{}, fmt.Errorf("lock Exam Submission causal access: %w", err)
	}
	var memberships []struct {
		StartAt    time.Time    `db:"start_at"`
		EndAt      sql.NullTime `db:"end_at"`
		ArchivedAt sql.NullTime `db:"archived_at"`
	}
	if err = tx.Select(ctx, &memberships, `SELECT start_at,end_at,archived_at FROM class_members
		WHERE class_id=? AND user_id=? ORDER BY start_at,id FOR SHARE`, row.ClassID, access.CandidateUserID.String()); err != nil {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, fmt.Errorf("lock Exam Submission membership history: %w", err)
	}
	integrityTail, err := lockExamSubmissionIntegrityTail(ctx, tx, access)
	if err != nil {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, err
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, fmt.Errorf("read Exam Submission decision time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	currentMemberships := 0
	for _, membership := range memberships {
		if !membership.ArchivedAt.Valid && !databaseNow.Before(membership.StartAt) &&
			(!membership.EndAt.Valid || databaseNow.Before(membership.EndAt.Time)) {
			currentMemberships++
		}
	}
	if currentMemberships != 1 || row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid ||
		!databaseNow.Before(row.SessionIdleExpiresAt) || !databaseNow.Before(row.SessionExpiresAt) ||
		row.UserArchivedAt.Valid || row.UserDisabledAt.Valid || row.ClassArchivedAt.Valid || row.LevelArchivedAt.Valid ||
		row.ProgrammeArchivedAt.Valid || row.UnitArchivedAt.Valid || row.PeriodArchivedAt.Valid {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrNotFound("exam_submission_access", access.AttemptID.String())
	}
	if row.ParticipationGeneration != access.Generation {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	}
	if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(access.ContinuityCredentialHash)) != 1 {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if allowCommittedCausal && row.AttemptState == string(model.ExamAttemptSubmitted) {
		var causal struct {
			ExamRevisionID         string         `db:"exam_revision_id"`
			WorkspaceCursor        int64          `db:"workspace_cursor"`
			FinalFocusLossSequence int64          `db:"final_focus_loss_sequence"`
			ParticipationID        string         `db:"participation_id"`
			Generation             int64          `db:"generation"`
			ConnectionID           string         `db:"connection_id"`
			Sealed                 bool           `db:"sealed"`
			BrowserActivityState   string         `db:"browser_activity_state"`
			BrowserSourceSessionID sql.NullString `db:"browser_activity_source_session_id"`
			BrowserFinalSequence   sql.NullInt64  `db:"browser_activity_final_sequence"`
			BrowserGapReason       sql.NullString `db:"browser_activity_gap_reason"`
		}
		if causalErr := tx.Get(ctx, &causal, `SELECT exam_revision_id,workspace_cursor,final_focus_loss_sequence,participation_id,
			generation,connection_id,sealed,browser_activity_state,browser_activity_source_session_id::text,
			browser_activity_final_sequence,browser_activity_gap_reason FROM exam_submissions WHERE exam_attempt_id=? FOR UPDATE`, access.AttemptID.String()); causalErr != nil {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, translateError("exam_submission", access.AttemptID.String(), causalErr)
		}
		if causal.ParticipationID != access.ParticipationID.String() || causal.Generation != access.Generation {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
		}
		if causal.ConnectionID != access.ConnectionID.String() || row.ConnectionState != string(model.AttemptConnectionClosed) ||
			row.ConnectionCloseReason.String != string(model.AttemptConnectionCloseSubmitted) {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_connection", "attempt_connection_closed", nil)
		}
		if causal.WorkspaceCursor != access.ExpectedWorkspaceCursor {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_workspace", "attempt_workspace_cursor", nil)
		}
		if causal.FinalFocusLossSequence != access.FinalFocusLossSequence {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("focus_loss_signal", "focus_loss_sequence", nil)
		}
		if causal.ExamRevisionID != access.ExpectedCurrentRevisionID.String() || !samePersistedBrowserActivitySubmission(causal.BrowserActivityState,
			causal.BrowserSourceSessionID, causal.BrowserFinalSequence, causal.BrowserGapReason, access.BrowserActivity) {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_submission", "exam_submission_causal_selector", nil)
		}
		if !causal.Sealed || row.ParticipationState != string(model.AttemptParticipationEnded) ||
			row.ParticipationEndReason.String != string(model.AttemptParticipationEndSubmitted) {
			return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
		}
		return row, integrityTail, databaseNow, nil
	}
	if row.ConnectionState != string(model.AttemptConnectionOpen) {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_connection", "attempt_connection_closed", nil)
	}
	if row.ParticipationState != string(model.AttemptParticipationActive) || !databaseNow.Before(row.LeaseExpiresAt) {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if row.SittingState != string(model.ExamSittingOpen) || !databaseNow.Before(row.ScheduledEndAt) {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if row.AttemptState != string(model.ExamAttemptActive) {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	if row.CurrentRevisionID != access.ExpectedCurrentRevisionID.String() {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_sitting", "exam_sitting_revision_selection", nil)
	}
	var pendingAcknowledgements int
	if err = tx.Get(ctx, &pendingAcknowledgements, `SELECT COUNT(*) FROM exam_sitting_live_corrections live
		JOIN exam_revisions correction ON correction.id=live.correction_revision_id AND correction.exam_id=live.exam_id
		JOIN exam_revisions admission ON admission.id=? AND admission.exam_id=live.exam_id
		JOIN exam_revisions current_revision ON current_revision.id=? AND current_revision.exam_id=live.exam_id
		WHERE live.exam_sitting_id=? AND correction.number>admission.number AND correction.number<=current_revision.number
		AND correction.candidate_correction_acknowledgement_required=true
		AND NOT EXISTS (SELECT 1 FROM exam_attempt_correction_acknowledgements acknowledgement
			WHERE acknowledgement.exam_attempt_id=? AND acknowledgement.correction_revision_id=correction.id)`,
		row.AdmissionRevisionID, row.CurrentRevisionID, row.SittingID, access.AttemptID.String()); err != nil {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, err
	}
	if pendingAcknowledgements != 0 {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("exam_submission", "exam_correction_acknowledgement_required", nil)
	}
	if row.WorkspaceCursor != access.ExpectedWorkspaceCursor {
		return zero, examSubmissionIntegrityTail{}, time.Time{}, store.NewErrConflict("attempt_workspace", "attempt_workspace_cursor", nil)
	}
	return row, integrityTail, databaseNow, nil
}

func examSubmissionSealTarget(row examSubmissionSealAccessRow) (*store.ExamSubmissionSealTarget, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_sitting_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "class_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "candidate_user_id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "workspace_id", err)
	}
	currentRevisionID, err := model.ParseExamRevisionID(row.CurrentRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_revision_id", err)
	}
	return &store.ExamSubmissionSealTarget{ExamID: examID, SittingID: sittingID, ClassID: classID,
		CandidateUserID: candidateID, WorkspaceID: workspaceID, CurrentRevisionID: currentRevisionID}, nil
}

type examSubmissionSealOutcomeV1 struct {
	Receipt          store.ExamSubmissionReceipt `json:"r"`
	ExamID           string                      `json:"e"`
	SittingID        string                      `json:"s"`
	ClassID          string                      `json:"c"`
	CandidateID      string                      `json:"u"`
	ParticipationID  string                      `json:"p"`
	Generation       int64                       `json:"g"`
	ConnectionID     string                      `json:"n"`
	ConnectionClosed bool                        `json:"x,omitempty"`
}

func (s *SQLExamSubmissionStore) Seal(ctx context.Context, input *store.ExamSubmissionSeal,
	command *store.CommandIdempotency,
) (*store.ExamSubmissionSealResult, error) {
	if input == nil || command == nil || command.Operation != store.ExamSubmissionSealOperation || command.OutcomeVersion != 1 ||
		command.UserID != input.Access.CandidateUserID || !input.SubmissionID.IsValid() ||
		!validExamSubmissionSealAccess(input.Access) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_submission", "seal", nil)
	}
	prepared := *input
	result, err := runIdempotentMutation(ctx, s.SQLStore, "seal voluntary Exam Submission", idempotentMutation[examSubmissionSealOutcomeV1]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examSubmissionSealOutcomeV1, error) {
			return sealExamSubmission(ctx, tx, &prepared)
		},
		encode: func(outcome examSubmissionSealOutcomeV1) ([]byte, error) { return encodeCommandOutcome(outcome) },
		decode: func(version int, data []byte) (examSubmissionSealOutcomeV1, error) {
			var outcome examSubmissionSealOutcomeV1
			if version != 1 {
				return outcome, fmt.Errorf("unsupported Exam Submission seal outcome version %d", version)
			}
			if err := decodeCommandOutcome(data, &outcome); err != nil {
				return outcome, err
			}
			if _, err := examSubmissionSealResult(outcome); err != nil {
				return outcome, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome examSubmissionSealOutcomeV1, originalAuditID string) error {
			row, _, _, lockErr := lockExamSubmissionSealAccess(ctx, tx, prepared.Access, true)
			if lockErr != nil {
				return lockErr
			}
			target, targetErr := examSubmissionSealTarget(row)
			if targetErr != nil {
				return targetErr
			}
			if target.ExamID.String() != outcome.ExamID || target.SittingID.String() != outcome.SittingID ||
				target.ClassID.String() != outcome.ClassID || target.CandidateUserID.String() != outcome.CandidateID ||
				outcome.Receipt.AttemptID != prepared.Access.AttemptID || outcome.ParticipationID != prepared.Access.ParticipationID.String() ||
				outcome.Generation != prepared.Access.Generation || outcome.ConnectionID != prepared.Access.ConnectionID.String() {
				return store.NewErrNotFound("exam_submission_access", prepared.Access.AttemptID.String())
			}
			return completeExamSubmissionAudit(ctx, tx, outcome, prepared.AuditEventID, prepared.AuditAt, true, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	response, err := examSubmissionSealResult(result.Value)
	if err != nil {
		return nil, err
	}
	response.Replayed = result.Replayed
	return response, nil
}

type examSubmissionManifestPersistenceRow struct {
	EntryID           string         `db:"entry_id"`
	Kind              string         `db:"kind"`
	Path              string         `db:"path"`
	WorkspaceObjectID sql.NullString `db:"workspace_object_id"`
	ContentVersion    sql.NullString `db:"content_version"`
	MediaType         sql.NullString `db:"media_type"`
	SizeBytes         sql.NullInt64  `db:"size_bytes"`
	SHA256            sql.NullString `db:"sha256"`
	StorageOrigin     sql.NullString `db:"storage_origin"`
	StarterObjectID   sql.NullString `db:"starter_object_id"`
	AttemptObjectID   sql.NullString `db:"attempt_object_id"`
}

func sealExamSubmission(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSubmissionSeal) (examSubmissionSealOutcomeV1, error) {
	var zero examSubmissionSealOutcomeV1
	recipient, err := lockMailRecipientUser(ctx, tx, input.Access.CandidateUserID)
	if err != nil {
		return zero, err
	}
	row, integrityTail, databaseNow, err := lockExamSubmissionSealAccess(ctx, tx, input.Access, false)
	if err != nil {
		return zero, err
	}
	if input.Access.FinalFocusLossSequence < integrityTail.AcceptedSequence {
		return zero, store.NewErrConflict("focus_loss_signal", "focus_loss_sequence", nil)
	}
	sealAt := model.TimeFromMillis(input.AuditAt)
	if sealAt.After(databaseNow) {
		return zero, store.NewErrConflict("exam_submission", "seal_time", nil)
	}
	if input.ExpectedRecipientRevision < 1 || recipient.Revision != input.ExpectedRecipientRevision {
		return zero, store.NewErrConflict("exam_submission", "receipt_recipient_changed", nil)
	}
	payloadKeyID, err := validateExamSubmissionReceiptMail(input.Notice, recipient, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionReceived)
	if err != nil {
		return zero, err
	}
	if payloadKeyID != "" {
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return zero, err
		}
	}
	tail := input.Access.FinalFocusLossSequence - integrityTail.AcceptedSequence
	if tail > math.MaxInt64-integrityTail.TotalUnresolved {
		return zero, store.NewErrConflict("focus_loss_signal", "focus_loss_sequence", nil)
	}
	unresolved := integrityTail.TotalUnresolved + tail
	focusUnresolved := unresolved
	browserUnresolved, browserActivity, err := settleVoluntaryBrowserActivity(ctx, tx, input.Access, databaseNow)
	if err != nil {
		return zero, err
	}
	if browserUnresolved > math.MaxInt64-unresolved {
		return zero, store.NewErrConflict("browser_activity", "browser_activity_accounting", nil)
	}
	unresolved += browserUnresolved
	rows, entries, err := loadAuthoritativeExamSubmissionManifest(ctx, tx, row.WorkspaceID)
	if err != nil {
		return zero, err
	}
	manifest, err := model.NewExamSubmissionManifest(row.WorkspaceCursor, entries)
	if err != nil {
		return zero, invalidPersistedState("exam_submission_manifest", "value", err)
	}
	attempt, participation, connection, err := row.activeDomain(input.Access)
	if err != nil {
		return zero, err
	}
	if err = model.SubmitExamAttempt(attempt, participation, connection, sealAt); err != nil {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return zero, invalidPersistedState("exam_submission", "workspace_id", err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: input.SubmissionID,
		AttemptID: input.Access.AttemptID, ExamRevisionID: input.Access.ExpectedCurrentRevisionID, WorkspaceID: workspaceID, Manifest: manifest,
		FinalFocusLossSequence: input.Access.FinalFocusLossSequence, UnresolvedIntegrityCount: unresolved,
		BrowserActivity: browserActivity,
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: sealAt})
	if err != nil {
		return zero, store.NewErrInvalidInput("exam_submission", "value", nil).Wrap(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_submissions
		(id,exam_attempt_id,exam_revision_id,workspace_id,participation_id,generation,connection_id,manifest_schema_version,
		workspace_cursor,manifest_digest,manifest_entry_count,manifest_total_file_bytes,final_focus_loss_sequence,
		browser_activity_state,browser_activity_source_session_id,browser_activity_final_sequence,browser_activity_gap_reason,
		integrity_state,unresolved_integrity_count,provenance,submitted_at,sealed)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,false)`, submission.ID.String(), submission.AttemptID.String(),
		submission.ExamRevisionID.String(), submission.WorkspaceID.String(), input.Access.ParticipationID.String(), input.Access.Generation,
		input.Access.ConnectionID.String(), submission.ManifestSchemaVersion, submission.WorkspaceCursor,
		submission.ManifestDigest, submission.ManifestEntryCount, submission.ManifestTotalFileBytes,
		submission.FinalFocusLossSequence, string(submission.BrowserActivity.State), nullableString(string(submission.BrowserActivity.SourceSessionID)),
		nullableInt64Pointer(submission.BrowserActivity.FinalSequence), nullableString(string(submission.BrowserActivity.GapReason)),
		string(submission.IntegrityState), submission.UnresolvedIntegrityCount, string(submission.Provenance),
		submission.SubmittedAt); err != nil {
		return zero, fmt.Errorf("insert Exam Submission: %w", translateError("exam_submission", submission.ID.String(), err))
	}
	if err = insertTerminalIntegrityDiscrepancies(ctx, tx, submission, integrityDiscrepancyTarget{
		AttemptID: input.Access.AttemptID, ParticipationID: input.Access.ParticipationID,
		Generation: input.Access.Generation, ConnectionID: input.Access.ConnectionID,
	}, terminalIntegrityDiscrepancies{FocusUnresolved: focusUnresolved,
		FocusReason:       model.IntegrityDiscrepancyFocusLossSequenceGap,
		BrowserUnresolved: browserUnresolved, BrowserActivity: browserActivity}); err != nil {
		return zero, err
	}
	for index, entry := range manifest.Entries {
		persisted := rows[index]
		if persisted.EntryID != entry.EntryID.String() {
			return zero, invalidPersistedState("exam_submission_manifest", "order", errors.New("canonical Entry order changed"))
		}
		if _, err = tx.Exec(ctx, `INSERT INTO exam_submission_manifest_entries
			(submission_id,workspace_id,entry_id,kind,path,content_version,media_type,size_bytes,sha256,storage_origin,
			starter_object_id,attempt_object_id,workspace_object_id)
			VALUES (?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?)`,
			submission.ID.String(), submission.WorkspaceID.String(), entry.EntryID.String(), string(entry.Kind), entry.Path,
			entry.ContentVersion.String(), entry.MediaType, nullableSubmissionSize(entry), entry.SHA256, string(entry.StorageOrigin),
			entry.StarterObjectID.String(), entry.AttemptObjectID.String(), persisted.WorkspaceObjectID); err != nil {
			return zero, fmt.Errorf("insert Exam Submission manifest Entry: %w", translateError("exam_submission_manifest_entry", entry.EntryID.String(), err))
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_submissions SET sealed=true WHERE id=? AND sealed=false`, submission.ID.String()); err != nil {
		return zero, fmt.Errorf("seal Exam Submission header: %w", err)
	}
	if integrityTail.CurrentExists {
		if tail > math.MaxInt64-integrityTail.CurrentUnresolved {
			return zero, store.NewErrConflict("focus_loss_signal", "focus_loss_sequence", nil)
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_focus_loss_evaluations SET unresolved_missing_count=?
			WHERE exam_attempt_id=? AND generation=?`, integrityTail.CurrentUnresolved+tail,
			input.Access.AttemptID.String(), input.Access.Generation); err != nil {
			return zero, fmt.Errorf("settle Exam Submission integrity gaps: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=?`,
		input.Access.AttemptID.String(), input.Access.Generation); err != nil {
		return zero, fmt.Errorf("settle Exam Submission pending Focus Loss window: %w", err)
	}
	if err = persistSubmittedExamAttempt(ctx, tx, attempt, participation, connection); err != nil {
		return zero, err
	}
	target, err := examSubmissionSealTarget(row)
	if err != nil {
		return zero, err
	}
	outcome := examSubmissionSealOutcomeV1{Receipt: store.ExamSubmissionReceipt{SubmissionID: submission.ID,
		AttemptID: submission.AttemptID, ExamRevisionID: submission.ExamRevisionID, State: attempt.State, WorkspaceCursor: submission.WorkspaceCursor,
		ManifestDigest: submission.ManifestDigest, SubmittedAt: submission.SubmittedAt}, ExamID: target.ExamID.String(),
		SittingID: target.SittingID.String(), ClassID: target.ClassID.String(), CandidateID: target.CandidateUserID.String(),
		ParticipationID: participation.ID.String(), Generation: participation.Generation, ConnectionID: connection.ID.String()}
	if err = completeExamSubmissionAudit(ctx, tx, outcome, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return zero, err
	}
	if err = insertExamSubmissionReceiptMail(ctx, tx, input.Notice, payloadKeyID); err != nil {
		return zero, err
	}
	return outcome, nil
}

func nullableSubmissionSize(entry model.ExamSubmissionManifestEntry) any {
	if entry.Kind == model.StarterWorkspaceEntryDirectory {
		return nil
	}
	return entry.SizeBytes
}

func nullableInt64Pointer(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

type browserActivitySubmissionSourceRow struct {
	ID                       string       `db:"id"`
	ParticipationID          string       `db:"participation_id"`
	Generation               int64        `db:"generation"`
	State                    string       `db:"state"`
	HighestContiguous        int64        `db:"highest_contiguous"`
	HighestSeen              int64        `db:"highest_seen"`
	ReceivedBeyondContiguous int64        `db:"received_beyond_contiguous"`
	EndedAt                  sql.NullTime `db:"ended_at"`
}

func settleVoluntaryBrowserActivity(ctx context.Context, tx *sqlxTxWrapper, access store.ExamSubmissionSealAccess,
	databaseNow time.Time,
) (int64, model.BrowserActivitySubmission, error) {
	var sources []browserActivitySubmissionSourceRow
	if err := tx.Select(ctx, &sources, `SELECT source.id::text,source.participation_id,source.generation,source.state,
		source.highest_contiguous,source.highest_seen,source.ended_at,
		(SELECT count(*) FROM browser_activity_events event WHERE event.source_session_id=source.id
			AND event.sequence>source.highest_contiguous AND event.sequence<=source.highest_seen) AS received_beyond_contiguous
		FROM browser_activity_sources source WHERE source.exam_attempt_id=? ORDER BY source.started_at,source.id FOR UPDATE OF source`, access.AttemptID.String()); err != nil {
		return 0, model.BrowserActivitySubmission{}, err
	}
	declaration := access.BrowserActivity.Clone()
	if declaration.State == model.BrowserActivitySubmissionNotApplicable {
		if len(sources) != 0 {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_not_applicable", nil)
		}
		return 0, declaration, nil
	}
	declaredIndex := -1
	for index, source := range sources {
		if source.ID == string(declaration.SourceSessionID) {
			declaredIndex = index
			break
		}
	}
	if declaredIndex < 0 {
		return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source", nil)
	}
	declared := sources[declaredIndex]
	if declared.ParticipationID != access.ParticipationID.String() || declared.Generation != access.Generation || declared.State != "current" {
		return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source_fence", nil)
	}
	switch declaration.State {
	case model.BrowserActivitySubmissionComplete:
		finalSequence := *declaration.FinalSequence
		if declared.HighestContiguous != finalSequence || declared.HighestSeen != finalSequence {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_incomplete", nil)
		}
		var finalKind string
		if err := tx.Get(ctx, &finalKind, `SELECT kind FROM browser_activity_events WHERE source_session_id=?::uuid AND sequence=?`,
			declared.ID, finalSequence); err != nil {
			return 0, model.BrowserActivitySubmission{}, translateError("browser_activity_event", declared.ID, err)
		}
		if finalKind != string(model.BrowserActivityClosed) {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_not_closed", nil)
		}
		result, updateErr := tx.Exec(ctx, `UPDATE browser_activity_sources SET state='closed',ended_at=? WHERE id=?::uuid AND state='current'`,
			databaseNow, declared.ID)
		if updateErr != nil {
			return 0, model.BrowserActivitySubmission{}, updateErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source_fence", rowsErr)
		}
		sources[declaredIndex].State = "closed"
	case model.BrowserActivitySubmissionGapped:
		if declaration.FinalSequence != nil && *declaration.FinalSequence < declared.HighestSeen {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_final_sequence", nil)
		}
		result, updateErr := tx.Exec(ctx, `UPDATE browser_activity_sources SET state='gapped',ended_at=? WHERE id=?::uuid AND state='current'`,
			databaseNow, declared.ID)
		if updateErr != nil {
			return 0, model.BrowserActivitySubmission{}, updateErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source_fence", rowsErr)
		}
		sources[declaredIndex].State = "gapped"
	}
	for index, source := range sources {
		if index == declaredIndex || source.State != "current" {
			continue
		}
		result, updateErr := tx.Exec(ctx, `UPDATE browser_activity_sources SET state='gapped',ended_at=? WHERE id=?::uuid AND state='current'`,
			databaseNow, source.ID)
		if updateErr != nil {
			return 0, model.BrowserActivitySubmission{}, updateErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return 0, model.BrowserActivitySubmission{}, store.NewErrConflict("browser_activity", "browser_activity_source_fence", rowsErr)
		}
		sources[index].State = "gapped"
	}
	unresolved, err := browserActivitySourceUnresolved(sources)
	if err != nil {
		return 0, model.BrowserActivitySubmission{}, err
	}
	if declaration.State == model.BrowserActivitySubmissionGapped && declaration.FinalSequence != nil &&
		*declaration.FinalSequence > declared.HighestSeen {
		additional := *declaration.FinalSequence - declared.HighestSeen
		if additional > math.MaxInt64-unresolved {
			return 0, model.BrowserActivitySubmission{}, invalidPersistedState("browser_activity", "unresolved_count",
				errors.New("Browser Activity unresolved count overflows"))
		}
		unresolved += additional
	}
	return unresolved, declaration, nil
}

func browserActivitySourceUnresolved(sources []browserActivitySubmissionSourceRow) (int64, error) {
	var unresolved int64
	for _, source := range sources {
		if source.State == "gapped" {
			if unresolved == math.MaxInt64 {
				return 0, invalidPersistedState("browser_activity", "unresolved_count",
					errors.New("Browser Activity unresolved count overflows"))
			}
			unresolved++
		}
		span := source.HighestSeen - source.HighestContiguous
		missing := span - source.ReceivedBeyondContiguous
		if span < 0 || source.ReceivedBeyondContiguous < 0 || source.ReceivedBeyondContiguous > span ||
			missing > math.MaxInt64-unresolved {
			return 0, invalidPersistedState("browser_activity", "unresolved_count",
				errors.New("Browser Activity unresolved count overflows"))
		}
		unresolved += missing
	}
	return unresolved, nil
}

func samePersistedBrowserActivitySubmission(state string, source sql.NullString, final sql.NullInt64, reason sql.NullString,
	expected model.BrowserActivitySubmission,
) bool {
	if state != string(expected.State) || source.String != string(expected.SourceSessionID) || source.Valid != expected.SourceSessionID.IsValid() ||
		reason.String != string(expected.GapReason) || reason.Valid != expected.GapReason.IsValid() || final.Valid != (expected.FinalSequence != nil) {
		return false
	}
	return expected.FinalSequence == nil || final.Int64 == *expected.FinalSequence
}

type examSubmissionIntegrityTail struct {
	AcceptedSequence  int64
	CurrentUnresolved int64
	TotalUnresolved   int64
	CurrentExists     bool
}

func lockExamSubmissionIntegrityTail(ctx context.Context, tx *sqlxTxWrapper, access store.ExamSubmissionSealAccess) (examSubmissionIntegrityTail, error) {
	var rows []struct {
		Generation       int64 `db:"generation"`
		AcceptedSequence int64 `db:"accepted_sequence"`
		Unresolved       int64 `db:"unresolved_missing_count"`
	}
	if err := tx.Select(ctx, &rows, `SELECT generation,accepted_sequence,unresolved_missing_count
		FROM exam_attempt_focus_loss_evaluations WHERE exam_attempt_id=? ORDER BY generation FOR UPDATE`,
		access.AttemptID.String()); err != nil {
		return examSubmissionIntegrityTail{}, fmt.Errorf("lock Exam Submission Focus Loss history: %w", err)
	}
	var result examSubmissionIntegrityTail
	for _, row := range rows {
		if row.Unresolved > math.MaxInt64-result.TotalUnresolved {
			return examSubmissionIntegrityTail{}, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
		}
		result.TotalUnresolved += row.Unresolved
		if row.Generation == access.Generation {
			result.CurrentExists = true
			result.AcceptedSequence = row.AcceptedSequence
			result.CurrentUnresolved = row.Unresolved
		}
	}
	return result, nil
}

func loadAuthoritativeExamSubmissionManifest(ctx context.Context, tx *sqlxTxWrapper, workspaceID string) ([]examSubmissionManifestPersistenceRow, []model.ExamSubmissionManifestEntry, error) {
	var rows []examSubmissionManifestPersistenceRow
	if err := tx.Select(ctx, &rows, `SELECT e.id AS entry_id,e.kind,e.path,e.current_object_id AS workspace_object_id,
		o.content_version,o.media_type,o.size_bytes,o.sha256,o.storage_origin,o.starter_object_id,
		CASE WHEN o.storage_origin='attempt' THEN o.id END AS attempt_object_id
		FROM exam_attempt_workspace_entries e LEFT JOIN exam_attempt_workspace_objects o
		ON o.workspace_id=e.workspace_id AND o.id=e.current_object_id
		WHERE e.workspace_id=? ORDER BY e.id FOR SHARE OF e`, workspaceID); err != nil {
		return nil, nil, fmt.Errorf("load authoritative Exam Submission manifest: %w", err)
	}
	entries := make([]model.ExamSubmissionManifestEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := row.model()
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	return rows, entries, nil
}

func (row examSubmissionManifestPersistenceRow) model() (model.ExamSubmissionManifestEntry, error) {
	id, err := model.ParseAttemptWorkspaceEntryID(row.EntryID)
	if err != nil {
		return model.ExamSubmissionManifestEntry{}, invalidPersistedState("exam_submission_manifest_entry", "entry_id", err)
	}
	entry := model.ExamSubmissionManifestEntry{EntryID: id, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path,
		MediaType: row.MediaType.String, SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String,
		StorageOrigin: model.AttemptWorkspaceObjectStorage(row.StorageOrigin.String)}
	if row.ContentVersion.Valid {
		entry.ContentVersion, err = model.ParseWorkspaceContentVersion(row.ContentVersion.String)
		if err != nil {
			return entry, invalidPersistedState("exam_submission_manifest_entry", "content_version", err)
		}
	}
	if row.StarterObjectID.Valid {
		entry.StarterObjectID, err = model.ParseStarterWorkspaceObjectID(row.StarterObjectID.String)
		if err != nil {
			return entry, invalidPersistedState("exam_submission_manifest_entry", "starter_object_id", err)
		}
	}
	if row.AttemptObjectID.Valid {
		entry.AttemptObjectID, err = model.ParseAttemptWorkspaceObjectID(row.AttemptObjectID.String)
		if err != nil {
			return entry, invalidPersistedState("exam_submission_manifest_entry", "attempt_object_id", err)
		}
	}
	if err = entry.Validate(); err != nil {
		return entry, invalidPersistedState("exam_submission_manifest_entry", "value", err)
	}
	return entry, nil
}

func (row examSubmissionSealAccessRow) activeDomain(access store.ExamSubmissionSealAccess) (*model.ExamAttempt, *model.AttemptParticipation, *model.AttemptConnection, error) {
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
	attempt := &model.ExamAttempt{ID: access.AttemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.AttemptSubmittedAt), Revision: row.AttemptRevision}
	participation := &model.AttemptParticipation{ID: access.ParticipationID, AttemptID: access.AttemptID, SessionID: access.SessionID,
		State: model.AttemptParticipationState(row.ParticipationState), Generation: row.ParticipationGeneration,
		RenewalSequence: row.RenewalSequence, ContinuityCredentialHash: row.CredentialHash,
		StartedAt: model.TimeUTC(row.ParticipationStartedAt), UpdatedAt: model.TimeUTC(row.ParticipationUpdatedAt),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.ParticipationEndedAt),
		EndReason: model.AttemptParticipationEndReason(row.ParticipationEndReason.String)}
	connection := &model.AttemptConnection{ID: access.ConnectionID, AttemptID: access.AttemptID, ParticipationID: access.ParticipationID,
		SessionID: access.SessionID, State: model.AttemptConnectionState(row.ConnectionState), OpenedAt: model.TimeUTC(row.ConnectionOpenedAt),
		ClosedAt: OptionalTimeFromNullTime(row.ConnectionClosedAt), CloseReason: model.AttemptConnectionCloseReason(row.ConnectionCloseReason.String)}
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

func persistSubmittedExamAttempt(ctx context.Context, tx *sqlxTxWrapper, attempt *model.ExamAttempt,
	participation *model.AttemptParticipation, connection *model.AttemptConnection,
) error {
	result, err := tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,submitted_at=?,revision=?
		WHERE id=? AND state='active' AND revision=?`, string(attempt.State), attempt.UpdatedAt, attempt.SubmittedAt.Time,
		attempt.Revision, attempt.ID.String(), attempt.Revision-1)
	if err != nil {
		return fmt.Errorf("submit Exam Attempt: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return store.NewErrConflict("exam_attempt", "exam_attempt_state", rowsErr)
	}
	result, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=?
		WHERE id=? AND exam_attempt_id=? AND generation=? AND state='active'`, string(participation.State), participation.UpdatedAt,
		participation.EndedAt.Time, string(participation.EndReason), participation.ID.String(), participation.AttemptID.String(), participation.Generation)
	if err != nil {
		return fmt.Errorf("end submitted Attempt Participation: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return store.NewErrConflict("attempt_participation", "attempt_participation_expired", rowsErr)
	}
	result, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state=?,closed_at=?,close_reason=?
		WHERE id=? AND exam_attempt_id=? AND participation_id=? AND state='open'`, string(connection.State),
		connection.ClosedAt.Time, string(connection.CloseReason), connection.ID.String(), connection.AttemptID.String(),
		connection.ParticipationID.String())
	if err != nil {
		return fmt.Errorf("close submitted Attempt Connection: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return store.NewErrConflict("attempt_connection", "attempt_connection_closed", rowsErr)
	}
	return nil
}

func completeExamSubmissionAudit(ctx context.Context, tx *sqlxTxWrapper, outcome examSubmissionSealOutcomeV1,
	auditID string, auditAt int64, replayed bool, originalAuditID string,
) error {
	data := map[string]any{"exam_submission_id": outcome.Receipt.SubmissionID.String(), "exam_attempt_id": outcome.Receipt.AttemptID.String(),
		"exam_sitting_id": outcome.SittingID, "candidate_user_id": outcome.CandidateID,
		"exam_revision_id": outcome.Receipt.ExamRevisionID.String(),
		"workspace_cursor": outcome.Receipt.WorkspaceCursor, "manifest_digest": outcome.Receipt.ManifestDigest,
		"state": string(outcome.Receipt.State), "submitted_at": outcome.Receipt.SubmittedAt}
	if replayed {
		if originalAuditID == "" {
			data["replayed"] = true
		} else {
			data["idempotency_replayed"] = true
			data["original_audit_event_id"] = originalAuditID
		}
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Submission audit: %w", err)
	}
	return nil
}

func examSubmissionSealResult(outcome examSubmissionSealOutcomeV1) (*store.ExamSubmissionSealResult, error) {
	if !outcome.Receipt.SubmissionID.IsValid() || !outcome.Receipt.AttemptID.IsValid() || !outcome.Receipt.ExamRevisionID.IsValid() ||
		outcome.Receipt.State != model.ExamAttemptSubmitted || outcome.Receipt.WorkspaceCursor < 0 ||
		len(outcome.Receipt.ManifestDigest) != 64 || outcome.Receipt.SubmittedAt.IsZero() || outcome.Generation < 1 {
		return nil, errors.New("invalid Exam Submission seal outcome")
	}
	examID, err := model.ParseExamID(outcome.ExamID)
	if err != nil {
		return nil, err
	}
	sittingID, err := model.ParseExamSittingID(outcome.SittingID)
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(outcome.ClassID)
	if err != nil {
		return nil, err
	}
	candidateID, err := model.ParseUserID(outcome.CandidateID)
	if err != nil {
		return nil, err
	}
	participationID, err := model.ParseAttemptParticipationID(outcome.ParticipationID)
	if err != nil {
		return nil, err
	}
	connectionID, err := model.ParseAttemptConnectionID(outcome.ConnectionID)
	if err != nil {
		return nil, err
	}
	return &store.ExamSubmissionSealResult{Receipt: outcome.Receipt, ExamID: examID, SittingID: sittingID, ClassID: classID,
		CandidateUserID: candidateID, ParticipationID: participationID, Generation: outcome.Generation, ConnectionID: connectionID}, nil
}

type examSubmissionHeaderRow struct {
	ID                       string         `db:"id"`
	AttemptID                string         `db:"exam_attempt_id"`
	ExamRevisionID           string         `db:"exam_revision_id"`
	WorkspaceID              string         `db:"workspace_id"`
	ManifestSchemaVersion    int            `db:"manifest_schema_version"`
	WorkspaceCursor          int64          `db:"workspace_cursor"`
	ManifestDigest           string         `db:"manifest_digest"`
	ManifestEntryCount       int            `db:"manifest_entry_count"`
	ManifestTotalFileBytes   int64          `db:"manifest_total_file_bytes"`
	FinalFocusLossSequence   int64          `db:"final_focus_loss_sequence"`
	BrowserActivityState     string         `db:"browser_activity_state"`
	BrowserSourceSessionID   sql.NullString `db:"browser_activity_source_session_id"`
	BrowserFinalSequence     sql.NullInt64  `db:"browser_activity_final_sequence"`
	BrowserGapReason         sql.NullString `db:"browser_activity_gap_reason"`
	IntegrityState           string         `db:"integrity_state"`
	UnresolvedIntegrityCount int64          `db:"unresolved_integrity_count"`
	Provenance               string         `db:"provenance"`
	SubmittedAt              time.Time      `db:"submitted_at"`
}

const examSubmissionHeaderSelect = `SELECT id,exam_attempt_id,exam_revision_id,workspace_id,manifest_schema_version,workspace_cursor,
	manifest_digest,manifest_entry_count,manifest_total_file_bytes,final_focus_loss_sequence,browser_activity_state,
	browser_activity_source_session_id::text,browser_activity_final_sequence,browser_activity_gap_reason,integrity_state,
	unresolved_integrity_count,provenance,submitted_at FROM exam_submissions`

func (row examSubmissionHeaderRow) model() (*model.ExamSubmission, error) {
	id, err := model.ParseSubmissionID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_attempt_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.ExamRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_revision_id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_submission", "workspace_id", err)
	}
	var finalSequence *int64
	if row.BrowserFinalSequence.Valid {
		value := row.BrowserFinalSequence.Int64
		finalSequence = &value
	}
	submission := &model.ExamSubmission{ID: id, AttemptID: attemptID, ExamRevisionID: revisionID, WorkspaceID: workspaceID,
		ManifestSchemaVersion: row.ManifestSchemaVersion, WorkspaceCursor: row.WorkspaceCursor,
		ManifestDigest: row.ManifestDigest, ManifestEntryCount: row.ManifestEntryCount,
		ManifestTotalFileBytes: row.ManifestTotalFileBytes, FinalFocusLossSequence: row.FinalFocusLossSequence,
		BrowserActivity: model.BrowserActivitySubmission{State: model.BrowserActivitySubmissionState(row.BrowserActivityState),
			SourceSessionID: model.BrowserSourceSessionID(row.BrowserSourceSessionID.String), FinalSequence: finalSequence,
			GapReason: model.BrowserActivitySubmissionGapReason(row.BrowserGapReason.String)},
		IntegrityState: model.SubmissionIntegrityState(row.IntegrityState), UnresolvedIntegrityCount: row.UnresolvedIntegrityCount,
		Provenance: model.ExamSubmissionProvenance(row.Provenance), SubmittedAt: model.TimeUTC(row.SubmittedAt)}
	if err = submission.Validate(); err != nil {
		return nil, invalidPersistedState("exam_submission", "value", err)
	}
	return submission, nil
}

func (s *SQLExamSubmissionStore) Resolve(ctx context.Context, submissionID model.SubmissionID) (*store.ExamSubmissionAuthorization, error) {
	if !submissionID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_submission", "id", nil)
	}
	var row struct {
		SubmissionID    string `db:"submission_id"`
		ExamID          string `db:"exam_id"`
		SittingID       string `db:"exam_sitting_id"`
		AttemptID       string `db:"exam_attempt_id"`
		CandidateUserID string `db:"candidate_user_id"`
		AcademicUnitID  string `db:"academic_unit_id"`
	}
	if err := s.GetMaster().Get(ctx, &row, `SELECT sub.id AS submission_id,a.exam_id,a.exam_sitting_id,
		a.id AS exam_attempt_id,a.candidate_user_id,e.academic_unit_id FROM exam_submissions sub
		JOIN exam_attempts a ON a.id=sub.exam_attempt_id JOIN exams e ON e.id=a.exam_id
		WHERE sub.id=? AND sub.sealed=true`, submissionID.String()); err != nil {
		return nil, translateError("exam_submission", submissionID.String(), err)
	}
	result := &store.ExamSubmissionAuthorization{SubmissionID: submissionID}
	var err error
	if result.ExamID, err = model.ParseExamID(row.ExamID); err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_id", err)
	}
	if result.SittingID, err = model.ParseExamSittingID(row.SittingID); err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_sitting_id", err)
	}
	if result.AttemptID, err = model.ParseExamAttemptID(row.AttemptID); err != nil {
		return nil, invalidPersistedState("exam_submission", "exam_attempt_id", err)
	}
	if result.CandidateUserID, err = model.ParseUserID(row.CandidateUserID); err != nil {
		return nil, invalidPersistedState("exam_submission", "candidate_user_id", err)
	}
	if result.AcademicUnitID, err = model.ParseAcademicUnitID(row.AcademicUnitID); err != nil {
		return nil, invalidPersistedState("exam_submission", "academic_unit_id", err)
	}
	return result, nil
}

func (s *SQLExamSubmissionStore) Get(ctx context.Context, submissionID model.SubmissionID) (*model.ExamSubmission, error) {
	if !submissionID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_submission", "id", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "get immutable Exam Submission", func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSubmission, error) {
		var row examSubmissionHeaderRow
		if err := tx.Get(ctx, &row, examSubmissionHeaderSelect+` WHERE id=? AND sealed=true FOR SHARE`, submissionID.String()); err != nil {
			return nil, translateError("exam_submission", submissionID.String(), err)
		}
		submission, err := row.model()
		if err != nil {
			return nil, err
		}
		_, entries, err := loadStoredExamSubmissionManifest(ctx, tx, submissionID)
		if err != nil {
			return nil, err
		}
		manifest, err := model.NewExamSubmissionManifest(submission.WorkspaceCursor, entries)
		if err != nil {
			return nil, invalidPersistedState("exam_submission", "manifest", err)
		}
		if manifest.SchemaVersion != submission.ManifestSchemaVersion || manifest.SHA256 != submission.ManifestDigest ||
			manifest.EntryCount != submission.ManifestEntryCount || manifest.TotalFileBytes != submission.ManifestTotalFileBytes {
			return nil, invalidPersistedState("exam_submission", "manifest", errors.New("manifest summary does not match immutable Entries"))
		}
		return submission, nil
	})
}

func loadStoredExamSubmissionManifest(ctx context.Context, executor sqlxExecutor, submissionID model.SubmissionID) ([]examSubmissionManifestPersistenceRow, []model.ExamSubmissionManifestEntry, error) {
	var rows []examSubmissionManifestPersistenceRow
	if err := executor.Select(ctx, &rows, `SELECT entry_id,kind,path,workspace_object_id,content_version,media_type,
		size_bytes,sha256,storage_origin,starter_object_id,attempt_object_id
		FROM exam_submission_manifest_entries WHERE submission_id=? ORDER BY entry_id`, submissionID.String()); err != nil {
		return nil, nil, fmt.Errorf("load stored Exam Submission manifest: %w", err)
	}
	entries := make([]model.ExamSubmissionManifestEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := row.model()
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	return rows, entries, nil
}

func (s *SQLExamSubmissionStore) ListManifest(ctx context.Context, options store.ExamSubmissionManifestListOptions) (*store.ExamSubmissionManifestPage, error) {
	if !options.SubmissionID.IsValid() || options.Limit < 1 || options.Limit > 200 ||
		(!options.AfterEntryID.IsZero() && !options.AfterEntryID.IsValid()) {
		return nil, store.NewErrInvalidInput("exam_submission_manifest", "list_options", nil)
	}
	header, err := s.Get(ctx, options.SubmissionID)
	if err != nil {
		return nil, err
	}
	query := `SELECT entry_id,kind,path,workspace_object_id,content_version,media_type,size_bytes,sha256,
		storage_origin,starter_object_id,attempt_object_id FROM exam_submission_manifest_entries
		WHERE submission_id=?`
	args := []any{options.SubmissionID.String()}
	if !options.AfterEntryID.IsZero() {
		query += ` AND entry_id>?`
		args = append(args, options.AfterEntryID.String())
	}
	query += ` ORDER BY entry_id LIMIT ?`
	args = append(args, options.Limit+1)
	var rows []examSubmissionManifestPersistenceRow
	if err = s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Submission manifest: %w", err)
	}
	page := &store.ExamSubmissionManifestPage{SubmissionID: header.ID, WorkspaceCursor: header.WorkspaceCursor,
		ManifestDigest: header.ManifestDigest, HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	page.Items = make([]store.ExamSubmissionManifestItem, 0, len(rows))
	for _, row := range rows {
		entry, entryErr := row.model()
		if entryErr != nil {
			return nil, entryErr
		}
		page.Items = append(page.Items, submissionManifestItem(entry))
	}
	return page, nil
}

func (s *SQLExamSubmissionStore) ResolveFile(ctx context.Context, submissionID model.SubmissionID,
	entryID model.AttemptWorkspaceEntryID,
) (*store.ExamSubmissionFileSelector, error) {
	if !submissionID.IsValid() || !entryID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_submission_manifest_entry", "identity", nil)
	}
	if _, err := s.Get(ctx, submissionID); err != nil {
		return nil, err
	}
	var row examSubmissionManifestPersistenceRow
	if err := s.GetMaster().Get(ctx, &row, `SELECT entry_id,kind,path,workspace_object_id,content_version,media_type,
		size_bytes,sha256,storage_origin,starter_object_id,attempt_object_id
		FROM exam_submission_manifest_entries WHERE submission_id=? AND entry_id=?`, submissionID.String(), entryID.String()); err != nil {
		return nil, translateError("exam_submission_manifest_entry", entryID.String(), err)
	}
	entry, err := row.model()
	if err != nil {
		return nil, err
	}
	if entry.Kind != model.StarterWorkspaceEntryFile {
		return nil, store.NewErrNotFound("exam_submission_manifest_entry", entryID.String())
	}
	return &store.ExamSubmissionFileSelector{Entry: submissionManifestItem(entry), StorageOrigin: entry.StorageOrigin,
		StarterObjectID: entry.StarterObjectID, AttemptObjectID: entry.AttemptObjectID, ContentVersion: entry.ContentVersion}, nil
}

func submissionManifestItem(entry model.ExamSubmissionManifestEntry) store.ExamSubmissionManifestItem {
	return store.ExamSubmissionManifestItem{EntryID: entry.EntryID, Kind: entry.Kind, Path: entry.Path,
		ContentVersion: entry.ContentVersion, MediaType: entry.MediaType, SizeBytes: entry.SizeBytes, SHA256: entry.SHA256}
}

var _ store.ExamSubmissionStore = (*SQLExamSubmissionStore)(nil)
