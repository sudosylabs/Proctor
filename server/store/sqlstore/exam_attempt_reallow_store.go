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

type examAttemptReallowOutcome struct {
	ExamID               string `json:"exam_id"`
	SittingID            string `json:"sitting_id"`
	ClassID              string `json:"class_id"`
	CandidateID          string `json:"candidate_id"`
	AttemptID            string `json:"attempt_id"`
	SuspensionID         string `json:"suspension_id"`
	FocusLossWindowReset bool   `json:"focus_loss_window_reset"`
}

func (examAttemptReallowOutcome) outcomeDocument() {}

// ReallowAttempt implements store.ExamAttemptStore: it closes the Attempt's
// active Suspension and returns the Attempt to active under an
// ExamAttemptReallowOperation command. Exact retries return the retained result
// after rechecking authority.
func (s *SQLExamAttemptStore) ReallowAttempt(ctx context.Context, input *store.ExamAttemptReallow,
	command *store.CommandIdempotency,
) (*store.ExamAttemptReallowResult, error) {
	if input == nil || command == nil || command.Operation != store.ExamAttemptReallowOperation || command.UserID != input.ActorUserID || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.AttemptID.IsValid() ||
		!input.SuspensionID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedRevision < 1 ||
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("exam_attempt", "reallow", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "re-allow Exam Attempt", idempotentMutation[examAttemptReallowOutcome]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examAttemptReallowOutcome, error) {
			return reallowExamAttempt(ctx, tx, input)
		},
		encode: func(value examAttemptReallowOutcome) ([]byte, error) { return encodeOutcome(value) },
		decode: func(encoded []byte) (examAttemptReallowOutcome, error) {
			var value examAttemptReallowOutcome
			return value, decodeOutcome(encoded, &value)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value examAttemptReallowOutcome, originalAuditID string) error {
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
	AttemptState    string `db:"attempt_state"`
	SittingState    string `db:"sitting_state"`
	CandidateUserID string `db:"candidate_user_id"`
	ActorIsManager  bool   `db:"actor_is_manager"`
}

func guardExamAttemptReallowAuthority(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptReallow,
	lock bool,
) error {
	var guard examAttemptReallowGuard
	query := `SELECT a.state AS attempt_state,s.state AS sitting_state,a.candidate_user_id,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=a.exam_id AND m.user_id=?) AS actor_is_manager
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		WHERE a.id=? AND a.exam_id=? AND a.exam_sitting_id=?`
	if lock {
		// The Sitting row is only fenced against state changes: FOR SHARE
		// still conflicts with Sitting transitions but not with the FOR SHARE
		// that every renewal in the Sitting takes, so a kick or re-allow of
		// one Attempt never queues the others' renewals. FOR NO KEY UPDATE
		// would still conflict with that FOR SHARE.
		query += ` FOR UPDATE OF a FOR SHARE OF s`
	}
	err := tx.Get(ctx, &guard, query, input.ActorUserID.String(), input.AttemptID.String(), input.ExamID.String(), input.SittingID.String())
	if err != nil {
		return translateError("exam_attempt", input.AttemptID.String(), err)
	}
	if !guard.ActorIsManager && !input.ManagerOverride {
		return store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if guard.CandidateUserID == input.ActorUserID.String() {
		return store.NewErrNotFound("exam_attempt", input.AttemptID.String())
	}
	if guard.SittingState == string(model.ExamSittingClosing) || guard.SittingState == string(model.ExamSittingClosed) {
		return store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	return nil
}

func reallowExamAttempt(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptReallow) (examAttemptReallowOutcome, error) {
	var zero examAttemptReallowOutcome
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
		SuspendedByUserID   sql.NullString `db:"suspended_by_user_id"`
		SuspensionReason    sql.NullString `db:"suspension_private_reason"`
	}
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.admission_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,
		a.revision AS attempt_revision,su.participation_id,COALESCE(su.integrity_flag_id,'') AS flag_id,su.state AS suspension_state,
		su.source,su.candidate_reason,su.generation,su.started_at,su.ended_at,su.reallowed_by_user_id,su.private_reason,
		su.suspended_by_user_id,su.suspension_private_reason
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
	if row.AttemptRevision != input.ExpectedRevision {
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
	flagID, suspendedBy, err := suspensionProvenance(model.AttemptSuspensionSource(row.Source),
		model.AttemptSuspensionCandidateReason(row.CandidateReason), row.FlagID, row.SuspendedByUserID.String)
	if err != nil {
		return zero, err
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: input.ExamID, SittingID: input.SittingID, CandidateUserID: candidateID,
		AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt),
		UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	suspension := &model.AttemptSuspension{ID: input.SuspensionID, AttemptID: attemptID, ParticipationID: participationID,
		FlagID: flagID, Generation: row.Generation, State: model.AttemptSuspensionState(row.SuspensionState),
		Source: model.AttemptSuspensionSource(row.Source), CandidateReason: model.AttemptSuspensionCandidateReason(row.CandidateReason),
		StartedAt: model.TimeUTC(row.StartedAt), EndedAt: OptionalTimeFromNullTime(row.EndedAt),
		SuspendedByUserID: suspendedBy, SuspensionPrivateReason: row.SuspensionReason.String}
	if err = attempt.Validate(); err != nil {
		return zero, invalidPersistedState("exam_attempt", "value", err)
	}
	if err = suspension.Validate(); err != nil {
		return zero, invalidPersistedState("attempt_suspension", "value", err)
	}
	if !model.IsValidAttemptSuspensionPrivateReason(input.PrivateReason) {
		return zero, store.NewErrInvalidInput("attempt_suspension", "private_reason", nil)
	}
	// Database time decides the transition, matching the system expiry and
	// suspension paths, so a skewed application clock cannot reject a valid
	// decision or stamp it before the episode began.
	var changedAt time.Time
	if err = tx.Get(ctx, &changedAt, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read Exam Attempt re-allow time: %w", err)
	}
	changedAt = model.TimeUTC(changedAt)
	if err = suspension.Reallow(input.ActorUserID, input.PrivateReason, changedAt); err != nil {
		return zero, fmt.Errorf("close Attempt Suspension: %w", err)
	}
	if err = attempt.Reallow(changedAt); err != nil {
		return zero, fmt.Errorf("re-allow Exam Attempt: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempt_suspensions SET state='closed',ended_at=?,reallowed_by_user_id=?,private_reason=? WHERE id=? AND state='active'`,
		suspension.EndedAt.Time, suspension.ReallowedByUserID.String(), suspension.PrivateReason, suspension.ID.String()); err != nil {
		return zero, fmt.Errorf("close Attempt Suspension: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempts SET state='ready',updated_at=?,revision=? WHERE id=? AND revision=?`,
		attempt.UpdatedAt, attempt.Revision, attempt.ID.String(), row.AttemptRevision); err != nil {
		return zero, fmt.Errorf("re-allow Exam Attempt: %w", err)
	}
	focusLossWindowReset := row.CandidateReason == string(model.AttemptSuspensionCandidateReasonFocusLossPolicy)
	if focusLossWindowReset {
		if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=? AND generation=?`,
			input.AttemptID.String(), row.Generation); err != nil {
			return zero, fmt.Errorf("reset causal Focus Loss window: %w", err)
		}
	}
	value := examAttemptReallowOutcome{ExamID: input.ExamID.String(), SittingID: input.SittingID.String(), ClassID: row.ClassID,
		CandidateID: row.CandidateID, AttemptID: input.AttemptID.String(), SuspensionID: input.SuspensionID.String(),
		FocusLossWindowReset: focusLossWindowReset}
	if err = completeExamAttemptReallowAudit(ctx, tx, value, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return value, nil
}

func completeExamAttemptReallowAudit(ctx context.Context, tx *sqlxTxWrapper, value examAttemptReallowOutcome,
	replayed bool, originalAuditID string, auditID model.AuditEventID, auditAt time.Time,
) error {
	data := map[string]any{"exam_id": value.ExamID, "exam_sitting_id": value.SittingID, "exam_attempt_id": value.AttemptID,
		"suspension_id": value.SuspensionID, "focus_loss_window_reset": value.FocusLossWindowReset, "replayed": replayed}
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

func (s *SQLExamAttemptStore) loadExamAttemptReallowResult(ctx context.Context, value examAttemptReallowOutcome) (*store.ExamAttemptReallowResult, error) {
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
		SuspendedByUserID   string       `db:"suspended_by_user_id"`
		SuspensionReason    string       `db:"suspension_private_reason"`
	}
	err := s.GetMaster().Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,a.id AS attempt_id,a.admission_revision_id,
		a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
		su.id AS suspension_id,su.participation_id,COALESCE(su.integrity_flag_id,'') AS flag_id,su.state AS suspension_state,su.source,
		su.candidate_reason,su.generation,su.started_at,su.ended_at,su.reallowed_by_user_id,su.private_reason,
		COALESCE(su.suspended_by_user_id,'') AS suspended_by_user_id,COALESCE(su.suspension_private_reason,'') AS suspension_private_reason
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
	flagID, suspendedBy, err := suspensionProvenance(model.AttemptSuspensionSource(row.Source),
		model.AttemptSuspensionCandidateReason(row.CandidateReason), row.FlagID, row.SuspendedByUserID)
	if err != nil {
		return nil, err
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
		ReallowedByUserID: reallowedID, PrivateReason: row.PrivateReason,
		SuspendedByUserID: suspendedBy, SuspensionPrivateReason: row.SuspensionReason}
	if err = domainSuspension.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_suspension", "value", err)
	}
	return &store.ExamAttemptReallowResult{ExamID: examID, SittingID: sittingID, ClassID: classID, CandidateUserID: candidateID,
		Attempt: attempt, FocusLossWindowReset: value.FocusLossWindowReset,
		Suspension: &store.ExamAttemptSuspensionView{ID: domainSuspension.ID, AttemptID: domainSuspension.AttemptID,
			ParticipationID: domainSuspension.ParticipationID, FlagID: domainSuspension.FlagID, Generation: domainSuspension.Generation,
			State: domainSuspension.State, Source: domainSuspension.Source, CandidateReason: domainSuspension.CandidateReason,
			StartedAt: domainSuspension.StartedAt, EndedAt: domainSuspension.EndedAt, ReallowedByUserID: domainSuspension.ReallowedByUserID,
			SuspendedByUserID: domainSuspension.SuspendedByUserID}}, nil
}
