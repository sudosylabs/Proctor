// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const examAttemptConnectOutcomeMaximumBytes = 2 * 1024

type sqlExamAttemptStore struct{ *SQLStore }

func newSQLExamAttemptStore(sqlStore *SQLStore) store.ExamAttemptStore {
	return &sqlExamAttemptStore{SQLStore: sqlStore}
}

type examAttemptConnectOutcomeV1 struct {
	AttemptID        string    `json:"a"`
	WorkspaceID      string    `json:"w"`
	ParticipationID  string    `json:"p"`
	ConnectionID     string    `json:"c"`
	ExamID           string    `json:"e"`
	SittingID        string    `json:"s"`
	CandidateID      string    `json:"u"`
	RevisionID       string    `json:"r"`
	SessionID        string    `json:"n"`
	ClassID          string    `json:"l"`
	StartedAt        time.Time `json:"t"`
	LeaseExpiresAt   time.Time `json:"x"`
	EntryCount       int       `json:"q"`
	Generation       int64     `json:"g"`
	FirstAdmission   bool      `json:"f"`
	ConnectionOpened bool      `json:"o"`
}

type examAttemptAdmissionGuard struct {
	ExamID       string    `db:"exam_id"`
	RevisionID   string    `db:"exam_revision_id"`
	ClassID      string    `db:"class_id"`
	PeriodID     string    `db:"academic_period_id"`
	State        string    `db:"state"`
	ScheduledEnd time.Time `db:"scheduled_end_at"`
	DatabaseNow  time.Time `db:"database_now"`
}

type examAttemptStarterRow struct {
	EntryID        string         `db:"entry_id"`
	Kind           string         `db:"kind"`
	Path           string         `db:"path"`
	ObjectID       sql.NullString `db:"object_id"`
	ContentVersion sql.NullString `db:"content_version"`
	MediaType      sql.NullString `db:"media_type"`
	SizeBytes      sql.NullInt64  `db:"size_bytes"`
	SHA256         sql.NullString `db:"sha256"`
}

func (s *sqlExamAttemptStore) Connect(ctx context.Context, input *store.ExamAttemptConnect, command *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	if err := validateExamAttemptConnect(input, command); err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "connect Exam Attempt", idempotentMutation[examAttemptConnectOutcomeV1]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examAttemptConnectOutcomeV1, error) {
			return s.connect(ctx, tx, input)
		},
		encode: func(outcome examAttemptConnectOutcomeV1) ([]byte, error) {
			encoded, encodeErr := encodeCommandOutcome(outcome)
			if encodeErr == nil && len(encoded) > examAttemptConnectOutcomeMaximumBytes {
				return nil, store.NewErrInvalidInput("exam_attempt", "connect_outcome", len(encoded))
			}
			return encoded, encodeErr
		},
		decode: func(version int, encoded []byte) (examAttemptConnectOutcomeV1, error) {
			var outcome examAttemptConnectOutcomeV1
			if version != 1 {
				return outcome, fmt.Errorf("unsupported Exam Attempt connect outcome version %d", version)
			}
			if err := decodeCommandOutcome(encoded, &outcome); err != nil {
				return outcome, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome examAttemptConnectOutcomeV1, originalAuditID string) error {
			guard, lockErr := s.lockExamAttemptEligibility(ctx, tx, input, true)
			if lockErr != nil {
				return lockErr
			}
			if guard.ExamID != outcome.ExamID || guard.ClassID != outcome.ClassID || outcome.SittingID != input.SittingID.String() ||
				outcome.CandidateID != input.CandidateUserID.String() || outcome.SessionID != input.SessionID.String() {
				return store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
			}
			return completeExamAttemptConnectAudit(ctx, tx, outcome, true, originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	aggregate, err := s.examAttemptConnectResult(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	aggregate.Replayed = result.Replayed
	aggregate.ConnectionOpened = result.Value.ConnectionOpened && !result.Replayed
	return aggregate, nil
}

func validateExamAttemptConnect(input *store.ExamAttemptConnect, command *store.CommandIdempotency) error {
	if input == nil || command == nil ||
		command.Operation != store.ExamAttemptConnectOperation || command.OutcomeVersion != 1 ||
		!input.SittingID.IsValid() || !input.CandidateUserID.IsValid() || !input.SessionID.IsValid() ||
		command.UserID != input.CandidateUserID || !input.AttemptID.IsValid() || !input.WorkspaceID.IsValid() || !input.ParticipationID.IsValid() || !input.ConnectionID.IsValid() ||
		!model.IsValidTokenHash(input.ContinuityCredentialHash) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_attempt", "connect", nil)
	}
	return nil
}

type attemptParticipationRenewalRow struct {
	State           string         `db:"state"`
	Generation      int64          `db:"generation"`
	RenewalSequence int64          `db:"renewal_sequence"`
	CredentialHash  string         `db:"continuity_credential_hash"`
	StartedAt       time.Time      `db:"started_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	LeaseExpiresAt  time.Time      `db:"lease_expires_at"`
	EndedAt         sql.NullTime   `db:"ended_at"`
	EndReason       sql.NullString `db:"end_reason"`
	DatabaseNow     time.Time      `db:"database_now"`
}

func (s *sqlExamAttemptStore) RenewParticipation(ctx context.Context, input *store.ExamAttemptParticipationRenewal) (*store.ExamAttemptParticipationRenewalResult, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ParticipationID.IsValid() || !input.ConnectionID.IsValid() ||
		!input.CandidateUserID.IsValid() || !input.SessionID.IsValid() || input.Generation < 1 || input.Sequence < 1 ||
		!model.IsValidTokenHash(input.ContinuityCredentialHash) {
		return nil, store.NewErrInvalidInput("attempt_participation", "renewal", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "renew Attempt Participation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptParticipationRenewalResult, error) {
		var attempt struct {
			CandidateID string `db:"candidate_user_id"`
			State       string `db:"state"`
		}
		if err := tx.Get(ctx, &attempt, `SELECT candidate_user_id,state FROM exam_attempts WHERE id=? FOR UPDATE`, input.AttemptID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
			}
			return nil, fmt.Errorf("lock Exam Attempt for renewal: %w", err)
		}
		if attempt.CandidateID != input.CandidateUserID.String() {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		var row attemptParticipationRenewalRow
		if err := tx.Get(ctx, &row, `SELECT state,generation,renewal_sequence,continuity_credential_hash,started_at,updated_at,
			lease_expires_at,ended_at,end_reason
			FROM exam_attempt_participations WHERE id=? AND exam_attempt_id=? FOR UPDATE`, input.ParticipationID.String(), input.AttemptID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
			}
			return nil, fmt.Errorf("lock Attempt Participation renewal: %w", err)
		}
		if input.Generation != row.Generation {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
		}
		if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		if row.State != string(model.AttemptParticipationActive) {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
		}
		if attempt.State != string(model.ExamAttemptActive) {
			return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
		}

		var connection struct {
			SessionID string `db:"session_id"`
			State     string `db:"state"`
		}
		if err := tx.Get(ctx, &connection, `SELECT session_id,state FROM exam_attempt_connections
			WHERE id=? AND exam_attempt_id=? AND participation_id=? FOR UPDATE`, input.ConnectionID.String(), input.AttemptID.String(), input.ParticipationID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
			}
			return nil, fmt.Errorf("lock Attempt Connection renewal: %w", err)
		}
		if connection.SessionID != input.SessionID.String() || connection.State != string(model.AttemptConnectionOpen) {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
		}
		if err := tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read Attempt Participation renewal decision time: %w", err)
		}
		if !row.DatabaseNow.Before(row.LeaseExpiresAt) {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
		}
		if input.Sequence < row.RenewalSequence {
			return nil, store.NewErrConflict("attempt_participation", "attempt_participation_sequence", nil)
		}
		if input.Sequence == row.RenewalSequence {
			return &store.ExamAttemptParticipationRenewalResult{AttemptID: input.AttemptID, ParticipationID: input.ParticipationID,
				Generation: row.Generation, AcceptedSequence: row.RenewalSequence, DatabaseTime: model.TimeUTC(row.UpdatedAt),
				LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), Duplicate: true}, nil
		}

		participation := &model.AttemptParticipation{ID: input.ParticipationID, AttemptID: input.AttemptID,
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
		return &store.ExamAttemptParticipationRenewalResult{AttemptID: input.AttemptID, ParticipationID: input.ParticipationID,
			Generation: participation.Generation, AcceptedSequence: participation.RenewalSequence,
			DatabaseTime: participation.UpdatedAt, LeaseExpiresAt: participation.LeaseExpiresAt}, nil
	})
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

func (s *sqlExamAttemptStore) ResolveParticipationExpiry(ctx context.Context, attemptID model.ExamAttemptID,
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

func (s *sqlExamAttemptStore) ListExpiredParticipations(ctx context.Context, limit int) ([]store.ExamAttemptParticipationExpiryDue, error) {
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

func (s *sqlExamAttemptStore) ExpireParticipation(ctx context.Context, input *store.ExamAttemptParticipationExpiry) (*store.ExamAttemptParticipationExpiryResult, error) {
	if input == nil || !input.AttemptID.IsValid() || !input.ParticipationID.IsValid() || input.Generation < 1 ||
		!input.EvidenceID.IsValid() || !input.FlagID.IsValid() || !input.SuspensionID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
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
		if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_suspensions (id,exam_attempt_id,participation_id,integrity_flag_id,generation,expiry_attempt_revision,state,source,candidate_reason,started_at)
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
		p.state AS participation_state,p.renewal_sequence,p.continuity_credential_hash,p.started_at,
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
	participation := &model.AttemptParticipation{ID: due.ParticipationID, AttemptID: due.AttemptID,
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
	FlagID                string         `db:"flag_id"`
	FlagKind              string         `db:"flag_kind"`
	FlagState             string         `db:"flag_state"`
	EvidenceID            string         `db:"evidence_id"`
	EvidenceKind          string         `db:"evidence_kind"`
	SuspensionID          string         `db:"suspension_id"`
	SuspensionState       string         `db:"suspension_state"`
	ExpiryAttemptRevision int64          `db:"expiry_attempt_revision"`
	SuspensionSource      string         `db:"suspension_source"`
	CandidateReason       string         `db:"candidate_reason"`
	ReallowedByUserID     string         `db:"reallowed_by_user_id"`
	FlagCreatedAt         time.Time      `db:"flag_created_at"`
	ObservedAt            time.Time      `db:"observed_at"`
	RecordedAt            time.Time      `db:"recorded_at"`
	SuspensionStartedAt   time.Time      `db:"suspension_started_at"`
	SuspensionEndedAt     sql.NullTime   `db:"ended_at"`
	PrivateReason         sql.NullString `db:"private_reason"`
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
		su.id AS suspension_id,su.state AS suspension_state,su.expiry_attempt_revision,su.source AS suspension_source,su.candidate_reason,
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
		model.TimeUTC(records.SuspensionStartedAt), model.OptionalTime{}, records.ExpiryAttemptRevision
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
	auditID string, auditAt int64,
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

type examAttemptReallowOutcomeV1 struct {
	ExamID, SittingID, ClassID, CandidateID, AttemptID, SuspensionID string
}

func (s *sqlExamAttemptStore) ReallowAttempt(ctx context.Context, input *store.ExamAttemptReallow,
	command *store.CommandIdempotency,
) (*store.ExamAttemptReallowResult, error) {
	if input == nil || command == nil || command.Operation != store.ExamAttemptReallowOperation || command.OutcomeVersion != 1 ||
		command.UserID != input.ActorUserID || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.AttemptID.IsValid() ||
		!input.SuspensionID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedAttemptRevision < 1 || input.ChangedAt.IsZero() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_attempt", "reallow", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "re-allow Exam Attempt", idempotentMutation[examAttemptReallowOutcomeV1]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examAttemptReallowOutcomeV1, error) {
			return reallowExamAttempt(ctx, tx, input)
		},
		encode: func(value examAttemptReallowOutcomeV1) ([]byte, error) { return encodeCommandOutcome(value) },
		decode: func(version int, encoded []byte) (examAttemptReallowOutcomeV1, error) {
			var value examAttemptReallowOutcomeV1
			if version != 1 {
				return value, fmt.Errorf("unsupported Exam Attempt re-allow outcome version %d", version)
			}
			return value, decodeCommandOutcome(encoded, &value)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value examAttemptReallowOutcomeV1, originalAuditID string) error {
			if err := guardExamAttemptReallowAuthority(ctx, tx, input, false); err != nil {
				return err
			}
			return completeExamAttemptReallowAudit(ctx, tx, value, true, originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	aggregate, err := s.loadExamAttemptReallowResult(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	aggregate.Replayed = result.Replayed
	return aggregate, nil
}

type examAttemptReallowGuard struct {
	AttemptState   string `db:"attempt_state"`
	SittingState   string `db:"sitting_state"`
	ActorIsManager bool   `db:"actor_is_manager"`
}

func guardExamAttemptReallowAuthority(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptReallow,
	lock bool,
) error {
	var guard examAttemptReallowGuard
	query := `SELECT a.state AS attempt_state,s.state AS sitting_state,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=a.exam_id AND m.user_id=?) AS actor_is_manager
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		WHERE a.id=? AND a.exam_id=? AND a.exam_sitting_id=?`
	if lock {
		query += ` FOR UPDATE OF a,s`
	}
	err := tx.Get(ctx, &guard, query, input.ActorUserID.String(), input.AttemptID.String(), input.ExamID.String(), input.SittingID.String())
	if err != nil {
		return translateError("exam_attempt", input.AttemptID.String(), err)
	}
	if !guard.ActorIsManager && !input.ManagerOverride {
		return store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if guard.SittingState == string(model.ExamSittingClosing) || guard.SittingState == string(model.ExamSittingClosed) {
		return store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	return nil
}

func reallowExamAttempt(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptReallow) (examAttemptReallowOutcomeV1, error) {
	var zero examAttemptReallowOutcomeV1
	if err := guardExamAttemptReallowAuthority(ctx, tx, input, true); err != nil {
		return zero, err
	}
	var row struct {
		ExamID              string         `db:"exam_id"`
		SittingID           string         `db:"exam_sitting_id"`
		ClassID             string         `db:"class_id"`
		CandidateID         string         `db:"candidate_user_id"`
		AdmissionRevisionID string         `db:"admission_revision_id"`
		AttemptState        string         `db:"attempt_state"`
		AttemptCreatedAt    time.Time      `db:"attempt_created_at"`
		AttemptUpdatedAt    time.Time      `db:"attempt_updated_at"`
		SubmittedAt         sql.NullTime   `db:"submitted_at"`
		AttemptRevision     int64          `db:"attempt_revision"`
		ParticipationID     string         `db:"participation_id"`
		FlagID              string         `db:"flag_id"`
		SuspensionState     string         `db:"suspension_state"`
		Source              string         `db:"source"`
		CandidateReason     string         `db:"candidate_reason"`
		Generation          int64          `db:"generation"`
		StartedAt           time.Time      `db:"started_at"`
		EndedAt             sql.NullTime   `db:"ended_at"`
		ReallowedByUserID   sql.NullString `db:"reallowed_by_user_id"`
		PrivateReason       sql.NullString `db:"private_reason"`
	}
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.admission_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,
		a.revision AS attempt_revision,su.participation_id,su.integrity_flag_id AS flag_id,su.state AS suspension_state,
		su.source,su.candidate_reason,su.generation,su.started_at,su.ended_at,su.reallowed_by_user_id,su.private_reason
		FROM exam_attempt_suspensions su JOIN exam_attempts a ON a.id=su.exam_attempt_id
		JOIN exam_sittings s ON s.id=a.exam_sitting_id WHERE su.id=? AND su.exam_attempt_id=? FOR UPDATE OF su`,
		input.SuspensionID.String(), input.AttemptID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return zero, store.NewErrConflict("attempt_suspension", "attempt_suspension_active", nil)
	}
	if err != nil {
		return zero, fmt.Errorf("lock Attempt Suspension: %w", err)
	}
	if row.SuspensionState != string(model.AttemptSuspensionActive) {
		return zero, store.NewErrConflict("attempt_suspension", "attempt_suspension_active", nil)
	}
	if row.AttemptRevision != input.ExpectedAttemptRevision {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_revision", nil)
	}
	if row.AttemptState != string(model.ExamAttemptSuspended) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	attemptID := input.AttemptID
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return zero, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return zero, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return zero, invalidPersistedState("attempt_suspension", "participation_id", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.FlagID)
	if err != nil {
		return zero, invalidPersistedState("attempt_suspension", "integrity_flag_id", err)
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: input.ExamID, SittingID: input.SittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	suspension := &model.AttemptSuspension{ID: input.SuspensionID, AttemptID: attemptID, ParticipationID: participationID,
		FlagID: flagID, Generation: row.Generation, State: model.AttemptSuspensionState(row.SuspensionState),
		Source: model.AttemptSuspensionSource(row.Source), CandidateReason: model.AttemptSuspensionCandidateReason(row.CandidateReason),
		StartedAt: model.TimeUTC(row.StartedAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt)}
	if err = attempt.Validate(); err != nil {
		return zero, invalidPersistedState("exam_attempt", "value", err)
	}
	if err = suspension.Validate(); err != nil {
		return zero, invalidPersistedState("attempt_suspension", "value", err)
	}
	if err = suspension.Reallow(input.ActorUserID, input.PrivateReason, input.ChangedAt); err != nil {
		return zero, store.NewErrInvalidInput("attempt_suspension", "private_reason", nil)
	}
	if err = attempt.Reallow(input.ChangedAt); err != nil {
		return zero, fmt.Errorf("re-allow Exam Attempt: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempt_suspensions SET state='closed',ended_at=?,reallowed_by_user_id=?,private_reason=? WHERE id=? AND state='active'`,
		suspension.EndedAt.Time, suspension.ReallowedByUserID.String(), suspension.PrivateReason, suspension.ID.String()); err != nil {
		return zero, fmt.Errorf("close Attempt Suspension: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempts SET state='active',updated_at=?,revision=? WHERE id=? AND revision=?`,
		attempt.UpdatedAt, attempt.Revision, attempt.ID.String(), row.AttemptRevision); err != nil {
		return zero, fmt.Errorf("re-allow Exam Attempt: %w", err)
	}
	value := examAttemptReallowOutcomeV1{ExamID: input.ExamID.String(), SittingID: input.SittingID.String(), ClassID: row.ClassID,
		CandidateID: row.CandidateID, AttemptID: input.AttemptID.String(), SuspensionID: input.SuspensionID.String()}
	if err = completeExamAttemptReallowAudit(ctx, tx, value, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return value, nil
}

func completeExamAttemptReallowAudit(ctx context.Context, tx *sqlxTxWrapper, value examAttemptReallowOutcomeV1,
	replayed bool, originalAuditID, auditID string, auditAt int64,
) error {
	data := map[string]any{"exam_id": value.ExamID, "exam_sitting_id": value.SittingID, "exam_attempt_id": value.AttemptID,
		"suspension_id": value.SuspensionID, "replayed": replayed}
	if replayed {
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Attempt re-allow audit: %w", err)
	}
	return nil
}

func (s *sqlExamAttemptStore) loadExamAttemptReallowResult(ctx context.Context, value examAttemptReallowOutcomeV1) (*store.ExamAttemptReallowResult, error) {
	var row struct {
		ExamID              string       `db:"exam_id"`
		SittingID           string       `db:"exam_sitting_id"`
		ClassID             string       `db:"class_id"`
		CandidateID         string       `db:"candidate_user_id"`
		AttemptID           string       `db:"attempt_id"`
		AdmissionRevisionID string       `db:"admission_revision_id"`
		AttemptState        string       `db:"attempt_state"`
		AttemptCreatedAt    time.Time    `db:"attempt_created_at"`
		AttemptUpdatedAt    time.Time    `db:"attempt_updated_at"`
		SubmittedAt         sql.NullTime `db:"submitted_at"`
		AttemptRevision     int64        `db:"attempt_revision"`
		SuspensionID        string       `db:"suspension_id"`
		ParticipationID     string       `db:"participation_id"`
		FlagID              string       `db:"flag_id"`
		SuspensionState     string       `db:"suspension_state"`
		Source              string       `db:"source"`
		CandidateReason     string       `db:"candidate_reason"`
		Generation          int64        `db:"generation"`
		StartedAt           time.Time    `db:"started_at"`
		EndedAt             sql.NullTime `db:"ended_at"`
		ReallowedByUserID   string       `db:"reallowed_by_user_id"`
		PrivateReason       string       `db:"private_reason"`
	}
	err := s.GetMaster().Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.id AS attempt_id,a.admission_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
		su.id AS suspension_id,su.participation_id,su.integrity_flag_id AS flag_id,su.state AS suspension_state,su.source,
		su.candidate_reason,su.generation,su.started_at,su.ended_at,su.reallowed_by_user_id,su.private_reason
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id JOIN exam_attempt_suspensions su ON su.exam_attempt_id=a.id
		WHERE a.id=? AND su.id=?`, value.AttemptID, value.SuspensionID)
	if err != nil {
		return nil, translateError("attempt_suspension", value.SuspensionID, err)
	}
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
		return nil, invalidPersistedState("exam_attempt", "class_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	suspensionID, err := model.ParseAttemptSuspensionID(row.SuspensionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "participation_id", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.FlagID)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "integrity_flag_id", err)
	}
	reallowedID, err := model.ParseUserID(row.ReallowedByUserID)
	if err != nil {
		return nil, invalidPersistedState("attempt_suspension", "reallowed_by_user_id", err)
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "value", err)
	}
	domainSuspension := &model.AttemptSuspension{ID: suspensionID, AttemptID: attemptID, ParticipationID: participationID,
		FlagID: flagID, Generation: row.Generation, State: model.AttemptSuspensionState(row.SuspensionState),
		Source: model.AttemptSuspensionSource(row.Source), CandidateReason: model.AttemptSuspensionCandidateReason(row.CandidateReason),
		StartedAt: model.TimeUTC(row.StartedAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		ReallowedByUserID: reallowedID, PrivateReason: row.PrivateReason}
	if err = domainSuspension.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_suspension", "value", err)
	}
	return &store.ExamAttemptReallowResult{ExamID: examID, SittingID: sittingID, ClassID: classID, CandidateUserID: candidateID,
		Attempt: attempt,
		Suspension: &store.ExamAttemptSuspensionView{ID: domainSuspension.ID, AttemptID: domainSuspension.AttemptID,
			ParticipationID: domainSuspension.ParticipationID, FlagID: domainSuspension.FlagID, Generation: domainSuspension.Generation,
			State: domainSuspension.State, Source: domainSuspension.Source, CandidateReason: domainSuspension.CandidateReason,
			StartedAt: domainSuspension.StartedAt, EndedAt: domainSuspension.EndedAt, ReallowedByUserID: domainSuspension.ReallowedByUserID}}, nil
}

func (s *sqlExamAttemptStore) connect(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect) (examAttemptConnectOutcomeV1, error) {
	var zero examAttemptConnectOutcomeV1
	guard, err := s.lockExamAttemptEligibility(ctx, tx, input, false)
	if err != nil {
		return zero, err
	}

	var existing struct {
		AttemptID           string    `db:"attempt_id"`
		WorkspaceID         string    `db:"workspace_id"`
		AdmissionRevision   string    `db:"admission_revision_id"`
		State               string    `db:"state"`
		CreatedAt           time.Time `db:"created_at"`
		WorkspaceEntryCount int       `db:"workspace_entry_count"`
	}
	err = tx.Get(ctx, &existing, `SELECT a.id AS attempt_id,w.id AS workspace_id,a.admission_revision_id,a.state,a.created_at,
		(SELECT COUNT(*) FROM exam_attempt_workspace_entries entries WHERE entries.workspace_id=w.id) AS workspace_entry_count
		FROM exam_attempts a JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		WHERE a.exam_sitting_id=? AND a.candidate_user_id=? FOR UPDATE OF a,w`, input.SittingID.String(), input.CandidateUserID.String())
	if err == nil {
		return s.reconnect(ctx, tx, input, guard, existing.AttemptID, existing.WorkspaceID, existing.AdmissionRevision,
			existing.State, existing.CreatedAt, existing.WorkspaceEntryCount)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, fmt.Errorf("find existing Exam Attempt: %w", err)
	}
	return s.firstAdmission(ctx, tx, input, guard)
}

// lockExamAttemptEligibility serializes every admission for one candidate and
// locks current membership before the Exam/Sitting lineage. allowPaused is
// reserved for exact replay recovery; every fresh Connection still requires
// Open.
func (s *sqlExamAttemptStore) lockExamAttemptEligibility(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, allowPaused bool) (examAttemptAdmissionGuard, error) {
	var zero examAttemptAdmissionGuard
	lockKey := "proctor:exam-attempt-admission:" + input.SittingID.String() + ":" + input.CandidateUserID.String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, lockKey); err != nil {
		return zero, fmt.Errorf("lock Exam Attempt admission: %w", err)
	}

	var initial struct {
		ExamID   string `db:"exam_id"`
		PeriodID string `db:"academic_period_id"`
	}
	if err := tx.Get(ctx, &initial, `SELECT s.exam_id,c.academic_period_id FROM exam_sittings s
		JOIN classes c ON c.id=s.class_id WHERE s.id=?`, input.SittingID.String()); err != nil {
		return zero, translateError("exam_sitting", input.SittingID.String(), err)
	}
	if err := lockClassEnrollment(ctx, tx, input.CandidateUserID.String(), initial.PeriodID); err != nil {
		return zero, err
	}
	var lockedExamID string
	if err := tx.Get(ctx, &lockedExamID, `SELECT id FROM exams WHERE id=? AND archived_at IS NULL FOR SHARE`, initial.ExamID); err != nil {
		return zero, translateError("exam_sitting", input.SittingID.String(), err)
	}

	var guard examAttemptAdmissionGuard
	if err := tx.Get(ctx, &guard, `SELECT s.exam_id,s.exam_revision_id,s.class_id,c.academic_period_id,
		s.state,s.scheduled_end_at
		FROM exam_sittings s JOIN classes c ON c.id=s.class_id
		WHERE s.id=? AND s.exam_id=? FOR SHARE OF s,c`, input.SittingID.String(), initial.ExamID); err != nil {
		return zero, translateError("exam_sitting", input.SittingID.String(), err)
	}
	if guard.PeriodID != initial.PeriodID {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_membership", nil)
	}
	if guard.State != string(model.ExamSittingOpen) && !(allowPaused && guard.State == string(model.ExamSittingPaused)) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	var eligible bool
	if err := tx.Get(ctx, &eligible, `SELECT true FROM users u
		JOIN sessions se ON se.id=? AND se.user_id=u.id
		JOIN class_members cm ON cm.user_id=u.id AND cm.class_id=?
		JOIN classes c ON c.id=cm.class_id AND c.academic_period_id=cm.academic_period_id
		JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id
		JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id AND ap.institution_id=au.institution_id
		WHERE u.id=? AND u.archived_at IS NULL AND u.disabled_at IS NULL
		AND se.archived_at IS NULL AND se.revoked_at IS NULL
		AND cm.archived_at IS NULL
		AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL
		FOR SHARE OF u,se,cm,c,pl,p,au,ap`, input.SessionID.String(), guard.ClassID, input.CandidateUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
		}
		return zero, fmt.Errorf("validate Exam Attempt eligibility: %w", err)
	}
	if !eligible {
		return zero, store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
	}
	if err := tx.Get(ctx, &guard.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read Exam Attempt admission decision time: %w", err)
	}
	if !guard.DatabaseNow.Before(guard.ScheduledEnd) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	}
	if err := tx.Get(ctx, &eligible, `SELECT EXISTS (
		SELECT 1 FROM users u
		JOIN sessions se ON se.id=? AND se.user_id=u.id
		JOIN class_members cm ON cm.user_id=u.id AND cm.class_id=?
		JOIN classes c ON c.id=cm.class_id AND c.academic_period_id=cm.academic_period_id
		JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id
		JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id AND ap.institution_id=au.institution_id
		WHERE u.id=? AND u.archived_at IS NULL AND u.disabled_at IS NULL
		AND se.archived_at IS NULL AND se.revoked_at IS NULL AND se.idle_expires_at>? AND se.expires_at>?
		AND cm.archived_at IS NULL AND cm.start_at<=? AND (cm.end_at IS NULL OR cm.end_at>?)
		AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL)`, input.SessionID.String(), guard.ClassID,
		input.CandidateUserID.String(), guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("revalidate Exam Attempt eligibility at decision time: %w", err)
	}
	if !eligible {
		return zero, store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
	}
	return guard, nil
}

func (s *sqlExamAttemptStore) firstAdmission(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard) (examAttemptConnectOutcomeV1, error) {
	var zero examAttemptConnectOutcomeV1
	var starter []examAttemptStarterRow
	if err := tx.Select(ctx, &starter, `SELECT entry_id,kind,path,object_id,content_version,media_type,size_bytes,sha256
		FROM exam_revision_starter_workspace_entries WHERE exam_revision_id=? ORDER BY entry_id`, guard.RevisionID); err != nil {
		return zero, fmt.Errorf("load frozen Starter Workspace: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempts
		(id,exam_id,exam_sitting_id,candidate_user_id,admission_revision_id,state,created_at,updated_at,revision)
		VALUES (?,?,?,?,?,'active',?,?,1)`, input.AttemptID.String(), guard.ExamID, input.SittingID.String(),
		input.CandidateUserID.String(), guard.RevisionID, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("insert Exam Attempt: %w", translateError("exam_attempt", input.AttemptID.String(), err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspaces (id,exam_attempt_id,admission_revision_id,cursor,created_at,updated_at)
		VALUES (?,?,?,0,?,?)`, input.WorkspaceID.String(), input.AttemptID.String(), guard.RevisionID, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("insert Exam Attempt Workspace: %w", err)
	}

	for _, source := range starter {
		entryID := model.NewAttemptWorkspaceEntryID()
		var objectID model.AttemptWorkspaceObjectID
		if source.Kind == string(model.StarterWorkspaceEntryFile) {
			objectID = model.NewAttemptWorkspaceObjectID()
			if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_objects
				(id,workspace_id,admission_revision_id,source_starter_entry_id,storage_origin,starter_object_id,state,content_version,media_type,size_bytes,sha256,created_at,updated_at)
				VALUES (?,?,?,?,'starter',?,'current',?,?,?,?,?,?)`, objectID.String(), input.WorkspaceID.String(), guard.RevisionID, source.EntryID, source.ObjectID.String,
				source.ContentVersion.String, source.MediaType.String, source.SizeBytes.Int64, source.SHA256.String, guard.DatabaseNow, guard.DatabaseNow); err != nil {
				return zero, fmt.Errorf("insert Exam Attempt Workspace object: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_entries
			(id,workspace_id,admission_revision_id,source_starter_entry_id,kind,path,current_object_id,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, entryID.String(), input.WorkspaceID.String(), guard.RevisionID, source.EntryID,
			source.Kind, source.Path, nullableAttemptWorkspaceObjectID(objectID), guard.DatabaseNow, guard.DatabaseNow); err != nil {
			return zero, fmt.Errorf("insert Exam Attempt Workspace entry: %w", err)
		}
	}

	leaseExpires := guard.DatabaseNow.Add(model.AttemptParticipationInitialLease)
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_participations
		(id,exam_attempt_id,state,generation,renewal_sequence,continuity_credential_hash,started_at,updated_at,lease_expires_at)
		VALUES (?,?,'active',1,0,?,?,?,?)`, input.ParticipationID.String(), input.AttemptID.String(), input.ContinuityCredentialHash,
		guard.DatabaseNow, guard.DatabaseNow, leaseExpires); err != nil {
		return zero, fmt.Errorf("insert Attempt Participation: %w", translateError("attempt_participation", input.ParticipationID.String(), err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_connections
		(id,exam_attempt_id,participation_id,session_id,state,opened_at) VALUES (?,?,?,?,'open',?)`,
		input.ConnectionID.String(), input.AttemptID.String(), input.ParticipationID.String(), input.SessionID.String(), guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("insert Attempt Connection: %w", translateError("attempt_connection", input.ConnectionID.String(), err))
	}

	outcome := examAttemptConnectOutcomeV1{AttemptID: input.AttemptID.String(), WorkspaceID: input.WorkspaceID.String(),
		ParticipationID: input.ParticipationID.String(), ConnectionID: input.ConnectionID.String(), ExamID: guard.ExamID,
		SittingID: input.SittingID.String(), CandidateID: input.CandidateUserID.String(), RevisionID: guard.RevisionID,
		SessionID: input.SessionID.String(), ClassID: guard.ClassID, StartedAt: model.TimeUTC(guard.DatabaseNow), LeaseExpiresAt: model.TimeUTC(leaseExpires),
		EntryCount: len(starter), Generation: 1, FirstAdmission: true, ConnectionOpened: true}
	if err := completeExamAttemptConnectAudit(ctx, tx, outcome, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return outcome, nil
}

func (s *sqlExamAttemptStore) reconnect(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard,
	attemptID, workspaceID, admissionRevisionID, attemptState string, attemptCreatedAt time.Time, workspaceEntryCount int,
) (examAttemptConnectOutcomeV1, error) {
	var zero examAttemptConnectOutcomeV1
	if attemptState != string(model.ExamAttemptActive) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	var participation struct {
		ID             string    `db:"id"`
		CredentialHash string    `db:"continuity_credential_hash"`
		Generation     int64     `db:"generation"`
		LeaseExpiresAt time.Time `db:"lease_expires_at"`
	}
	if err := tx.Get(ctx, &participation, `SELECT id,continuity_credential_hash,generation,lease_expires_at
		FROM exam_attempt_participations WHERE exam_attempt_id=? AND state='active' FOR UPDATE`, attemptID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if err = tx.Get(ctx, &participation.Generation, `SELECT COALESCE(MAX(generation),0)+1
				FROM exam_attempt_participations WHERE exam_attempt_id=?`, attemptID); err != nil {
				return zero, fmt.Errorf("select next Attempt Participation generation: %w", err)
			}
			participation.ID = input.ParticipationID.String()
			participation.CredentialHash = input.ContinuityCredentialHash
			participation.LeaseExpiresAt = guard.DatabaseNow.Add(model.AttemptParticipationInitialLease)
			if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_participations
				(id,exam_attempt_id,state,generation,renewal_sequence,continuity_credential_hash,started_at,updated_at,lease_expires_at)
				VALUES (?,?,'active',?,0,?,?,?,?)`, participation.ID, attemptID, participation.Generation,
				participation.CredentialHash, guard.DatabaseNow, guard.DatabaseNow, participation.LeaseExpiresAt); err != nil {
				return zero, fmt.Errorf("insert next Attempt Participation: %w", translateError("attempt_participation", participation.ID, err))
			}
		} else {
			return zero, fmt.Errorf("lock active Attempt Participation: %w", err)
		}
	}
	if subtle.ConstantTimeCompare([]byte(participation.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if !guard.DatabaseNow.Before(participation.LeaseExpiresAt) {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}

	connectionID := input.ConnectionID.String()
	connectionOpened := false
	var open struct {
		ID              string `db:"id"`
		ParticipationID string `db:"participation_id"`
		SessionID       string `db:"session_id"`
	}
	err := tx.Get(ctx, &open, `SELECT id,participation_id,session_id FROM exam_attempt_connections
		WHERE exam_attempt_id=? AND state='open' FOR UPDATE`, attemptID)
	switch {
	case err == nil && open.ParticipationID == participation.ID && open.SessionID == input.SessionID.String():
		connectionID = open.ID
	case err == nil:
		return zero, store.NewErrConflict("attempt_connection", "attempt_connection_open", nil)
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_connections
			(id,exam_attempt_id,participation_id,session_id,state,opened_at) VALUES (?,?,?,?,'open',?)`,
			connectionID, attemptID, participation.ID, input.SessionID.String(), guard.DatabaseNow); err != nil {
			return zero, fmt.Errorf("insert reconnect Attempt Connection: %w", translateError("attempt_connection", connectionID, err))
		}
		connectionOpened = true
	case err != nil:
		return zero, fmt.Errorf("lock open Attempt Connection: %w", err)
	}

	outcome := examAttemptConnectOutcomeV1{AttemptID: attemptID, WorkspaceID: workspaceID, ParticipationID: participation.ID,
		ConnectionID: connectionID, ExamID: guard.ExamID, SittingID: input.SittingID.String(), CandidateID: input.CandidateUserID.String(),
		RevisionID: admissionRevisionID, SessionID: input.SessionID.String(), ClassID: guard.ClassID, StartedAt: model.TimeUTC(attemptCreatedAt),
		LeaseExpiresAt: model.TimeUTC(participation.LeaseExpiresAt), EntryCount: workspaceEntryCount,
		Generation: participation.Generation, FirstAdmission: false, ConnectionOpened: connectionOpened}
	if err = completeExamAttemptConnectAudit(ctx, tx, outcome, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return outcome, nil
}

func nullableAttemptWorkspaceObjectID(id model.AttemptWorkspaceObjectID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}

func (s *sqlExamAttemptStore) examAttemptConnectResult(ctx context.Context, outcome examAttemptConnectOutcomeV1) (*store.ExamAttemptConnectResult, error) {
	attemptID, err := model.ParseExamAttemptID(outcome.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(outcome.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(outcome.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(outcome.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(outcome.RevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(outcome.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(outcome.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "id", err)
	}
	connectionID, err := model.ParseAttemptConnectionID(outcome.ConnectionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "id", err)
	}
	attempt, err := model.NewExamAttempt(attemptID, examID, sittingID, candidateID, revisionID, outcome.StartedAt)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "outcome", err)
	}
	workspace, err := model.NewExamAttemptWorkspace(workspaceID, attemptID, outcome.StartedAt)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "outcome", err)
	}
	current, err := s.Get(ctx, examID, attemptID)
	if err != nil {
		return nil, err
	}
	attempt, workspace = current.Attempt, current.Workspace
	var participationRow struct {
		State           string         `db:"state"`
		Generation      int64          `db:"generation"`
		RenewalSequence int64          `db:"renewal_sequence"`
		StartedAt       time.Time      `db:"started_at"`
		UpdatedAt       time.Time      `db:"updated_at"`
		LeaseExpiresAt  time.Time      `db:"lease_expires_at"`
		EndedAt         sql.NullTime   `db:"ended_at"`
		EndReason       sql.NullString `db:"end_reason"`
		LeaseCurrent    bool           `db:"lease_current"`
	}
	if err = s.GetMaster().Get(ctx, &participationRow, `SELECT state,generation,renewal_sequence,started_at,updated_at,lease_expires_at,ended_at,end_reason,
		lease_expires_at>statement_timestamp() AS lease_current
		FROM exam_attempt_participations WHERE id=? AND exam_attempt_id=?`, participationID.String(), attemptID.String()); err != nil {
		return nil, translateError("attempt_participation", participationID.String(), err)
	}
	if !participationRow.LeaseCurrent {
		return nil, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	participation := &store.ExamAttemptParticipationView{ID: participationID, AttemptID: attemptID,
		State: model.AttemptParticipationState(participationRow.State), Generation: participationRow.Generation,
		RenewalSequence: participationRow.RenewalSequence, StartedAt: model.TimeUTC(participationRow.StartedAt),
		UpdatedAt: model.TimeUTC(participationRow.UpdatedAt), LeaseExpiresAt: model.TimeUTC(participationRow.LeaseExpiresAt),
		EndedAt: OptionalTimeFromNullTime(participationRow.EndedAt), EndReason: model.AttemptParticipationEndReason(participationRow.EndReason.String)}
	var connectionRow attemptConnectionRow
	if err = s.GetMaster().Get(ctx, &connectionRow, `SELECT id,exam_attempt_id,participation_id,session_id,state,opened_at,closed_at,close_reason
		FROM exam_attempt_connections WHERE id=?`, connectionID.String()); err != nil {
		return nil, translateError("attempt_connection", connectionID.String(), err)
	}
	connection, err := connectionRow.model()
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(outcome.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "class_id", err)
	}
	return &store.ExamAttemptConnectResult{Attempt: attempt, Workspace: workspace, Participation: participation,
		Connection: connection, ClassID: classID, FirstAdmission: outcome.FirstAdmission}, nil
}

func completeExamAttemptConnectAudit(ctx context.Context, tx *sqlxTxWrapper, outcome examAttemptConnectOutcomeV1,
	replayed bool, originalAuditID, auditID string, auditAt int64,
) error {
	data := map[string]any{"exam_id": outcome.ExamID, "exam_sitting_id": outcome.SittingID,
		"exam_attempt_id": outcome.AttemptID, "admission_revision_id": outcome.RevisionID,
		"participation_id": outcome.ParticipationID, "generation": outcome.Generation, "connection_id": outcome.ConnectionID,
		"workspace_entry_count": outcome.EntryCount, "first_admission": outcome.FirstAdmission,
		"connection_opened": outcome.ConnectionOpened && !replayed}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Attempt connect audit: %w", err)
	}
	return nil
}

func (s *sqlExamAttemptStore) CloseConnection(ctx context.Context, input *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	if input == nil || !input.ConnectionID.IsValid() || !input.CandidateUserID.IsValid() || !input.SessionID.IsValid() || !input.Reason.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("attempt_connection", "close", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "close Attempt Connection", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptConnectionCloseResult, error) {
		row, attemptID, sittingID, candidateID, err := lockAttemptConnection(ctx, tx, input.ConnectionID)
		if err != nil {
			return nil, err
		}
		if candidateID != input.CandidateUserID || row.SessionID != input.SessionID.String() {
			return nil, store.NewErrNotFound("attempt_connection", input.ConnectionID.String())
		}
		changed := row.State == string(model.AttemptConnectionOpen)
		if changed {
			var now time.Time
			if err = tx.Get(ctx, &now, `SELECT statement_timestamp()`); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state='closed',closed_at=?,close_reason=?
				WHERE id=? AND state='open'`, now, string(input.Reason), input.ConnectionID.String()); err != nil {
				return nil, fmt.Errorf("close Attempt Connection: %w", err)
			}
			row.State, row.ClosedAt, row.CloseReason = string(model.AttemptConnectionClosed), sql.NullTime{Time: now, Valid: true}, sql.NullString{String: string(input.Reason), Valid: true}
		}
		connection, err := row.model()
		if err != nil {
			return nil, err
		}
		data, err := model.EncodeAuditData(map[string]any{"exam_sitting_id": sittingID.String(), "exam_attempt_id": attemptID.String(),
			"connection_id": connection.ID.String(), "state": connection.State, "close_reason": connection.CloseReason, "changed": changed})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete Attempt Connection close audit: %w", err)
		}
		return &store.ExamAttemptConnectionCloseResult{AttemptID: attemptID, SittingID: sittingID,
			CandidateUserID: candidateID, Connection: connection, Changed: changed}, nil
	})
}

type attemptConnectionRow struct {
	ID              string         `db:"id"`
	AttemptID       string         `db:"exam_attempt_id"`
	ParticipationID string         `db:"participation_id"`
	SessionID       string         `db:"session_id"`
	State           string         `db:"state"`
	OpenedAt        time.Time      `db:"opened_at"`
	ClosedAt        sql.NullTime   `db:"closed_at"`
	CloseReason     sql.NullString `db:"close_reason"`
}

func (row attemptConnectionRow) model() (*model.AttemptConnection, error) {
	id, err := model.ParseAttemptConnectionID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "exam_attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "participation_id", err)
	}
	sessionID, err := model.ParseSessionID(row.SessionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_connection", "session_id", err)
	}
	value := &model.AttemptConnection{ID: id, AttemptID: attemptID, ParticipationID: participationID, SessionID: sessionID,
		State: model.AttemptConnectionState(row.State), OpenedAt: model.TimeUTC(row.OpenedAt), ClosedAt: OptionalTimeFromNullTime(row.ClosedAt)}
	if row.CloseReason.Valid {
		value.CloseReason = model.AttemptConnectionCloseReason(row.CloseReason.String)
	}
	if err := value.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_connection", "value", err)
	}
	return value, nil
}

func lockAttemptConnection(ctx context.Context, tx *sqlxTxWrapper, id model.AttemptConnectionID) (attemptConnectionRow, model.ExamAttemptID, model.ExamSittingID, model.UserID, error) {
	var joined struct {
		attemptConnectionRow
		SittingID   string `db:"exam_sitting_id"`
		CandidateID string `db:"candidate_user_id"`
	}
	err := tx.Get(ctx, &joined, `SELECT c.id,c.exam_attempt_id,c.participation_id,c.session_id,c.state,c.opened_at,c.closed_at,c.close_reason,
		a.exam_sitting_id,a.candidate_user_id FROM exam_attempt_connections c JOIN exam_attempts a ON a.id=c.exam_attempt_id
		WHERE c.id=? FOR UPDATE OF c`, id.String())
	if err != nil {
		return attemptConnectionRow{}, "", "", "", translateError("attempt_connection", id.String(), err)
	}
	attemptID, err := model.ParseExamAttemptID(joined.AttemptID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", invalidPersistedState("attempt_connection", "exam_attempt_id", err)
	}
	sittingID, err := model.ParseExamSittingID(joined.SittingID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", invalidPersistedState("attempt_connection", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(joined.CandidateID)
	if err != nil {
		return attemptConnectionRow{}, "", "", "", invalidPersistedState("attempt_connection", "candidate_user_id", err)
	}
	return joined.attemptConnectionRow, attemptID, sittingID, candidateID, nil
}

type examAttemptManagerRow struct {
	AttemptID                 string         `db:"attempt_id"`
	ExamID                    string         `db:"exam_id"`
	SittingID                 string         `db:"exam_sitting_id"`
	CandidateID               string         `db:"candidate_user_id"`
	AdmissionRevisionID       string         `db:"admission_revision_id"`
	AttemptState              string         `db:"attempt_state"`
	AttemptCreatedAt          time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt          time.Time      `db:"attempt_updated_at"`
	SubmittedAt               sql.NullTime   `db:"submitted_at"`
	AttemptRevision           int64          `db:"attempt_revision"`
	WorkspaceID               string         `db:"workspace_id"`
	WorkspaceCursor           int64          `db:"workspace_cursor"`
	WorkspaceCreatedAt        time.Time      `db:"workspace_created_at"`
	WorkspaceUpdatedAt        time.Time      `db:"workspace_updated_at"`
	ParticipationID           sql.NullString `db:"participation_id"`
	ParticipationState        sql.NullString `db:"participation_state"`
	Generation                sql.NullInt64  `db:"generation"`
	RenewalSequence           sql.NullInt64  `db:"renewal_sequence"`
	StartedAt                 sql.NullTime   `db:"started_at"`
	ParticipationUpdatedAt    sql.NullTime   `db:"participation_updated_at"`
	LeaseExpiresAt            sql.NullTime   `db:"lease_expires_at"`
	EndedAt                   sql.NullTime   `db:"ended_at"`
	EndReason                 sql.NullString `db:"end_reason"`
	ConnectionID              sql.NullString `db:"connection_id"`
	ConnectionState           sql.NullString `db:"connection_state"`
	OpenedAt                  sql.NullTime   `db:"opened_at"`
	ClosedAt                  sql.NullTime   `db:"closed_at"`
	CloseReason               sql.NullString `db:"close_reason"`
	SuspensionID              sql.NullString `db:"suspension_id"`
	SuspensionParticipationID sql.NullString `db:"suspension_participation_id"`
	SuspensionFlagID          sql.NullString `db:"suspension_flag_id"`
	SuspensionGeneration      sql.NullInt64  `db:"suspension_generation"`
	SuspensionState           sql.NullString `db:"suspension_state"`
	SuspensionSource          sql.NullString `db:"suspension_source"`
	SuspensionReason          sql.NullString `db:"suspension_reason"`
	SuspensionStartedAt       sql.NullTime   `db:"suspension_started_at"`
	SuspensionEndedAt         sql.NullTime   `db:"suspension_ended_at"`
	SuspensionReallowedBy     sql.NullString `db:"suspension_reallowed_by"`
}

const examAttemptManagerSelect = `SELECT a.id AS attempt_id,a.exam_id,a.exam_sitting_id,a.candidate_user_id,a.admission_revision_id,
	a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
	w.id AS workspace_id,w.cursor AS workspace_cursor,w.created_at AS workspace_created_at,w.updated_at AS workspace_updated_at,
	p.id AS participation_id,p.state AS participation_state,p.generation,p.renewal_sequence,p.started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,p.ended_at,p.end_reason,
	c.id AS connection_id,c.state AS connection_state,c.opened_at,c.closed_at,c.close_reason,
	su.id AS suspension_id,su.participation_id AS suspension_participation_id,su.integrity_flag_id AS suspension_flag_id,
	su.generation AS suspension_generation,su.state AS suspension_state,su.source AS suspension_source,
	su.candidate_reason AS suspension_reason,su.started_at AS suspension_started_at,su.ended_at AS suspension_ended_at,
	su.reallowed_by_user_id AS suspension_reallowed_by
	FROM exam_attempts a JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
	LEFT JOIN LATERAL (SELECT id,state,generation,renewal_sequence,started_at,updated_at,lease_expires_at,ended_at,end_reason
		FROM exam_attempt_participations WHERE exam_attempt_id=a.id ORDER BY generation DESC LIMIT 1) p ON true
	LEFT JOIN LATERAL (SELECT id,state,opened_at,closed_at,close_reason FROM exam_attempt_connections
		WHERE exam_attempt_id=a.id AND state='open' ORDER BY opened_at DESC,id DESC LIMIT 1) c ON true
	LEFT JOIN LATERAL (SELECT id,participation_id,integrity_flag_id,generation,state,source,candidate_reason,started_at,ended_at,reallowed_by_user_id
		FROM exam_attempt_suspensions WHERE exam_attempt_id=a.id AND state='active' LIMIT 1) su ON true`

func (s *sqlExamAttemptStore) Get(ctx context.Context, examID model.ExamID, attemptID model.ExamAttemptID) (*store.ExamAttemptManagerSnapshot, error) {
	if !examID.IsValid() || !attemptID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_attempt", "identity", nil)
	}
	var row examAttemptManagerRow
	if err := s.GetMaster().Get(ctx, &row, examAttemptManagerSelect+` WHERE a.exam_id=? AND a.id=?`, examID.String(), attemptID.String()); err != nil {
		return nil, translateError("exam_attempt", attemptID.String(), err)
	}
	return row.snapshot()
}

func (s *sqlExamAttemptStore) List(ctx context.Context, options store.ExamAttemptManagerListOptions) ([]store.ExamAttemptManagerSnapshot, error) {
	if !options.ExamID.IsValid() || !options.SittingID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		(options.BeforeCreatedAt.IsZero() != options.BeforeAttemptID.IsZero()) {
		return nil, store.NewErrInvalidInput("exam_attempt", "manager_list", nil)
	}
	query := examAttemptManagerSelect + ` WHERE a.exam_id=? AND a.exam_sitting_id=?`
	args := []any{options.ExamID.String(), options.SittingID.String()}
	if len(options.States) != 0 {
		states := make([]string, len(options.States))
		for i, state := range options.States {
			if state != model.ExamAttemptActive && state != model.ExamAttemptSuspended && state != model.ExamAttemptSubmitted {
				return nil, store.NewErrInvalidInput("exam_attempt", "states", nil)
			}
			states[i] = string(state)
		}
		query += ` AND a.state=ANY(?)`
		args = append(args, pq.Array(states))
	}
	if !options.BeforeCreatedAt.IsZero() {
		query += ` AND (a.created_at,a.id)<(?,?)`
		args = append(args, model.TimeUTC(options.BeforeCreatedAt), options.BeforeAttemptID.String())
	}
	query += ` ORDER BY a.created_at DESC,a.id DESC LIMIT ?`
	args = append(args, options.Limit)
	var rows []examAttemptManagerRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Attempts: %w", err)
	}
	result := make([]store.ExamAttemptManagerSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		result = append(result, *snapshot)
	}
	return result, nil
}

func (row examAttemptManagerRow) snapshot() (*store.ExamAttemptManagerSnapshot, error) {
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID, AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt), UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "value", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "id", err)
	}
	workspace := &model.ExamAttemptWorkspace{ID: workspaceID, AttemptID: attemptID, Cursor: row.WorkspaceCursor, CreatedAt: model.TimeUTC(row.WorkspaceCreatedAt), UpdatedAt: model.TimeUTC(row.WorkspaceUpdatedAt)}
	if err = workspace.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "value", err)
	}
	result := &store.ExamAttemptManagerSnapshot{Attempt: attempt, Workspace: workspace}
	if row.ParticipationID.Valid {
		id, e := model.ParseAttemptParticipationID(row.ParticipationID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_participation", "id", e)
		}
		result.LatestParticipation = &store.ExamAttemptParticipationView{ID: id, AttemptID: attemptID, State: model.AttemptParticipationState(row.ParticipationState.String), Generation: row.Generation.Int64, RenewalSequence: row.RenewalSequence.Int64, StartedAt: model.TimeUTC(row.StartedAt.Time), UpdatedAt: model.TimeUTC(row.ParticipationUpdatedAt.Time), LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt.Time), EndedAt: OptionalTimeFromNullTime(row.EndedAt), EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	}
	if row.ConnectionID.Valid {
		id, e := model.ParseAttemptConnectionID(row.ConnectionID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_connection", "id", e)
		}
		result.CurrentConnection = &store.ExamAttemptManagerConnection{ID: id, State: model.AttemptConnectionState(row.ConnectionState.String), OpenedAt: model.TimeUTC(row.OpenedAt.Time), ClosedAt: OptionalTimeFromNullTime(row.ClosedAt), CloseReason: model.AttemptConnectionCloseReason(row.CloseReason.String)}
	}
	if row.SuspensionID.Valid {
		id, e := model.ParseAttemptSuspensionID(row.SuspensionID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "id", e)
		}
		participationID, e := model.ParseAttemptParticipationID(row.SuspensionParticipationID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "participation_id", e)
		}
		flagID, e := model.ParseIntegrityFlagID(row.SuspensionFlagID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "integrity_flag_id", e)
		}
		var actorID model.UserID
		if row.SuspensionReallowedBy.Valid {
			actorID, e = model.ParseUserID(row.SuspensionReallowedBy.String)
			if e != nil {
				return nil, invalidPersistedState("attempt_suspension", "reallowed_by_user_id", e)
			}
		}
		domainSuspension, e := model.NewPolicyAttemptSuspension(id, attemptID, participationID, flagID,
			row.SuspensionGeneration.Int64, model.AttemptSuspensionCandidateReason(row.SuspensionReason.String), row.SuspensionStartedAt.Time)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "value", e)
		}
		if model.AttemptSuspensionState(row.SuspensionState.String) != model.AttemptSuspensionActive ||
			row.SuspensionEndedAt.Valid || !actorID.IsZero() {
			return nil, invalidPersistedState("attempt_suspension", "value", errors.New("invalid active Attempt Suspension projection"))
		}
		result.ActiveSuspension = &store.ExamAttemptSuspensionView{ID: domainSuspension.ID, AttemptID: domainSuspension.AttemptID,
			ParticipationID: domainSuspension.ParticipationID, FlagID: domainSuspension.FlagID, Generation: domainSuspension.Generation,
			State: domainSuspension.State, Source: domainSuspension.Source, CandidateReason: domainSuspension.CandidateReason,
			StartedAt: domainSuspension.StartedAt}
	}
	return result, nil
}

type candidateAttemptGuard struct {
	AttemptID           string `db:"attempt_id"`
	SittingID           string `db:"sitting_id"`
	AdmissionRevisionID string `db:"admission_revision_id"`
	RevisionID          string `db:"revision_id"`
}

func (s *sqlExamAttemptStore) candidateGuard(ctx context.Context, access store.CandidateAttemptAccess) (candidateAttemptGuard, error) {
	if !access.AttemptID.IsValid() || !access.CandidateUserID.IsValid() || !access.SessionID.IsValid() || !access.ConnectionID.IsValid() || !model.IsValidTokenHash(access.ContinuityCredentialHash) {
		return candidateAttemptGuard{}, store.NewErrInvalidInput("exam_attempt", "candidate_access", nil)
	}
	var guard candidateAttemptGuard
	err := s.GetMaster().Get(ctx, &guard, `SELECT a.id AS attempt_id,a.exam_sitting_id AS sitting_id,a.admission_revision_id,s.exam_revision_id AS revision_id
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id AND p.state='active'
		JOIN exam_attempt_connections c ON c.participation_id=p.id AND c.exam_attempt_id=a.id AND c.state='open'
		WHERE a.id=? AND a.candidate_user_id=? AND a.state='active' AND c.id=? AND c.session_id=?
		AND p.continuity_credential_hash=? AND p.lease_expires_at>statement_timestamp()
		AND s.state IN ('open','paused') AND s.scheduled_end_at>statement_timestamp()`, access.AttemptID.String(), access.CandidateUserID.String(),
		access.ConnectionID.String(), access.SessionID.String(), access.ContinuityCredentialHash)
	if errors.Is(err, sql.ErrNoRows) {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	if err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("authorize candidate Exam Attempt access: %w", err)
	}
	return guard, nil
}

func (s *sqlExamAttemptStore) lockCandidateGuard(ctx context.Context, tx *sqlxTxWrapper, access store.CandidateAttemptAccess) (candidateAttemptGuard, error) {
	if !access.AttemptID.IsValid() || !access.CandidateUserID.IsValid() || !access.SessionID.IsValid() ||
		!access.ConnectionID.IsValid() || !model.IsValidTokenHash(access.ContinuityCredentialHash) {
		return candidateAttemptGuard{}, store.NewErrInvalidInput("exam_attempt", "candidate_access", nil)
	}
	var row struct {
		candidateAttemptGuard
		AttemptState       string       `db:"attempt_state"`
		SittingState       string       `db:"sitting_state"`
		ScheduledEndAt     time.Time    `db:"scheduled_end_at"`
		ParticipationState string       `db:"participation_state"`
		CredentialHash     string       `db:"continuity_credential_hash"`
		LeaseExpiresAt     time.Time    `db:"lease_expires_at"`
		ConnectionState    string       `db:"connection_state"`
		SessionArchivedAt  sql.NullTime `db:"session_archived_at"`
		SessionRevokedAt   sql.NullTime `db:"session_revoked_at"`
		SessionIdleExpiry  time.Time    `db:"session_idle_expires_at"`
		SessionExpiry      time.Time    `db:"session_expires_at"`
		UserArchivedAt     sql.NullTime `db:"user_archived_at"`
		UserDisabledAt     sql.NullTime `db:"user_disabled_at"`
	}
	err := tx.Get(ctx, &row, `SELECT a.id AS attempt_id,a.exam_sitting_id AS sitting_id,
		a.admission_revision_id,s.exam_revision_id AS revision_id,a.state AS attempt_state,
		s.state AS sitting_state,s.scheduled_end_at,p.state AS participation_state,
		p.continuity_credential_hash,p.lease_expires_at,c.state AS connection_state,
		se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
		se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
		u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id
		JOIN exam_attempt_connections c ON c.participation_id=p.id AND c.exam_attempt_id=a.id
		JOIN sessions se ON se.id=c.session_id JOIN users u ON u.id=se.user_id
		WHERE a.id=? AND a.candidate_user_id=? AND c.id=? AND c.session_id=?
		FOR SHARE OF a,s,p,c,se,u`, access.AttemptID.String(), access.CandidateUserID.String(),
		access.ConnectionID.String(), access.SessionID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	if err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("lock candidate Exam Attempt access: %w", err)
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("read candidate Exam Attempt access time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	if row.AttemptState != string(model.ExamAttemptActive) || row.ParticipationState != string(model.AttemptParticipationActive) ||
		row.ConnectionState != string(model.AttemptConnectionOpen) ||
		subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(access.ContinuityCredentialHash)) != 1 ||
		!databaseNow.Before(row.LeaseExpiresAt) ||
		(row.SittingState != string(model.ExamSittingOpen) && row.SittingState != string(model.ExamSittingPaused)) ||
		!databaseNow.Before(row.ScheduledEndAt) || row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid ||
		!databaseNow.Before(row.SessionIdleExpiry) || !databaseNow.Before(row.SessionExpiry) ||
		row.UserArchivedAt.Valid || row.UserDisabledAt.Valid {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	return row.candidateAttemptGuard, nil
}

func (s *sqlExamAttemptStore) GetCandidatePresentation(ctx context.Context, access store.CandidateAttemptAccess) (*store.CandidateExamPresentation, error) {
	guard, err := s.candidateGuard(ctx, access)
	if err != nil {
		return nil, err
	}
	var header struct {
		Title        string `db:"title"`
		Instructions string `db:"instructions_markdown"`
	}
	if err = s.GetMaster().Get(ctx, &header, `SELECT title,instructions_markdown FROM exam_revisions WHERE id=? AND sealed=true`, guard.RevisionID); err != nil {
		return nil, translateError("exam_revision", guard.RevisionID, err)
	}
	var rows []struct {
		ResourceID  string `db:"resource_id"`
		DisplayName string `db:"display_name"`
		Description string `db:"description"`
		MediaType   string `db:"media_type"`
		SHA256      string `db:"sha256"`
		Position    int    `db:"position"`
		SizeBytes   int64  `db:"size_bytes"`
	}
	if err = s.GetMaster().Select(ctx, &rows, `SELECT resource_id,display_name,description_markdown AS description,position,media_type,size_bytes,sha256
		FROM exam_revision_resources WHERE exam_revision_id=? ORDER BY position`, guard.RevisionID); err != nil {
		return nil, fmt.Errorf("list candidate Exam Resources: %w", err)
	}
	attemptID, parseErr := model.ParseExamAttemptID(guard.AttemptID)
	if parseErr != nil {
		return nil, invalidPersistedState("exam_attempt", "id", parseErr)
	}
	sittingID, parseErr := model.ParseExamSittingID(guard.SittingID)
	if parseErr != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", parseErr)
	}
	admissionRevisionID, parseErr := model.ParseExamRevisionID(guard.AdmissionRevisionID)
	if parseErr != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", parseErr)
	}
	revisionID, parseErr := model.ParseExamRevisionID(guard.RevisionID)
	if parseErr != nil {
		return nil, invalidPersistedState("exam_sitting", "exam_revision_id", parseErr)
	}
	result := &store.CandidateExamPresentation{AttemptID: attemptID, SittingID: sittingID, AdmissionRevisionID: admissionRevisionID, CurrentRevisionID: revisionID,
		Title: header.Title, InstructionsMarkdown: header.Instructions, Resources: make([]store.CandidateExamResource, 0, len(rows))}
	for _, row := range rows {
		resourceID, parseErr := model.ParseExamResourceID(row.ResourceID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_revision_resource", "resource_id", parseErr)
		}
		result.Resources = append(result.Resources, store.CandidateExamResource{ResourceID: resourceID, DisplayName: row.DisplayName, DescriptionMarkdown: row.Description, Position: row.Position, MediaType: model.ExamResourceMediaType(row.MediaType), SizeBytes: row.SizeBytes, SHA256: row.SHA256})
	}
	return result, nil
}

func (s *sqlExamAttemptStore) listCandidateWorkspace(ctx context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	firstPage := options.ExpectedCursor == -1 && options.AfterEntryID.IsZero()
	continuation := options.ExpectedCursor >= 0 && options.AfterEntryID.IsValid()
	if options.Limit < 1 || options.Limit > model.AttemptWorkspaceJournalReadMaximum || (!firstPage && !continuation) {
		return nil, store.NewErrInvalidInput("attempt_workspace", "list_options", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list candidate Attempt Workspace", func(ctx context.Context, tx *sqlxTxWrapper) (*store.CandidateAttemptWorkspacePage, error) {
		if _, err := s.lockCandidateGuard(ctx, tx, options.Access); err != nil {
			return nil, err
		}
		return listCandidateWorkspaceSnapshot(ctx, tx, options, continuation)
	})
}

func listCandidateWorkspaceSnapshot(ctx context.Context, tx *sqlxTxWrapper, options store.CandidateWorkspaceListOptions, continuation bool) (*store.CandidateAttemptWorkspacePage, error) {
	var workspace struct {
		ID     string `db:"id"`
		Cursor int64  `db:"cursor"`
	}
	if err := tx.Get(ctx, &workspace, `SELECT id,cursor FROM exam_attempt_workspaces WHERE exam_attempt_id=? FOR SHARE`,
		options.Access.AttemptID.String()); err != nil {
		return nil, translateError("attempt_workspace", options.Access.AttemptID.String(), err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(workspace.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace", "id", err)
	}
	if continuation && options.ExpectedCursor != workspace.Cursor {
		return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, RefreshRequired: true}, nil
	}
	var afterPath string
	if continuation {
		if err = tx.Get(ctx, &afterPath, `SELECT path FROM exam_attempt_workspace_entries WHERE workspace_id=? AND id=?`,
			workspace.ID, options.AfterEntryID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, RefreshRequired: true}, nil
			}
			return nil, fmt.Errorf("resolve Attempt Workspace manifest boundary: %w", err)
		}
	}
	var rows []struct {
		EntryID        string         `db:"entry_id"`
		Kind           string         `db:"kind"`
		Path           string         `db:"path"`
		ContentVersion sql.NullString `db:"content_version"`
		MediaType      sql.NullString `db:"media_type"`
		SizeBytes      sql.NullInt64  `db:"size_bytes"`
		SHA256         sql.NullString `db:"sha256"`
	}
	query := `SELECT e.id AS entry_id,e.kind,e.path,o.content_version,o.media_type,o.size_bytes,o.sha256
		FROM exam_attempt_workspaces w JOIN exam_attempt_workspace_entries e ON e.workspace_id=w.id
		LEFT JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id
		WHERE w.exam_attempt_id=?`
	args := []any{options.Access.AttemptID.String()}
	if continuation {
		query += ` AND (e.path,e.id)>(?,?)`
		args = append(args, afterPath, options.AfterEntryID.String())
	}
	query += ` ORDER BY e.path,e.id LIMIT ?`
	args = append(args, options.Limit+1)
	if err := tx.Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list candidate Attempt Workspace: %w", err)
	}
	hasMore := len(rows) > options.Limit
	if hasMore {
		rows = rows[:options.Limit]
	}
	result := make([]store.CandidateAttemptWorkspaceItem, 0, len(rows))
	for _, row := range rows {
		id, err := model.ParseAttemptWorkspaceEntryID(row.EntryID)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_entry", "id", err)
		}
		var version model.WorkspaceContentVersion
		if row.ContentVersion.Valid {
			version, err = model.ParseWorkspaceContentVersion(row.ContentVersion.String)
			if err != nil {
				return nil, invalidPersistedState("attempt_workspace_object", "content_version", err)
			}
		}
		result = append(result, store.CandidateAttemptWorkspaceItem{EntryID: id, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path, ContentVersion: version, MediaType: row.MediaType.String, SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String})
	}
	return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, Items: result, HasMore: hasMore}, nil
}

func (s *sqlExamAttemptStore) ResolveCandidateResource(ctx context.Context, access store.CandidateAttemptAccess, resourceID model.ExamResourceID) (*store.CandidateResourceContent, error) {
	guard, err := s.candidateGuard(ctx, access)
	if err != nil {
		return nil, err
	}
	if !resourceID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_resource", "id", nil)
	}
	var row struct {
		ResourceID     string `db:"resource_id"`
		DisplayName    string `db:"display_name"`
		Description    string `db:"description"`
		MediaType      string `db:"media_type"`
		SHA256         string `db:"sha256"`
		FileRevisionID string `db:"file_revision_id"`
		RenditionID    string `db:"rendition_id"`
		Position       int    `db:"position"`
		SizeBytes      int64  `db:"size_bytes"`
	}
	err = s.GetMaster().Get(ctx, &row, `SELECT resource_id,display_name,description_markdown AS description,position,media_type,size_bytes,sha256,file_revision_id,rendition_id
		FROM exam_revision_resources WHERE exam_revision_id=? AND resource_id=?`, guard.RevisionID, resourceID.String())
	if err != nil {
		return nil, translateError("exam_resource", resourceID.String(), err)
	}
	fileRevisionID, err := model.ParseFileRevisionID(row.FileRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_resource", "file_revision_id", err)
	}
	renditionID, err := model.ParseFileRenditionID(row.RenditionID)
	if err != nil {
		return nil, invalidPersistedState("exam_resource", "rendition_id", err)
	}
	return &store.CandidateResourceContent{Resource: store.CandidateExamResource{ResourceID: resourceID, DisplayName: row.DisplayName,
		DescriptionMarkdown: row.Description, Position: row.Position, MediaType: model.ExamResourceMediaType(row.MediaType), SizeBytes: row.SizeBytes, SHA256: row.SHA256}, FileRevisionID: fileRevisionID, RenditionID: renditionID}, nil
}

func resolveCandidateWorkspaceFile(ctx context.Context, executor sqlxExecutor, access store.CandidateAttemptAccess, entryID model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	var row struct {
		EntryID   string         `db:"entry_id"`
		Kind      string         `db:"kind"`
		Path      string         `db:"path"`
		ObjectID  string         `db:"object_id"`
		Origin    string         `db:"origin"`
		Version   string         `db:"version"`
		MediaType string         `db:"media_type"`
		SHA256    string         `db:"sha256"`
		StarterID sql.NullString `db:"starter_id"`
		SizeBytes int64          `db:"size_bytes"`
	}
	err := executor.Get(ctx, &row, `SELECT e.id AS entry_id,e.kind,e.path,o.id AS object_id,o.storage_origin AS origin,
		o.starter_object_id AS starter_id,o.content_version AS version,o.media_type,o.size_bytes,o.sha256
		FROM exam_attempt_workspaces w JOIN exam_attempt_workspace_entries e ON e.workspace_id=w.id
		JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id
		WHERE w.exam_attempt_id=? AND e.id=? AND e.kind='file'`, access.AttemptID.String(), entryID.String())
	if err != nil {
		return nil, translateError("attempt_workspace_entry", entryID.String(), err)
	}
	objectID, err := model.ParseAttemptWorkspaceObjectID(row.ObjectID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "id", err)
	}
	version, err := model.ParseWorkspaceContentVersion(row.Version)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "content_version", err)
	}
	var starterID model.StarterWorkspaceObjectID
	if row.StarterID.Valid {
		starterID, err = model.ParseStarterWorkspaceObjectID(row.StarterID.String)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_object", "starter_object_id", err)
		}
	}
	return &store.CandidateWorkspaceContent{Entry: store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path,
		ContentVersion: version, MediaType: row.MediaType, SizeBytes: row.SizeBytes, SHA256: row.SHA256}, StorageOrigin: model.AttemptWorkspaceObjectStorage(row.Origin),
		StarterObjectID: starterID, AttemptObjectID: objectID, ContentVersion: version}, nil
}

var _ store.ExamAttemptStore = (*sqlExamAttemptStore)(nil)
