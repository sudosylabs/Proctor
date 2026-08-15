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
		s.state,s.scheduled_end_at,statement_timestamp() AS database_now
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
	if !guard.DatabaseNow.Before(guard.ScheduledEnd) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
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
		AND se.idle_expires_at>? AND se.expires_at>?
		AND cm.archived_at IS NULL AND cm.start_at<=? AND (cm.end_at IS NULL OR cm.end_at>?)
		AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL
		FOR SHARE OF u,se,cm,c,pl,p,au,ap`, input.SessionID.String(), guard.ClassID, input.CandidateUserID.String(),
		guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
		}
		return zero, fmt.Errorf("validate Exam Attempt eligibility: %w", err)
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
				(id,workspace_id,admission_revision_id,source_starter_entry_id,storage_origin,starter_object_id,content_version,media_type,size_bytes,sha256,created_at)
				VALUES (?,?,?,?,'starter',?,?,?,?,?,?)`, objectID.String(), input.WorkspaceID.String(), guard.RevisionID, source.EntryID, source.ObjectID.String,
				source.ContentVersion.String, source.MediaType.String, source.SizeBytes.Int64, source.SHA256.String, guard.DatabaseNow); err != nil {
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
			return zero, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
		}
		return zero, fmt.Errorf("lock active Attempt Participation: %w", err)
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
	AttemptID              string         `db:"attempt_id"`
	ExamID                 string         `db:"exam_id"`
	SittingID              string         `db:"exam_sitting_id"`
	CandidateID            string         `db:"candidate_user_id"`
	AdmissionRevisionID    string         `db:"admission_revision_id"`
	AttemptState           string         `db:"attempt_state"`
	AttemptCreatedAt       time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt       time.Time      `db:"attempt_updated_at"`
	SubmittedAt            sql.NullTime   `db:"submitted_at"`
	AttemptRevision        int64          `db:"attempt_revision"`
	WorkspaceID            string         `db:"workspace_id"`
	WorkspaceCursor        int64          `db:"workspace_cursor"`
	WorkspaceCreatedAt     time.Time      `db:"workspace_created_at"`
	WorkspaceUpdatedAt     time.Time      `db:"workspace_updated_at"`
	ParticipationID        sql.NullString `db:"participation_id"`
	ParticipationState     sql.NullString `db:"participation_state"`
	Generation             sql.NullInt64  `db:"generation"`
	RenewalSequence        sql.NullInt64  `db:"renewal_sequence"`
	StartedAt              sql.NullTime   `db:"started_at"`
	ParticipationUpdatedAt sql.NullTime   `db:"participation_updated_at"`
	LeaseExpiresAt         sql.NullTime   `db:"lease_expires_at"`
	EndedAt                sql.NullTime   `db:"ended_at"`
	EndReason              sql.NullString `db:"end_reason"`
	ConnectionID           sql.NullString `db:"connection_id"`
	ConnectionState        sql.NullString `db:"connection_state"`
	OpenedAt               sql.NullTime   `db:"opened_at"`
	ClosedAt               sql.NullTime   `db:"closed_at"`
	CloseReason            sql.NullString `db:"close_reason"`
}

const examAttemptManagerSelect = `SELECT a.id AS attempt_id,a.exam_id,a.exam_sitting_id,a.candidate_user_id,a.admission_revision_id,
	a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
	w.id AS workspace_id,w.cursor AS workspace_cursor,w.created_at AS workspace_created_at,w.updated_at AS workspace_updated_at,
	p.id AS participation_id,p.state AS participation_state,p.generation,p.renewal_sequence,p.started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,p.ended_at,p.end_reason,
	c.id AS connection_id,c.state AS connection_state,c.opened_at,c.closed_at,c.close_reason
	FROM exam_attempts a JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
	LEFT JOIN LATERAL (SELECT id,state,generation,renewal_sequence,started_at,updated_at,lease_expires_at,ended_at,end_reason
		FROM exam_attempt_participations WHERE exam_attempt_id=a.id ORDER BY generation DESC LIMIT 1) p ON true
	LEFT JOIN LATERAL (SELECT id,state,opened_at,closed_at,close_reason FROM exam_attempt_connections
		WHERE exam_attempt_id=a.id AND state='open' ORDER BY opened_at DESC,id DESC LIMIT 1) c ON true`

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

func (s *sqlExamAttemptStore) ListCandidateWorkspace(ctx context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	if options.Limit < 1 || options.Limit > 200 || (options.AfterPath == "") != options.AfterEntryID.IsZero() {
		return nil, store.NewErrInvalidInput("attempt_workspace", "list_options", nil)
	}
	if options.AfterPath != "" {
		normalized, err := model.NormalizeStarterWorkspacePath(options.AfterPath)
		if err != nil || normalized != options.AfterPath {
			return nil, store.NewErrInvalidInput("attempt_workspace", "after_path", nil)
		}
	}
	if _, err := s.candidateGuard(ctx, options.Access); err != nil {
		return nil, err
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
	if options.AfterPath != "" {
		query += ` AND (e.path,e.id)>(?,?)`
		args = append(args, options.AfterPath, options.AfterEntryID.String())
	}
	query += ` ORDER BY e.path,e.id LIMIT ?`
	args = append(args, options.Limit+1)
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
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
	return &store.CandidateAttemptWorkspacePage{Items: result, HasMore: hasMore}, nil
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

func (s *sqlExamAttemptStore) ResolveCandidateWorkspaceFile(ctx context.Context, access store.CandidateAttemptAccess, entryID model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	if _, err := s.candidateGuard(ctx, access); err != nil {
		return nil, err
	}
	if !entryID.IsValid() {
		return nil, store.NewErrInvalidInput("attempt_workspace_entry", "id", nil)
	}
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
	err := s.GetMaster().Get(ctx, &row, `SELECT e.id AS entry_id,e.kind,e.path,o.id AS object_id,o.storage_origin AS origin,
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
