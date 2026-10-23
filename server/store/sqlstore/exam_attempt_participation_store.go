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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type attemptParticipationRenewalRow struct {
	State                string         `db:"state"`
	SessionID            string         `db:"session_id"`
	RegistrationID       string         `db:"desktop_registration_id"`
	KeyThumbprint        string         `db:"dpop_key_thumbprint"`
	Generation           int64          `db:"generation"`
	RenewalSequence      int64          `db:"renewal_sequence"`
	CredentialHash       string         `db:"continuity_credential_hash"`
	StartedAt            time.Time      `db:"started_at"`
	UpdatedAt            time.Time      `db:"updated_at"`
	LeaseExpiresAt       time.Time      `db:"lease_expires_at"`
	EndedAt              sql.NullTime   `db:"ended_at"`
	EndReason            sql.NullString `db:"end_reason"`
	DatabaseNow          time.Time      `db:"database_now"`
	SessionIdleExpiresAt time.Time      `db:"session_idle_expires_at"`
	SessionExpiresAt     time.Time      `db:"session_expires_at"`
}

// RenewParticipation extends the lease of the Candidate's active Participation
// generation; see store.ExamAttemptStore for the sequence and expiry contract.
func (s *SQLExamAttemptStore) RenewParticipation(ctx context.Context, input *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	return s.changeParticipationControl(ctx, input, true)
}

// participationRenewalAttemptRow is the Attempt and Sitting state a
// Participation renewal locks before the Participation itself.
type participationRenewalAttemptRow struct {
	CandidateID    string    `db:"candidate_user_id"`
	ExamID         string    `db:"exam_id"`
	SittingID      string    `db:"exam_sitting_id"`
	State          string    `db:"state"`
	SittingState   string    `db:"sitting_state"`
	ScheduledEndAt time.Time `db:"scheduled_end_at"`
}

func (s *SQLExamAttemptStore) changeParticipationControl(ctx context.Context, input *store.ExamAttemptParticipationRenewal, renew bool) (*store.ExamAttemptParticipationRenewalResult, error) {
	if !validParticipationControl(input, renew) {
		return nil, store.NewErrInvalidInput("attempt_participation", "renewal", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "renew Attempt Participation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptParticipationRenewalResult, error) {
		return s.changeParticipationControlInTx(ctx, tx, input, renew)
	})
}

// validParticipationControl reports whether input is a well-formed renewal,
// or with renew false a well-formed sequence-zero control check.
func validParticipationControl(input *store.ExamAttemptParticipationRenewal, renew bool) bool {
	if input == nil || !input.AttemptID.IsValid() || !input.ParticipationID.IsValid() || !input.ConnectionID.IsValid() ||
		!input.CandidateUserID.IsValid() || !input.SessionID.IsValid() || input.Generation < 1 || renew && input.Sequence < 1 || !renew && input.Sequence != 0 || input.DesktopCompatibilityPolicyRevision < 1 || input.DesktopBuild.Validate() != nil || input.DesktopBuild.NativeAgreement == nil || input.SecurityCoverage.Validate() != nil ||
		!input.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(input.DPoPKeyThumbprint) ||
		!model.IsValidTokenHash(input.ContinuityCredentialHash) {
		return false
	}
	return true
}

// changeParticipationControlInTx locks and checks the Attempt, Participation,
// Session and Connection, processes security coverage, and advances the lease
// when renew carries a new sequence.
func (s *SQLExamAttemptStore) changeParticipationControlInTx(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal,
	renew bool,
) (*store.ExamAttemptParticipationRenewalResult, error) {
	if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, input.DesktopCompatibilityPolicyRevision); err != nil {
		return nil, err
	}
	attempt, examID, sittingID, err := lockParticipationRenewalAttempt(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	row, err := lockParticipationRenewalCredential(ctx, tx, input, attempt.State)
	if err != nil {
		return nil, err
	}
	if err = lockParticipationRenewalConnection(ctx, tx, input); err != nil {
		return nil, err
	}
	if attempt.SittingState != string(model.ExamSittingOpen) && attempt.SittingState != string(model.ExamSittingPaused) {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if err = checkParticipationRenewalTime(ctx, tx, input, &row, attempt.ScheduledEndAt, renew); err != nil {
		return nil, err
	}
	coverage, err := s.processSecurityCoverage(ctx, tx, input, attempt.SittingState == string(model.ExamSittingOpen), row.DatabaseNow)
	if err != nil {
		return nil, err
	}
	if !renew || input.Sequence == row.RenewalSequence {
		return &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: coverage, AttemptID: input.AttemptID, ExamID: examID, SittingID: sittingID,
			CandidateUserID: input.CandidateUserID, ParticipationID: input.ParticipationID,
			Generation: row.Generation, AcceptedSequence: row.RenewalSequence, DatabaseTime: model.TimeUTC(row.UpdatedAt),
			LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), Duplicate: true}, nil
	}
	participation, err := applyParticipationRenewal(ctx, tx, input, row, attempt.ScheduledEndAt)
	if err != nil {
		return nil, err
	}
	return &store.ExamAttemptParticipationRenewalResult{SecurityCoverage: coverage, AttemptID: input.AttemptID, ExamID: examID, SittingID: sittingID,
		CandidateUserID: input.CandidateUserID, ParticipationID: input.ParticipationID,
		Generation: participation.Generation, AcceptedSequence: participation.RenewalSequence,
		DatabaseTime: participation.UpdatedAt, LeaseExpiresAt: participation.LeaseExpiresAt}, nil
}

// lockParticipationRenewalAttempt locks the Attempt and shares its Sitting,
// requiring the input Candidate to own the Attempt.
func lockParticipationRenewalAttempt(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal,
) (participationRenewalAttemptRow, model.ExamID, model.ExamSittingID, error) {
	var attempt participationRenewalAttemptRow
	if err := tx.Get(ctx, &attempt, `SELECT a.candidate_user_id,a.exam_id,a.exam_sitting_id,a.state,s.state AS sitting_state,s.scheduled_end_at
			FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
			WHERE a.id=? FOR UPDATE OF a FOR SHARE OF s`, input.AttemptID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return attempt, "", "", store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		return attempt, "", "", fmt.Errorf("lock Exam Attempt for renewal: %w", err)
	}
	if attempt.CandidateID != input.CandidateUserID.String() {
		return attempt, "", "", store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	examID, examErr := model.ParseExamID(attempt.ExamID)
	sittingID, sittingErr := model.ParseExamSittingID(attempt.SittingID)
	if examErr != nil || sittingErr != nil {
		return attempt, "", "", invalidPersistedState("exam_attempt", "ownership", errors.Join(examErr, sittingErr))
	}
	return attempt, examID, sittingID, nil
}

// lockParticipationRenewalCredential locks the Participation with its Session
// and Desktop Registration and checks the credential, generation and states.
func lockParticipationRenewalCredential(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal,
	attemptState string,
) (attemptParticipationRenewalRow, error) {
	var row attemptParticipationRenewalRow
	if err := tx.Get(ctx, &row, `SELECT p.state,p.session_id,se.desktop_registration_id,se.dpop_key_thumbprint,
			p.generation,p.renewal_sequence,p.continuity_credential_hash,p.started_at,p.updated_at,
			p.lease_expires_at,p.ended_at,p.end_reason,se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at
			FROM exam_attempt_participations p JOIN sessions se ON se.id=p.session_id
			JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
			WHERE p.id=? AND p.exam_attempt_id=? AND se.user_id=? AND se.archived_at IS NULL AND se.revoked_at IS NULL
			AND dr.revoked_at IS NULL FOR UPDATE OF p,se,dr`, input.ParticipationID.String(), input.AttemptID.String(), input.CandidateUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return row, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		return row, fmt.Errorf("lock Attempt Participation renewal: %w", err)
	}
	if row.SessionID != input.SessionID.String() || row.RegistrationID != input.DesktopRegistrationID.String() ||
		row.KeyThumbprint != input.DPoPKeyThumbprint {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if input.Generation != row.Generation {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	}
	if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if row.State != string(model.AttemptParticipationActive) {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if attemptState != string(model.ExamAttemptActive) {
		return row, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	return row, nil
}

// lockParticipationRenewalConnection locks the renewing Connection and
// requires it to be open on the input Session.
func lockParticipationRenewalConnection(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal) error {
	var connection struct {
		SessionID string `db:"session_id"`
		State     string `db:"state"`
	}
	if err := tx.Get(ctx, &connection, `SELECT session_id,state FROM exam_attempt_connections
			WHERE id=? AND exam_attempt_id=? AND participation_id=? FOR UPDATE`, input.ConnectionID.String(), input.AttemptID.String(), input.ParticipationID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		return fmt.Errorf("lock Attempt Connection renewal: %w", err)
	}
	if connection.SessionID != input.SessionID.String() || connection.State != string(model.AttemptConnectionOpen) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	return nil
}

// checkParticipationRenewalTime reads the decision time into row and checks
// the Session, Sitting deadline, lease and renewal sequence against it.
func checkParticipationRenewalTime(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal,
	row *attemptParticipationRenewalRow, scheduledEndAt time.Time, renew bool,
) error {
	if err := tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return fmt.Errorf("read Attempt Participation renewal decision time: %w", err)
	}
	if !row.DatabaseNow.Before(row.SessionIdleExpiresAt) || !row.DatabaseNow.Before(row.SessionExpiresAt) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if !row.DatabaseNow.Before(scheduledEndAt) || !row.DatabaseNow.Before(row.LeaseExpiresAt) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if renew && input.Sequence < row.RenewalSequence {
		return store.NewErrConflict("attempt_participation", "attempt_participation_sequence", nil)
	}
	return nil
}

// applyParticipationRenewal renews the Participation domain state, bounds the
// lease by the Sitting end, and writes it fenced on the prior sequence.
func applyParticipationRenewal(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptParticipationRenewal,
	row attemptParticipationRenewalRow, scheduledEndAt time.Time,
) (*model.AttemptParticipation, error) {
	participation := &model.AttemptParticipation{ID: input.ParticipationID, AttemptID: input.AttemptID, SessionID: input.SessionID,
		State: model.AttemptParticipationActive, Generation: row.Generation, RenewalSequence: row.RenewalSequence,
		ContinuityCredentialHash: row.CredentialHash, StartedAt: model.TimeUTC(row.StartedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	if err := participation.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_participation", "value", err)
	}
	if _, err := participation.Renew(input.Generation, input.Sequence, row.DatabaseNow); err != nil {
		return nil, fmt.Errorf("renew Attempt Participation domain state: %w", err)
	}
	if participation.LeaseExpiresAt.After(scheduledEndAt) {
		participation.LeaseExpiresAt = model.TimeUTC(scheduledEndAt)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_attempt_participations SET renewal_sequence=?,updated_at=?,lease_expires_at=?
			WHERE id=? AND exam_attempt_id=? AND state='active' AND renewal_sequence=?`, participation.RenewalSequence,
		participation.UpdatedAt, participation.LeaseExpiresAt, input.ParticipationID.String(), input.AttemptID.String(), row.RenewalSequence)
	if err != nil {
		return nil, fmt.Errorf("renew Attempt Participation: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return nil, fmt.Errorf("inspect Attempt Participation renewal: %w", rowsErr)
		}
		return nil, store.NewErrConflict("attempt_participation", "attempt_participation_sequence", nil)
	}
	return participation, nil
}

type participationExpiryDueRow struct {
	ExamID          string    `db:"exam_id"`
	SittingID       string    `db:"exam_sitting_id"`
	ClassID         string    `db:"class_id"`
	CandidateID     string    `db:"candidate_user_id"`
	AttemptID       string    `db:"attempt_id"`
	ParticipationID string    `db:"participation_id"`
	Generation      int64     `db:"generation"`
	LeaseExpiresAt  time.Time `db:"lease_expires_at"`
}

const participationExpiryDueSelect = `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,
	a.id AS attempt_id,p.id AS participation_id,p.generation,p.lease_expires_at
	FROM exam_attempt_participations p JOIN exam_attempts a ON a.id=p.exam_attempt_id
	JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id`

// ResolveParticipationExpiry returns the active Participation generation only
// when its lease has expired by the database clock. An unexpired or
// non-matching generation is an attempt_participation_expired conflict.
func (s *SQLExamAttemptStore) ResolveParticipationExpiry(ctx context.Context, attemptID model.ExamAttemptID,
	participationID model.AttemptParticipationID, generation int64,
) (*store.ExamAttemptParticipationExpiryDue, error) {
	if !attemptID.IsValid() || !participationID.IsValid() || generation < 1 {
		return nil, store.NewErrInvalidInput("attempt_participation", "expiry_identity", nil)
	}
	var row participationExpiryDueRow
	err := s.GetMaster().Get(ctx, &row, participationExpiryDueSelect+`
		WHERE a.id=? AND p.id=? AND p.generation=? AND p.state='active' AND p.lease_expires_at<=statement_timestamp()`,
		attemptID.String(), participationID.String(), generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve expired Attempt Participation: %w", err)
	}
	return row.expiryDue()
}

// ListExpiredParticipations returns up to limit (1..200) active Participation
// generations whose lease has expired by the database clock, oldest expiry
// first.
func (s *SQLExamAttemptStore) ListExpiredParticipations(ctx context.Context, limit int) ([]store.ExamAttemptParticipationExpiryDue, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("attempt_participation", "expiry_limit", limit)
	}
	var rows []participationExpiryDueRow
	if err := s.GetMaster().Select(ctx, &rows, participationExpiryDueSelect+`
		WHERE p.state='active' AND p.lease_expires_at<=statement_timestamp()
		ORDER BY p.lease_expires_at,p.id LIMIT ?`, limit); err != nil {
		return nil, fmt.Errorf("list expired Attempt Participations: %w", err)
	}
	result := make([]store.ExamAttemptParticipationExpiryDue, 0, len(rows))
	for _, row := range rows {
		due, err := row.expiryDue()
		if err != nil {
			return nil, err
		}
		result = append(result, *due)
	}
	return result, nil
}

func (row participationExpiryDueRow) expiryDue() (*store.ExamAttemptParticipationExpiryDue, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "exam_sitting_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "class_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "candidate_user_id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "exam_attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "id", err)
	}
	return &store.ExamAttemptParticipationExpiryDue{ExamID: examID, SittingID: sittingID, ClassID: classID,
		CandidateUserID: candidateID, AttemptID: attemptID, ParticipationID: participationID,
		Generation: row.Generation, LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt)}, nil
}

type participationExpiryLockRow struct {
	participationExpiryDueRow
	AttemptState           string         `db:"attempt_state"`
	AdmissionRevisionID    string         `db:"admission_revision_id"`
	ParticipationState     string         `db:"participation_state"`
	SessionID              string         `db:"session_id"`
	CredentialHash         string         `db:"continuity_credential_hash"`
	AttemptCreatedAt       time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt       time.Time      `db:"attempt_updated_at"`
	StartedAt              time.Time      `db:"started_at"`
	ParticipationUpdatedAt time.Time      `db:"participation_updated_at"`
	SubmittedAt            sql.NullTime   `db:"submitted_at"`
	EndedAt                sql.NullTime   `db:"ended_at"`
	AttemptRevision        int64          `db:"attempt_revision"`
	RenewalSequence        int64          `db:"renewal_sequence"`
	EndReason              sql.NullString `db:"end_reason"`
	DatabaseNow            time.Time      `db:"database_now"`
}

// ExpireParticipation implements store.ExamAttemptStore: it ends the expired
// Participation generation as lease_expired, closes its Connection, records
// Connection Loss evidence and a Flag, and suspends the Attempt in one
// transaction. A later call for the same generation returns the retained
// outcome.
func (s *SQLExamAttemptStore) ExpireParticipation(ctx context.Context, input *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ParticipationID.IsValid() || input.Generation < 1 ||
		!input.EvidenceID.IsValid() || !input.FlagID.IsValid() || !input.SuspensionID.IsValid() ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("attempt_participation", "expiry", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "expire Attempt Participation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptParticipationExpiryResult, error) {
		row, err := lockParticipationExpiry(ctx, tx, input.AttemptID, input.ParticipationID)
		if err != nil {
			return nil, err
		}
		if row.Generation != input.Generation {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
		}
		if row.ParticipationState == string(model.AttemptParticipationEnded) && row.EndReason.String == string(model.AttemptParticipationEndLeaseExpired) {
			result, loadErr := loadParticipationExpiryResult(ctx, tx, row, true)
			if loadErr != nil {
				return nil, loadErr
			}
			if auditErr := completeParticipationExpiryAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); auditErr != nil {
				return nil, auditErr
			}
			return result, nil
		}
		var connectionID sql.NullString
		if err = tx.Get(ctx, &connectionID, `SELECT id FROM exam_attempt_connections
			WHERE exam_attempt_id=? AND participation_id=? AND state='open' FOR UPDATE`, input.AttemptID.String(), input.ParticipationID.String()); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("lock expired Attempt Connection: %w", err)
		}
		if err = tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read Attempt Participation expiry decision time: %w", err)
		}
		if row.ParticipationState != string(model.AttemptParticipationActive) || row.DatabaseNow.Before(row.LeaseExpiresAt) {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_not_expired", nil)
		}
		if row.AttemptState != string(model.ExamAttemptActive) {
			return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
		}
		attempt, participation, err := row.domain()
		if err != nil {
			return nil, err
		}
		if err = attempt.Suspend(row.DatabaseNow); err != nil {
			return nil, fmt.Errorf("suspend Exam Attempt: %w", err)
		}
		if err = participation.End(model.AttemptParticipationEndLeaseExpired, row.DatabaseNow); err != nil {
			return nil, fmt.Errorf("end expired Attempt Participation: %w", err)
		}
		flag, err := model.NewIntegrityFlag(input.FlagID, input.AttemptID, input.Generation, model.IntegrityPolicyConnectionLoss, row.DatabaseNow)
		if err != nil {
			return nil, err
		}
		evidence, err := model.NewConnectionLossEvidence(input.EvidenceID, input.AttemptID, input.ParticipationID,
			input.FlagID, input.Generation, row.LeaseExpiresAt, row.DatabaseNow)
		if err != nil {
			return nil, err
		}
		suspension, err := model.NewPolicyAttemptSuspension(input.SuspensionID, input.AttemptID, input.ParticipationID,
			input.FlagID, input.Generation, model.AttemptSuspensionCandidateReasonSecureContinuityLost, row.DatabaseNow)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,revision=? WHERE id=? AND revision=?`,
			attempt.State, attempt.UpdatedAt, attempt.Revision, input.AttemptID.String(), row.AttemptRevision); err != nil {
			return nil, fmt.Errorf("suspend Exam Attempt: %w", err)
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=? WHERE id=?`,
			participation.State, participation.UpdatedAt, participation.EndedAt.Time, participation.EndReason, input.ParticipationID.String()); err != nil {
			return nil, fmt.Errorf("end Attempt Participation: %w", err)
		}
		if err := closeNativeDelivery(ctx, tx, input.ParticipationID, model.DeliveryClosedParticipation, row.LeaseExpiresAt); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state='closed',closed_at=?,close_reason='lease_expired'
			WHERE exam_attempt_id=? AND participation_id=? AND state='open'`, row.DatabaseNow, input.AttemptID.String(), input.ParticipationID.String()); err != nil {
			return nil, fmt.Errorf("close expired Attempt Connection: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO integrity_flags (id,exam_attempt_id,generation,policy_kind,state,created_at) VALUES (?,?,?,?,?,?)`,
			flag.ID.String(), flag.AttemptID.String(), flag.Generation, flag.Kind, flag.State, flag.CreatedAt); err != nil {
			return nil, translateError("integrity_flag", flag.ID.String(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO integrity_evidence (id,exam_attempt_id,participation_id,integrity_flag_id,generation,policy_kind,observed_at,recorded_at)
			VALUES (?,?,?,?,?,?,?,?)`, evidence.ID.String(), evidence.AttemptID.String(), evidence.ParticipationID.String(), evidence.FlagID.String(),
			evidence.Generation, evidence.Kind, evidence.ObservedAt, evidence.RecordedAt); err != nil {
			return nil, translateError("integrity_evidence", evidence.ID.String(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_suspensions (id,exam_attempt_id,participation_id,integrity_flag_id,generation,suspension_attempt_revision,state,source,candidate_reason,started_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, suspension.ID.String(), suspension.AttemptID.String(), suspension.ParticipationID.String(), suspension.FlagID.String(),
			suspension.Generation, attempt.Revision, suspension.State, suspension.Source, suspension.CandidateReason, suspension.StartedAt); err != nil {
			return nil, translateError("attempt_suspension", suspension.ID.String(), err)
		}
		row.AttemptState, row.AttemptUpdatedAt, row.AttemptRevision = string(attempt.State), attempt.UpdatedAt, attempt.Revision
		row.ParticipationState, row.ParticipationUpdatedAt = string(participation.State), participation.UpdatedAt
		row.EndedAt, row.EndReason = sql.NullTime{Time: participation.EndedAt.Time, Valid: true}, sql.NullString{String: string(participation.EndReason), Valid: true}
		result, err := loadParticipationExpiryResult(ctx, tx, row, false)
		if err != nil {
			return nil, err
		}
		if err = completeParticipationExpiryAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); err != nil {
			return nil, err
		}
		return result, nil
	})
}

func lockParticipationExpiry(ctx context.Context, tx *sqlxTxWrapper, attemptID model.ExamAttemptID,
	participationID model.AttemptParticipationID,
) (participationExpiryLockRow, error) {
	var row participationExpiryLockRow
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.id AS attempt_id,
		p.id AS participation_id,p.generation,p.lease_expires_at,a.state AS attempt_state,a.admission_revision_id,
		a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
		p.state AS participation_state,p.session_id,p.renewal_sequence,p.continuity_credential_hash,p.started_at,
		p.updated_at AS participation_updated_at,p.ended_at,p.end_reason
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id
		WHERE a.id=? AND p.id=? FOR UPDATE OF a,p`, attemptID.String(), participationID.String())
	if err != nil {
		return row, translateError("attempt_participation", participationID.String(), err)
	}
	return row, nil
}

func (row participationExpiryLockRow) domain() (*model.ExamAttempt, *model.AttemptParticipation, error) {
	due, err := row.expiryDue()
	if err != nil {
		return nil, nil, err
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	attempt := &model.ExamAttempt{ID: due.AttemptID, ExamID: due.ExamID, SittingID: due.SittingID,
		CandidateUserID: due.CandidateUserID, AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState),
		CreatedAt: model.TimeUTC(row.AttemptCreatedAt), UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt),
		SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return nil, nil, invalidPersistedState("exam_attempt", "value", err)
	}
	sessionID, err := model.ParseSessionID(row.SessionID)
	if err != nil {
		return nil, nil, invalidPersistedState("attempt_participation", "session_id", err)
	}
	participation := &model.AttemptParticipation{ID: due.ParticipationID, AttemptID: due.AttemptID, SessionID: sessionID,
		State: model.AttemptParticipationState(row.ParticipationState), Generation: row.Generation,
		RenewalSequence: row.RenewalSequence, ContinuityCredentialHash: row.CredentialHash,
		StartedAt: model.TimeUTC(row.StartedAt), UpdatedAt: model.TimeUTC(row.ParticipationUpdatedAt),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	if err = participation.Validate(); err != nil {
		return nil, nil, invalidPersistedState("attempt_participation", "value", err)
	}
	return attempt, participation, nil
}

type participationExpiryRecordRow struct {
	FlagID                    string         `db:"flag_id"`
	FlagKind                  string         `db:"flag_kind"`
	FlagState                 string         `db:"flag_state"`
	EvidenceID                string         `db:"evidence_id"`
	EvidenceKind              string         `db:"evidence_kind"`
	SuspensionID              string         `db:"suspension_id"`
	SuspensionState           string         `db:"suspension_state"`
	SuspensionAttemptRevision int64          `db:"suspension_attempt_revision"`
	SuspensionSource          string         `db:"suspension_source"`
	CandidateReason           string         `db:"candidate_reason"`
	ReallowedByUserID         string         `db:"reallowed_by_user_id"`
	FlagCreatedAt             time.Time      `db:"flag_created_at"`
	ObservedAt                time.Time      `db:"observed_at"`
	RecordedAt                time.Time      `db:"recorded_at"`
	SuspensionStartedAt       time.Time      `db:"suspension_started_at"`
	SuspensionEndedAt         sql.NullTime   `db:"ended_at"`
	PrivateReason             sql.NullString `db:"private_reason"`
}

func loadParticipationExpiryResult(ctx context.Context, tx *sqlxTxWrapper, row participationExpiryLockRow,
	replayed bool,
) (*store.ExamAttemptParticipationExpiryResult, error) {
	currentAttempt, participation, err := row.domain()
	if err != nil {
		return nil, err
	}
	due, err := row.expiryDue()
	if err != nil {
		return nil, err
	}
	var records participationExpiryRecordRow
	err = tx.Get(ctx, &records, `SELECT f.id AS flag_id,f.policy_kind AS flag_kind,f.state AS flag_state,
		f.created_at AS flag_created_at,e.id AS evidence_id,e.policy_kind AS evidence_kind,e.observed_at,e.recorded_at,
		su.id AS suspension_id,su.state AS suspension_state,su.suspension_attempt_revision,su.source AS suspension_source,su.candidate_reason,
		su.started_at AS suspension_started_at,su.ended_at,COALESCE(su.reallowed_by_user_id,'') AS reallowed_by_user_id,su.private_reason
		FROM integrity_flags f JOIN integrity_evidence e ON e.integrity_flag_id=f.id
		JOIN exam_attempt_suspensions su ON su.integrity_flag_id=f.id
		WHERE f.exam_attempt_id=? AND f.generation=? AND f.policy_kind='connection_loss'`, row.AttemptID, row.Generation)
	if err != nil {
		return nil, translateError("attempt_expiry", row.AttemptID, err)
	}
	flagID, err := model.ParseIntegrityFlagID(records.FlagID)
	if err != nil {
		return nil, invalidPersistedState("integrity_flag", "id", err)
	}
	evidenceID, err := model.ParseIntegrityEvidenceID(records.EvidenceID)
	if err != nil {
		return nil, invalidPersistedState("integrity_evidence", "id", err)
	}
	suspensionID, err := model.ParseAttemptSuspensionID(records.SuspensionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "id", err)
	}
	flag := &model.IntegrityFlag{ID: flagID, AttemptID: due.AttemptID, Generation: row.Generation,
		Kind: model.IntegrityPolicyKind(records.FlagKind), State: model.IntegrityFlagState(records.FlagState), CreatedAt: model.TimeUTC(records.FlagCreatedAt)}
	if err = flag.Validate(); err != nil {
		return nil, invalidPersistedState("integrity_flag", "value", err)
	}
	evidence := &model.IntegrityEvidence{ID: evidenceID, AttemptID: due.AttemptID, ParticipationID: due.ParticipationID,
		FlagID: flagID, Generation: row.Generation, Kind: model.IntegrityPolicyKind(records.EvidenceKind),
		ObservedAt: model.TimeUTC(records.ObservedAt), RecordedAt: model.TimeUTC(records.RecordedAt)}
	if err = evidence.Validate(); err != nil {
		return nil, invalidPersistedState("integrity_evidence", "value", err)
	}
	var reallowed model.UserID
	if records.ReallowedByUserID != "" {
		reallowed, err = model.ParseUserID(records.ReallowedByUserID)
		if err != nil {
			return nil, invalidPersistedState("attempt_suspension", "reallowed_by_user_id", err)
		}
	}
	currentSuspension := &model.AttemptSuspension{ID: suspensionID, AttemptID: due.AttemptID, ParticipationID: due.ParticipationID,
		FlagID: flagID, Generation: row.Generation, State: model.AttemptSuspensionState(records.SuspensionState),
		Source: model.AttemptSuspensionSource(records.SuspensionSource), CandidateReason: model.AttemptSuspensionCandidateReason(records.CandidateReason),
		StartedAt: model.TimeUTC(records.SuspensionStartedAt), EndedAt: OptionalTimeFromNullTime(records.SuspensionEndedAt),
		ReallowedByUserID: reallowed, PrivateReason: records.PrivateReason.String}
	if err = currentSuspension.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_suspension", "value", err)
	}
	historicalSuspension, err := model.NewPolicyAttemptSuspension(suspensionID, due.AttemptID, due.ParticipationID,
		flagID, row.Generation, model.AttemptSuspensionCandidateReason(records.CandidateReason), records.SuspensionStartedAt)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "expiry_value", err)
	}
	attempt := *currentAttempt
	attempt.State, attempt.UpdatedAt, attempt.SubmittedAt, attempt.Revision = model.ExamAttemptSuspended,
		model.TimeUTC(records.SuspensionStartedAt), model.OptionalTime{}, records.SuspensionAttemptRevision
	if err = attempt.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "expiry_value", err)
	}
	suspension := &store.ExamAttemptSuspensionView{ID: historicalSuspension.ID, AttemptID: historicalSuspension.AttemptID,
		ParticipationID: historicalSuspension.ParticipationID, FlagID: historicalSuspension.FlagID, Generation: historicalSuspension.Generation,
		State: historicalSuspension.State, Source: historicalSuspension.Source, CandidateReason: historicalSuspension.CandidateReason,
		StartedAt: historicalSuspension.StartedAt}
	var connectionRow attemptConnectionRow
	err = tx.Get(ctx, &connectionRow, `SELECT id,exam_attempt_id,participation_id,session_id,state,opened_at,closed_at,close_reason
		FROM exam_attempt_connections WHERE participation_id=? ORDER BY opened_at DESC,id DESC LIMIT 1`, row.ParticipationID)
	var connection *store.ExamAttemptManagerConnection
	connectionClosed := false
	if err == nil {
		id, parseErr := model.ParseAttemptConnectionID(connectionRow.ID)
		if parseErr != nil {
			return nil, invalidPersistedState("attempt_connection", "id", parseErr)
		}
		attemptID, parseErr := model.ParseExamAttemptID(connectionRow.AttemptID)
		if parseErr != nil {
			return nil, invalidPersistedState("attempt_connection", "exam_attempt_id", parseErr)
		}
		participationID, parseErr := model.ParseAttemptParticipationID(connectionRow.ParticipationID)
		if parseErr != nil {
			return nil, invalidPersistedState("attempt_connection", "participation_id", parseErr)
		}
		sessionID, parseErr := model.ParseSessionID(connectionRow.SessionID)
		if parseErr != nil {
			return nil, invalidPersistedState("attempt_connection", "session_id", parseErr)
		}
		domainConnection := &model.AttemptConnection{ID: id, AttemptID: attemptID, ParticipationID: participationID,
			SessionID: sessionID, State: model.AttemptConnectionState(connectionRow.State), OpenedAt: model.TimeUTC(connectionRow.OpenedAt),
			ClosedAt: OptionalTimeFromNullTime(connectionRow.ClosedAt), CloseReason: model.AttemptConnectionCloseReason(connectionRow.CloseReason.String)}
		if attemptID != due.AttemptID || participationID != due.ParticipationID {
			return nil, invalidPersistedState("attempt_connection", "ownership", errors.New("mismatched Attempt Connection ownership"))
		}
		if parseErr = domainConnection.Validate(); parseErr != nil {
			return nil, invalidPersistedState("attempt_connection", "value", parseErr)
		}
		connectionClosed = domainConnection.CloseReason == model.AttemptConnectionCloseLeaseExpired
		connection = &store.ExamAttemptManagerConnection{ID: id, State: domainConnection.State,
			OpenedAt: domainConnection.OpenedAt, ClosedAt: domainConnection.ClosedAt, CloseReason: domainConnection.CloseReason}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load expired Attempt Connection: %w", err)
	}
	return &store.ExamAttemptParticipationExpiryResult{ExamID: due.ExamID, SittingID: due.SittingID, ClassID: due.ClassID,
		CandidateUserID: due.CandidateUserID, Attempt: &attempt,
		Participation: &store.ExamAttemptParticipationView{ID: participation.ID, AttemptID: participation.AttemptID, State: participation.State,
			Generation: participation.Generation, RenewalSequence: participation.RenewalSequence, StartedAt: participation.StartedAt,
			UpdatedAt: participation.UpdatedAt, LeaseExpiresAt: participation.LeaseExpiresAt, EndedAt: participation.EndedAt, EndReason: participation.EndReason},
		Connection: connection, ConnectionClosed: connectionClosed, Evidence: evidence, Flag: flag, Suspension: suspension,
		DatabaseTime: model.TimeUTC(records.FlagCreatedAt), Replayed: replayed}, nil
}

func completeParticipationExpiryAudit(ctx context.Context, tx *sqlxTxWrapper, result *store.ExamAttemptParticipationExpiryResult,
	auditID model.AuditEventID, auditAt time.Time,
) error {
	data, err := model.EncodeAuditData(map[string]any{"exam_id": result.ExamID.String(), "exam_sitting_id": result.SittingID.String(),
		"exam_attempt_id": result.Attempt.ID.String(), "participation_id": result.Participation.ID.String(),
		"generation": result.Participation.Generation, "integrity_evidence_id": result.Evidence.ID.String(),
		"integrity_flag_id": result.Flag.ID.String(), "suspension_id": result.Suspension.ID.String(), "replayed": result.Replayed})
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", data, auditAt); err != nil {
		return fmt.Errorf("complete Attempt Participation expiry audit: %w", err)
	}
	return nil
}
