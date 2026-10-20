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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// examAttemptManualSuspensionOutcomeV1 retains only stable identities. The
// committed episode, ended Participation, and closed Connection are immutable,
// so replay rehydrates the original result from them.
type examAttemptManualSuspensionOutcomeV1 struct {
	ExamID, SittingID, ClassID, CandidateID, AttemptID, ParticipationID, SuspensionID string
	ConnectionID                                                                      string
	ConnectionClosed                                                                  bool
}

func (s *sqlExamAttemptStore) SuspendAttempt(ctx context.Context, input *store.ExamAttemptManualSuspension,
	command *store.CommandIdempotency,
) (*store.ExamAttemptManualSuspensionResult, error) {
	if input == nil || command == nil || command.Operation != store.ExamAttemptSuspendOperation || command.OutcomeVersion != 1 ||
		command.UserID != input.ActorUserID || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.AttemptID.IsValid() ||
		!input.SuspensionID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedAttemptRevision < 1 ||
		!model.IsValidAttemptSuspensionPrivateReason(input.PrivateReason) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_attempt", "manual_suspension", nil)
	}
	guard := &store.ExamAttemptReallow{ExamID: input.ExamID, SittingID: input.SittingID, AttemptID: input.AttemptID,
		ActorUserID: input.ActorUserID, ManagerOverride: input.ManagerOverride}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "suspend Exam Attempt", idempotentMutation[examAttemptManualSuspensionOutcomeV1]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examAttemptManualSuspensionOutcomeV1, error) {
			if err := guardExamAttemptReallowAuthority(ctx, tx, guard, true); err != nil {
				return examAttemptManualSuspensionOutcomeV1{}, err
			}
			return suspendExamAttemptByManager(ctx, tx, input)
		},
		encode: func(value examAttemptManualSuspensionOutcomeV1) ([]byte, error) { return encodeCommandOutcome(value) },
		decode: func(version int, encoded []byte) (examAttemptManualSuspensionOutcomeV1, error) {
			var value examAttemptManualSuspensionOutcomeV1
			if version != 1 {
				return value, fmt.Errorf("unsupported Exam Attempt manual suspension outcome version %d", version)
			}
			return value, decodeCommandOutcome(encoded, &value)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value examAttemptManualSuspensionOutcomeV1, originalAuditID string) error {
			if err := guardExamAttemptReallowAuthority(ctx, tx, guard, false); err != nil {
				return err
			}
			return completeExamAttemptManualSuspensionAudit(ctx, tx, value, true, originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	aggregate, err := s.loadExamAttemptManualSuspensionResult(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	aggregate.Replayed = result.Replayed
	return aggregate, nil
}

type manualSuspensionAttemptRow struct {
	ExamID              string       `db:"exam_id"`
	SittingID           string       `db:"exam_sitting_id"`
	ClassID             string       `db:"class_id"`
	CandidateID         string       `db:"candidate_user_id"`
	AdmissionRevisionID string       `db:"admission_revision_id"`
	State               string       `db:"state"`
	CreatedAt           time.Time    `db:"created_at"`
	UpdatedAt           time.Time    `db:"updated_at"`
	SubmittedAt         sql.NullTime `db:"submitted_at"`
	Revision            int64        `db:"revision"`
}

func (row manualSuspensionAttemptRow) domain(attemptID model.ExamAttemptID) (*model.ExamAttempt, error) {
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
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt),
		UpdatedAt: model.TimeUTC(row.UpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.Revision}
	if err = attempt.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "value", err)
	}
	return attempt, nil
}

const manualSuspensionAttemptSelect = `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.admission_revision_id,
	a.state,a.created_at,a.updated_at,a.submitted_at,a.revision
	FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id WHERE a.id=?`

type manualSuspensionParticipationRow struct {
	ID              string         `db:"id"`
	SessionID       string         `db:"session_id"`
	State           string         `db:"state"`
	Generation      int64          `db:"generation"`
	RenewalSequence int64          `db:"renewal_sequence"`
	CredentialHash  string         `db:"continuity_credential_hash"`
	StartedAt       time.Time      `db:"started_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	LeaseExpiresAt  time.Time      `db:"lease_expires_at"`
	EndedAt         sql.NullTime   `db:"ended_at"`
	EndReason       sql.NullString `db:"end_reason"`
}

const manualSuspensionParticipationColumns = `id,session_id,state,generation,renewal_sequence,continuity_credential_hash,
	started_at,updated_at,lease_expires_at,ended_at,end_reason`

func (row manualSuspensionParticipationRow) domain(attemptID model.ExamAttemptID) (*model.AttemptParticipation, error) {
	id, err := model.ParseAttemptParticipationID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "id", err)
	}
	sessionID, err := model.ParseSessionID(row.SessionID)
	if err != nil {
		return nil, invalidPersistedState("attempt_participation", "session_id", err)
	}
	participation := &model.AttemptParticipation{ID: id, AttemptID: attemptID, SessionID: sessionID,
		State: model.AttemptParticipationState(row.State), Generation: row.Generation, RenewalSequence: row.RenewalSequence,
		ContinuityCredentialHash: row.CredentialHash, StartedAt: model.TimeUTC(row.StartedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	if err = participation.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_participation", "value", err)
	}
	return participation, nil
}

func participationView(participation *model.AttemptParticipation) *store.ExamAttemptParticipationView {
	return &store.ExamAttemptParticipationView{ID: participation.ID, AttemptID: participation.AttemptID, State: participation.State,
		Generation: participation.Generation, RenewalSequence: participation.RenewalSequence, StartedAt: participation.StartedAt,
		UpdatedAt: participation.UpdatedAt, LeaseExpiresAt: participation.LeaseExpiresAt, EndedAt: participation.EndedAt,
		EndReason: participation.EndReason}
}

// suspendExamAttemptByManager runs after the Attempt and Sitting are locked
// and current authority is rechecked. Lock order follows expiry: Attempt, then
// Participation, then Connection.
func suspendExamAttemptByManager(ctx context.Context, tx *sqlxTxWrapper,
	input *store.ExamAttemptManualSuspension,
) (examAttemptManualSuspensionOutcomeV1, error) {
	var zero examAttemptManualSuspensionOutcomeV1
	var attemptRow manualSuspensionAttemptRow
	if err := tx.Get(ctx, &attemptRow, manualSuspensionAttemptSelect, input.AttemptID.String()); err != nil {
		return zero, translateError("exam_attempt", input.AttemptID.String(), err)
	}
	if attemptRow.Revision != input.ExpectedAttemptRevision {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_revision", nil)
	}
	if attemptRow.State != string(model.ExamAttemptActive) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	attempt, err := attemptRow.domain(input.AttemptID)
	if err != nil {
		return zero, err
	}
	var participationRow manualSuspensionParticipationRow
	err = tx.Get(ctx, &participationRow, `SELECT `+manualSuspensionParticipationColumns+`
		FROM exam_attempt_participations WHERE exam_attempt_id=? AND state='active' FOR UPDATE`, input.AttemptID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	if err != nil {
		return zero, fmt.Errorf("lock kicked Attempt Participation: %w", err)
	}
	participation, err := participationRow.domain(input.AttemptID)
	if err != nil {
		return zero, err
	}
	var connection *model.AttemptConnection
	var connectionRow attemptConnectionRow
	err = tx.Get(ctx, &connectionRow, `SELECT id,exam_attempt_id,participation_id,session_id,state,opened_at,closed_at,close_reason
		FROM exam_attempt_connections WHERE exam_attempt_id=? AND participation_id=? AND state='open' FOR UPDATE`,
		input.AttemptID.String(), participation.ID.String())
	switch {
	case err == nil:
		if connection, err = connectionRow.model(); err != nil {
			return zero, err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return zero, fmt.Errorf("lock kicked Attempt Connection: %w", err)
	}
	var at time.Time
	if err = tx.Get(ctx, &at, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read Exam Attempt manual suspension time: %w", err)
	}
	at = model.TimeUTC(at)
	if err = model.SuspendExamAttemptByManager(attempt, participation, connection, at); err != nil {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", err)
	}
	suspension, err := model.NewManualAttemptSuspension(input.SuspensionID, attempt.ID, participation.ID, participation.Generation,
		input.ActorUserID, input.PrivateReason, at)
	if err != nil {
		return zero, store.NewErrInvalidInput("attempt_suspension", "private_reason", nil)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,revision=? WHERE id=? AND revision=? AND state='active'`,
		attempt.State, attempt.UpdatedAt, attempt.Revision, attempt.ID.String(), input.ExpectedAttemptRevision); err != nil {
		return zero, fmt.Errorf("suspend kicked Exam Attempt: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=?
		WHERE id=? AND exam_attempt_id=? AND generation=? AND state='active'`, participation.State, participation.UpdatedAt,
		participation.EndedAt.Time, participation.EndReason, participation.ID.String(), attempt.ID.String(), participation.Generation); err != nil {
		return zero, fmt.Errorf("end kicked Attempt Participation: %w", err)
	}
	if err = closeNativeDelivery(ctx, tx, participation.ID, model.DeliveryClosedSuspension, at); err != nil {
		return zero, err
	}
	outcome := examAttemptManualSuspensionOutcomeV1{ExamID: attempt.ExamID.String(), SittingID: attempt.SittingID.String(),
		ClassID: attemptRow.ClassID, CandidateID: attempt.CandidateUserID.String(), AttemptID: attempt.ID.String(),
		ParticipationID: participation.ID.String(), SuspensionID: suspension.ID.String()}
	if connection != nil {
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state=?,closed_at=?,close_reason=?
			WHERE id=? AND exam_attempt_id=? AND participation_id=? AND state='open'`, connection.State, connection.ClosedAt.Time,
			connection.CloseReason, connection.ID.String(), attempt.ID.String(), participation.ID.String()); err != nil {
			return zero, fmt.Errorf("close kicked Attempt Connection: %w", err)
		}
		outcome.ConnectionID, outcome.ConnectionClosed = connection.ID.String(), true
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_suspensions
		(id,exam_attempt_id,participation_id,generation,suspension_attempt_revision,state,source,candidate_reason,started_at,
		 suspended_by_user_id,suspension_private_reason) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		suspension.ID.String(), suspension.AttemptID.String(), suspension.ParticipationID.String(), suspension.Generation,
		attempt.Revision, suspension.State, suspension.Source, suspension.CandidateReason, suspension.StartedAt,
		suspension.SuspendedByUserID.String(), suspension.SuspensionPrivateReason); err != nil {
		return zero, fmt.Errorf("create manual Attempt Suspension: %w", translateError("attempt_suspension", suspension.ID.String(), err))
	}
	if err = completeExamAttemptManualSuspensionAudit(ctx, tx, outcome, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return outcome, nil
}

func completeExamAttemptManualSuspensionAudit(ctx context.Context, tx *sqlxTxWrapper, value examAttemptManualSuspensionOutcomeV1,
	replayed bool, originalAuditID, auditID string, auditAt int64,
) error {
	data := map[string]any{"exam_id": value.ExamID, "exam_sitting_id": value.SittingID, "exam_attempt_id": value.AttemptID,
		"participation_id": value.ParticipationID, "suspension_id": value.SuspensionID, "connection_closed": value.ConnectionClosed,
		"replayed": replayed}
	if replayed {
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Attempt manual suspension audit: %w", err)
	}
	return nil
}

// loadExamAttemptManualSuspensionResult rehydrates the committed kick from
// its immutable episode start, ended generation, and closed Connection, so an
// exact replay returns the original result even after a later re-allow.
func (s *sqlExamAttemptStore) loadExamAttemptManualSuspensionResult(ctx context.Context,
	value examAttemptManualSuspensionOutcomeV1,
) (*store.ExamAttemptManualSuspensionResult, error) {
	attemptID, err := model.ParseExamAttemptID(value.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "exam_attempt_id", err)
	}
	classID, err := model.ParseClassID(value.ClassID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "class_id", err)
	}
	var attemptRow manualSuspensionAttemptRow
	if err = s.GetMaster().Get(ctx, &attemptRow, manualSuspensionAttemptSelect, attemptID.String()); err != nil {
		return nil, translateError("exam_attempt", attemptID.String(), err)
	}
	attempt, err := attemptRow.domain(attemptID)
	if err != nil {
		return nil, err
	}
	var suspensionRow struct {
		ParticipationID string    `db:"participation_id"`
		Generation      int64     `db:"generation"`
		AttemptRevision int64     `db:"suspension_attempt_revision"`
		Source          string    `db:"source"`
		CandidateReason string    `db:"candidate_reason"`
		StartedAt       time.Time `db:"started_at"`
		FlagID          string    `db:"flag_id"`
		SuspendedBy     string    `db:"suspended_by_user_id"`
	}
	if err = s.GetMaster().Get(ctx, &suspensionRow, `SELECT participation_id,generation,suspension_attempt_revision,source,
		candidate_reason,started_at,COALESCE(integrity_flag_id,'') AS flag_id,COALESCE(suspended_by_user_id,'') AS suspended_by_user_id
		FROM exam_attempt_suspensions WHERE id=? AND exam_attempt_id=?`, value.SuspensionID, attemptID.String()); err != nil {
		return nil, translateError("attempt_suspension", value.SuspensionID, err)
	}
	source := model.AttemptSuspensionSource(suspensionRow.Source)
	reason := model.AttemptSuspensionCandidateReason(suspensionRow.CandidateReason)
	flagID, suspendedBy, err := suspensionProvenance(source, reason, suspensionRow.FlagID, suspensionRow.SuspendedBy)
	if err != nil {
		return nil, err
	}
	suspensionID, err := model.ParseAttemptSuspensionID(value.SuspensionID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "suspension_id", err)
	}
	if source != model.AttemptSuspensionSourceManual || suspensionRow.ParticipationID != value.ParticipationID {
		return nil, invalidPersistedState("attempt_suspension", "manual_outcome", errors.New("mismatched manual Suspension outcome"))
	}
	var participationRow manualSuspensionParticipationRow
	if err = s.GetMaster().Get(ctx, &participationRow, `SELECT `+manualSuspensionParticipationColumns+`
		FROM exam_attempt_participations WHERE id=? AND exam_attempt_id=?`, value.ParticipationID, attemptID.String()); err != nil {
		return nil, translateError("attempt_participation", value.ParticipationID, err)
	}
	participation, err := participationRow.domain(attemptID)
	if err != nil {
		return nil, err
	}
	if participation.State != model.AttemptParticipationEnded || participation.EndReason != model.AttemptParticipationEndKicked ||
		participation.Generation != suspensionRow.Generation {
		return nil, invalidPersistedState("attempt_participation", "manual_outcome", errors.New("kicked Participation was not ended as kicked"))
	}
	historical := *attempt
	historical.State, historical.UpdatedAt, historical.SubmittedAt, historical.Revision = model.ExamAttemptSuspended,
		model.TimeUTC(suspensionRow.StartedAt), model.OptionalTime{}, suspensionRow.AttemptRevision
	if err = historical.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "manual_outcome", err)
	}
	result := &store.ExamAttemptManualSuspensionResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID, ClassID: classID,
		CandidateUserID: attempt.CandidateUserID, Attempt: &historical, Participation: participationView(participation),
		ConnectionClosed: value.ConnectionClosed,
		Suspension: &store.ExamAttemptSuspensionView{ID: suspensionID, AttemptID: attemptID, ParticipationID: participation.ID,
			FlagID: flagID, Generation: suspensionRow.Generation, State: model.AttemptSuspensionActive, Source: source,
			CandidateReason: reason, StartedAt: model.TimeUTC(suspensionRow.StartedAt), SuspendedByUserID: suspendedBy}}
	if value.ConnectionID != "" {
		var connectionRow attemptConnectionRow
		if err = s.GetMaster().Get(ctx, &connectionRow, `SELECT id,exam_attempt_id,participation_id,session_id,state,opened_at,closed_at,close_reason
			FROM exam_attempt_connections WHERE id=? AND exam_attempt_id=? AND participation_id=?`,
			value.ConnectionID, attemptID.String(), participation.ID.String()); err != nil {
			return nil, translateError("attempt_connection", value.ConnectionID, err)
		}
		connection, connectionErr := connectionRow.model()
		if connectionErr != nil {
			return nil, connectionErr
		}
		if connection.CloseReason != model.AttemptConnectionCloseKicked {
			return nil, invalidPersistedState("attempt_connection", "manual_outcome", errors.New("kicked Connection was not closed as kicked"))
		}
		result.Connection = &store.ExamAttemptManagerConnection{ID: connection.ID, State: connection.State,
			OpenedAt: connection.OpenedAt, ClosedAt: connection.ClosedAt, CloseReason: connection.CloseReason}
	}
	return result, nil
}
