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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func validateExamSittingLifecycleJob(job *model.Job, sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase, revision int64, availableAt time.Time) error {
	if job == nil {
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil)
	}
	wantKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil).Wrap(err)
	}
	command, decodeErr := model.DecodeExamSittingLifecycleCommand(job.Command)
	validateErr := job.Validate()
	wantType := model.JobTypeExamSittingLifecycle
	if phase == model.ExamSittingLifecycleJobFinalize {
		wantType = model.JobTypeExamSittingSealing
	}
	if validateErr != nil || decodeErr != nil || command.ExamSittingID != sittingID ||
		job.Type != wantType || job.Status != model.JobStatusQueued ||
		job.DedupePolicy != model.JobDedupeActive || job.DedupeKey != wantKey || job.AttemptCount != 0 ||
		job.Revision != 1 || job.WorkReserved != 0 || job.StartedAt.Valid || job.CompletedAt.Valid || job.Progress != nil ||
		len(job.Checkpoint) != 0 || len(job.Result) != 0 ||
		job.PublicErrorCode != "" || !job.AvailableAt.Equal(model.TimeUTC(availableAt)) {
		if validateErr != nil {
			decodeErr = validateErr
		}
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil).Wrap(decodeErr)
	}
	return nil
}

func insertExamSittingLifecycleJob(ctx context.Context, tx *sqlxTxWrapper, job *model.Job) error {
	result, err := insertQueuedJob(ctx, tx, job, true)
	if err != nil {
		return fmt.Errorf("insert Exam Sitting lifecycle Job: %w", translateError("job", job.ID.String(), err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Exam Sitting lifecycle Job insertion: %w", err)
	}
	if affected != 1 {
		return store.NewErrConflict("exam_sitting", "exam_sitting_job_mismatch", nil)
	}
	return nil
}

func insertExamSittingPrivateAction(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, actorID model.UserID,
	actionCode string, privateReason string, auditEventID model.AuditEventID,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO exam_sitting_private_actions
		(audit_event_id,exam_sitting_id,actor_user_id,action_code,private_reason,created_at,sitting_revision)
		VALUES (?,?,?,?,?,?,?)`, auditEventID, sitting.ID.String(), actorID.String(), actionCode, privateReason,
		sitting.UpdatedAt, sitting.Revision)
	if err != nil {
		return fmt.Errorf("insert Exam Sitting private action: %w", translateError("exam_sitting_private_action", auditEventID.String(), err))
	}
	return nil
}

// Pause pauses an open Sitting at database time under command. The Sitting must
// be at ExpectedRevision and in a state that allows pausing; otherwise it is an
// exam_sitting_revision or exam_sitting_state conflict.
func (s SQLExamSittingStore) Pause(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if err := prepareExamSittingManagerTransition(input, false); err != nil {
		return nil, err
	}
	return s.runManagerLifecycleMutation(ctx, "Exam Sitting pause", input, command, store.ExamSittingTransitionManagerPaused, true,
		func(current *model.ExamSitting, at time.Time) error { return current.Pause(at) })
}

// Resume reopens a paused Sitting at database time under command, with the same
// revision and state fencing as Pause.
func (s SQLExamSittingStore) Resume(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if err := prepareExamSittingManagerTransition(input, false); err != nil {
		return nil, err
	}
	return s.runManagerLifecycleMutation(ctx, "Exam Sitting resume", input, command, store.ExamSittingTransitionManagerResumed, false,
		func(current *model.ExamSitting, at time.Time) error { return current.Resume(at) })
}

// EarlyClose moves an open or paused Sitting into Closing with the
// manager_closed reason under command and schedules its finalize Job, with the
// same revision and state fencing as Pause.
func (s SQLExamSittingStore) EarlyClose(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if err := prepareExamSittingManagerTransition(input, true); err != nil {
		return nil, err
	}
	if err := validateExamSittingLifecycleJob(input.FinalizeJob, input.SittingID, model.ExamSittingLifecycleJobFinalize,
		input.ExpectedRevision+1, input.ChangedAt); err != nil {
		return nil, err
	}
	return s.runManagerLifecycleMutation(ctx, "Exam Sitting early close", input, command, store.ExamSittingTransitionManagerClosed, true,
		func(current *model.ExamSitting, at time.Time) error {
			return current.EnterClosing(model.ExamSittingReasonManagerClosed, at)
		})
}

func prepareExamSittingManagerTransition(input *store.ExamSittingManagerTransition, requireFinalize bool) error {
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() ||
		input.ExpectedRevision < 1 || input.ChangedAt.IsZero() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
		!validExamSittingPrivateReason(input.PrivateReason) || (requireFinalize != (input.FinalizeJob != nil)) {
		return store.NewErrInvalidInput("exam_sitting", "manager_transition", nil)
	}
	return nil
}

func validExamSittingPrivateReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
}

func (s SQLExamSittingStore) runManagerLifecycleMutation(ctx context.Context, label string, input *store.ExamSittingManagerTransition,
	command *store.CommandIdempotency, transition store.ExamSittingLifecycleTransitionCode, allowArchived bool,
	apply func(*model.ExamSitting, time.Time) error,
) (*store.ExamSittingLifecycleResult, error) {
	if command == nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, label, idempotentMutation[*model.ExamSitting]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			if err := lockExamSittingLifecycle(ctx, tx); err != nil {
				return nil, err
			}
			if err := guardExamSittingManagerExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, allowArchived); err != nil {
				return nil, err
			}
			current, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
			if err != nil {
				return nil, err
			}
			if current.Revision != input.ExpectedRevision {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
			}
			if !examSittingManagerTransitionAllowed(current.State, transition) {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
			}
			var decisionAt time.Time
			if err = tx.Get(ctx, &decisionAt, `SELECT statement_timestamp()`); err != nil {
				return nil, err
			}
			decisionAt = model.TimeUTC(decisionAt)
			if !decisionAt.Before(current.ScheduledEndAt) {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
			}
			if err = apply(current, decisionAt); err != nil {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
			}
			if err = persistExamSittingLifecycle(ctx, tx, current, input.ExpectedRevision); err != nil {
				return nil, err
			}
			if input.FinalizeJob != nil {
				if err = insertExamSittingLifecycleJob(ctx, tx, input.FinalizeJob); err != nil {
					return nil, err
				}
			}
			if err = insertExamSittingPrivateAction(ctx, tx, current, input.ActorUserID, string(transition), input.PrivateReason, input.AuditEventID); err != nil {
				return nil, err
			}
			if err = completeExamSittingLifecycleAudit(ctx, tx, current, transition, true, false, "", input.AuditEventID, input.AuditAt); err != nil {
				return nil, err
			}
			return current, nil
		},
		encode: encodeExamSittingOutcome,
		decode: decodeExamSittingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, originalAuditID string) error {
			return completeExamSittingLifecycleAudit(ctx, tx, sitting, transition, true, true, originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := s.snapshotFromCommittedOutcome(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	return &store.ExamSittingLifecycleResult{Value: snapshot, Transition: transition,
		Changed: true, Replayed: result.Replayed}, nil
}

func examSittingManagerTransitionAllowed(state model.ExamSittingState, transition store.ExamSittingLifecycleTransitionCode) bool {
	switch transition {
	case store.ExamSittingTransitionManagerPaused:
		return state == model.ExamSittingOpen
	case store.ExamSittingTransitionManagerResumed:
		return state == model.ExamSittingPaused
	case store.ExamSittingTransitionManagerClosed:
		return state == model.ExamSittingOpen || state == model.ExamSittingPaused
	default:
		return false
	}
}

// Extend moves an open or paused Sitting's scheduled end to ScheduledEndAt
// under command and reschedules its deadline Job. It conflicts when the Sitting
// is not at ExpectedRevision, its deadline has already passed by database time,
// or the new end leaves its Academic Period.
func (s SQLExamSittingStore) Extend(ctx context.Context, input *store.ExamSittingExtension, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() ||
		input.ExpectedRevision < 1 || input.ScheduledEndAt.IsZero() || input.ChangedAt.IsZero() || input.DeadlineJob == nil ||
		!validExamSittingPrivateReason(input.PrivateReason) || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() || command == nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "extension", nil)
	}
	if err := validateExamSittingLifecycleJob(input.DeadlineJob, input.SittingID, model.ExamSittingLifecycleJobDeadline,
		input.ExpectedRevision+1, input.ScheduledEndAt); err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "Exam Sitting extension", idempotentMutation[*model.ExamSitting]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			if err := lockExamSittingLifecycle(ctx, tx); err != nil {
				return nil, err
			}
			if err := guardExamSittingManagerExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, false); err != nil {
				return nil, err
			}
			current, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
			if err != nil {
				return nil, err
			}
			if current.Revision != input.ExpectedRevision {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
			}
			if current.State != model.ExamSittingOpen && current.State != model.ExamSittingPaused {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
			}
			var decisionAt time.Time
			if err = tx.Get(ctx, &decisionAt, `SELECT statement_timestamp()`); err != nil {
				return nil, err
			}
			decisionAt = model.TimeUTC(decisionAt)
			if !decisionAt.Before(current.ScheduledEndAt) {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
			}
			if err = guardExamSittingStructure(ctx, tx, current, input.ScheduledEndAt, true); err != nil {
				var conflict *store.ErrConflict
				if errors.As(err, &conflict) && conflict.Constraint == "exam_sitting_period_containment" {
					return nil, store.NewErrConflict("exam_sitting", "exam_sitting_schedule_outside_period", nil)
				}
				return nil, err
			}
			if err = current.ExtendEnd(input.ScheduledEndAt, decisionAt); err != nil {
				return nil, store.NewErrConflict("exam_sitting", "exam_sitting_extension_not_later", nil)
			}
			if err = persistExamSittingLifecycle(ctx, tx, current, input.ExpectedRevision); err != nil {
				return nil, err
			}
			if err = insertExamSittingLifecycleJob(ctx, tx, input.DeadlineJob); err != nil {
				return nil, err
			}
			if err = insertExamSittingPrivateAction(ctx, tx, current, input.ActorUserID, string(store.ExamSittingTransitionManagerExtended), input.PrivateReason, input.AuditEventID); err != nil {
				return nil, err
			}
			if err = completeExamSittingLifecycleAudit(ctx, tx, current, store.ExamSittingTransitionManagerExtended, true, false, "", input.AuditEventID, input.AuditAt); err != nil {
				return nil, err
			}
			return current, nil
		},
		encode: encodeExamSittingOutcome,
		decode: decodeExamSittingOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, originalAuditID string) error {
			return completeExamSittingLifecycleAudit(ctx, tx, sitting, store.ExamSittingTransitionManagerExtended, true, true,
				originalAuditID, input.AuditEventID, input.AuditAt)
		},
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := s.snapshotFromCommittedOutcome(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	return &store.ExamSittingLifecycleResult{Value: snapshot,
		Transition: store.ExamSittingTransitionManagerExtended, Changed: true, Replayed: result.Replayed}, nil
}

func persistExamSittingLifecycle(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, expectedRevision int64) error {
	reason := sql.NullString{}
	if sitting.ReasonCode != "" {
		reason = sql.NullString{String: string(sitting.ReasonCode), Valid: true}
	}
	result, err := tx.Exec(ctx, `UPDATE exam_sittings SET scheduled_end_at=?,state=?,updated_at=?,opened_at=?,paused_at=?,
		closing_at=?,closed_at=?,canceled_at=?,reason_code=?,revision=? WHERE id=? AND revision=?`, sitting.ScheduledEndAt,
		sitting.State, sitting.UpdatedAt, NullTimeFromOptional(sitting.OpenedAt), NullTimeFromOptional(sitting.PausedAt),
		NullTimeFromOptional(sitting.ClosingAt), NullTimeFromOptional(sitting.ClosedAt), NullTimeFromOptional(sitting.CanceledAt),
		reason, sitting.Revision, sitting.ID.String(), expectedRevision)
	if err != nil {
		return fmt.Errorf("persist Exam Sitting lifecycle: %w", err)
	}
	return requireExamSittingAffected(result)
}

// AdvanceDue applies whichever system lifecycle transition is due for the
// Sitting at database time: opening or canceling a scheduled Sitting, entering
// Closing at the scheduled end, or scheduling finalization. A Sitting with
// nothing due is returned unchanged.
func (s SQLExamSittingStore) AdvanceDue(ctx context.Context, input *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.SittingID.IsValid() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("exam_sitting", "advance_due", nil)
	}
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "advance due Exam Sitting", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingLifecycleResult, error) {
		return advanceDueExamSitting(ctx, tx, input)
	})
	if err != nil {
		return nil, err
	}
	if err := s.completeCommittedSnapshot(ctx, result.Value); err != nil {
		return nil, err
	}
	return result, nil
}

// advanceDueExamSitting locks the Sitting's lifecycle, applies the due
// transition at statement time, persists it with any finalization Job, and
// audits the outcome inside the caller's transaction.
func advanceDueExamSitting(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	examID, err := resolveExamSittingExamID(ctx, tx, input.SittingID)
	if err != nil {
		return nil, err
	}
	if err = lockExamSittingLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	archived, err := lockExamSittingSystemExam(ctx, tx, examID)
	if err != nil {
		return nil, err
	}
	current, err := lockExamSitting(ctx, tx, examID, input.SittingID)
	if err != nil {
		return nil, err
	}
	var now time.Time
	if err = tx.Get(ctx, &now, `SELECT statement_timestamp()`); err != nil {
		return nil, fmt.Errorf("read Exam Sitting lifecycle time: %w", err)
	}
	now = model.TimeUTC(now)
	expectedRevision := current.Revision
	transition, changed, err := applyDueExamSittingTransition(ctx, tx, input, current, archived, now)
	if err != nil {
		return nil, err
	}
	if changed {
		if err = persistExamSittingLifecycle(ctx, tx, current, expectedRevision); err != nil {
			return nil, err
		}
		if current.State == model.ExamSittingClosing {
			if err = insertExamSittingLifecycleJob(ctx, tx, input.FinalizeJob); err != nil {
				return nil, err
			}
		}
	}
	if !changed && current.State == model.ExamSittingClosing {
		if err = insertExamSittingLifecycleJob(ctx, tx, input.FinalizeJob); err != nil {
			return nil, err
		}
	}
	if err = completeExamSittingLifecycleAudit(ctx, tx, current, transition, changed, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: current}, Transition: transition, Changed: changed}, nil
}

// applyDueExamSittingTransition mutates the locked Sitting by the transition
// due at now, validating the finalization Job where Closing is involved, and
// reports the transition and whether the Sitting changed.
func applyDueExamSittingTransition(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingDueAdvance,
	current *model.ExamSitting, archived bool, now time.Time,
) (store.ExamSittingLifecycleTransitionCode, bool, error) {
	var err error
	transition := store.ExamSittingLifecycleTransitionCode("")
	changed := false
	switch current.State {
	case model.ExamSittingScheduled:
		if now.Before(current.ScheduledStartAt) {
			break
		}
		if !now.Before(current.ScheduledEndAt) {
			err = current.Cancel(model.ExamSittingReasonScheduleElapsed, now)
			transition = store.ExamSittingTransitionScheduleElapsed
			changed = true
			break
		}
		valid, contained := false, false
		if !archived {
			valid, contained, _, err = examSittingStructureStatus(ctx, tx, current, current.ScheduledEndAt)
		}
		if err != nil {
			return "", false, err
		}
		if !valid || !contained {
			err = current.Cancel(model.ExamSittingReasonAcademicStructureInvalid, now)
			transition = store.ExamSittingTransitionAcademicStructureInvalid
		} else {
			err = current.Open(now)
			transition = store.ExamSittingTransitionOpened
		}
		changed = true
	case model.ExamSittingOpen, model.ExamSittingPaused:
		if now.Before(current.ScheduledEndAt) {
			break
		}
		if input.FinalizeJob == nil {
			return "", false, store.NewErrInvalidInput("exam_sitting", "finalize_job", nil)
		}
		if err = validateExamSittingLifecycleJob(input.FinalizeJob, current.ID, model.ExamSittingLifecycleJobFinalize,
			current.Revision+1, current.ScheduledEndAt); err != nil {
			if isStaleExamSittingFinalizeJob(input.FinalizeJob, current) {
				return "", false, store.NewErrConflict("exam_sitting", "exam_sitting_revision", err)
			}
			return "", false, err
		}
		err = current.EnterClosing(model.ExamSittingReasonScheduledEndReached, now)
		transition = store.ExamSittingTransitionScheduledEndReached
		changed = true
	case model.ExamSittingClosing:
		if input.FinalizeJob == nil || !current.ClosingAt.Valid {
			return "", false, store.NewErrInvalidInput("exam_sitting", "finalize_job", nil)
		}
		if err = validateExamSittingLifecycleJob(input.FinalizeJob, current.ID,
			model.ExamSittingLifecycleJobFinalize, current.Revision, current.ClosingAt.Time); err != nil {
			return "", false, err
		}
	}
	if err != nil {
		return "", false, invalidPersistedState("exam_sitting", "lifecycle_transition", err)
	}
	return transition, changed, nil
}

func isStaleExamSittingFinalizeJob(job *model.Job, current *model.ExamSitting) bool {
	if job == nil || current == nil || job.Validate() != nil || job.Type != model.JobTypeExamSittingSealing ||
		job.Status != model.JobStatusQueued || job.DedupePolicy != model.JobDedupeActive || job.AttemptCount != 0 ||
		job.Revision != 1 || job.WorkReserved != 0 || job.StartedAt.Valid || job.CompletedAt.Valid || job.Progress != nil ||
		len(job.Checkpoint) != 0 || len(job.Result) != 0 ||
		job.PublicErrorCode != "" || !job.AvailableAt.Equal(current.ScheduledEndAt) {
		return false
	}
	command, err := model.DecodeExamSittingLifecycleCommand(job.Command)
	if err != nil || command.ExamSittingID != current.ID {
		return false
	}
	prefix := "exam-sitting:" + current.ID.String() + ":" + string(model.ExamSittingLifecycleJobFinalize) + ":"
	if !strings.HasPrefix(job.DedupeKey, prefix) {
		return false
	}
	preparedRevision, err := strconv.ParseInt(strings.TrimPrefix(job.DedupeKey, prefix), 10, 64)
	return err == nil && preparedRevision > 0 && preparedRevision != current.Revision+1
}

// FinishSealing closes a Closing Sitting once every Attempt is submitted with a
// sealed Submission; otherwise the Sitting is returned unchanged.
func (s SQLExamSittingStore) FinishSealing(ctx context.Context, input *store.ExamSittingFinishSealing) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.SittingID.IsValid() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("exam_sitting", "finish_sealing", nil)
	}
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "finish sealed Exam Sitting", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingLifecycleResult, error) {
		examID, err := resolveExamSittingExamID(ctx, tx, input.SittingID)
		if err != nil {
			return nil, err
		}
		if err = lockExamSittingLifecycle(ctx, tx); err != nil {
			return nil, err
		}
		if _, err = lockExamSittingSystemExam(ctx, tx, examID); err != nil {
			return nil, err
		}
		current, err := lockExamSitting(ctx, tx, examID, input.SittingID)
		if err != nil {
			return nil, err
		}
		changed, hasAttempts := false, false
		if current.State == model.ExamSittingClosing {
			var status struct {
				HasAttempts   bool `db:"has_attempts"`
				HasUnfinished bool `db:"has_unfinished"`
			}
			if err = tx.Get(ctx, &status, `SELECT
				EXISTS (SELECT 1 FROM exam_attempts a WHERE a.exam_sitting_id=? LIMIT 1) AS has_attempts,
				EXISTS (SELECT 1 FROM exam_attempts a
					LEFT JOIN exam_submissions sub ON sub.exam_attempt_id=a.id AND sub.sealed=true
					WHERE a.exam_sitting_id=? AND (a.state<>'submitted' OR sub.id IS NULL) LIMIT 1) AS has_unfinished`,
				input.SittingID.String(), input.SittingID.String()); err != nil {
				return nil, fmt.Errorf("inspect Closing Exam Sitting sealing: %w", err)
			}
			hasAttempts, changed = status.HasAttempts, !status.HasUnfinished
		}
		transition := store.ExamSittingLifecycleTransitionCode("")
		if changed {
			expected := current.Revision
			var now time.Time
			if err = tx.Get(ctx, &now, `SELECT statement_timestamp()`); err != nil {
				return nil, fmt.Errorf("read Exam Sitting sealing completion time: %w", err)
			}
			if err = current.Close(model.TimeUTC(now)); err != nil {
				return nil, invalidPersistedState("exam_sitting", "finish_sealing", err)
			}
			if err = persistExamSittingLifecycle(ctx, tx, current, expected); err != nil {
				return nil, err
			}
			transition = store.ExamSittingTransitionSealingCompleted
			if !hasAttempts {
				transition = store.ExamSittingTransitionClosedNoAttempts
			}
		}
		if err = completeExamSittingLifecycleAudit(ctx, tx, current, transition, changed, false, "", input.AuditEventID, input.AuditAt); err != nil {
			return nil, err
		}
		return &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: current}, Transition: transition, Changed: changed}, nil
	})
	if err != nil {
		return nil, err
	}
	if err = s.completeCommittedSnapshot(ctx, result.Value); err != nil {
		return nil, err
	}
	return result, nil
}

func lockExamSittingSystemExam(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID) (bool, error) {
	var archived sql.NullTime
	if err := tx.Get(ctx, &archived, `SELECT archived_at FROM exams WHERE id=? FOR UPDATE`, examID.String()); err != nil {
		return false, translateError("exam", examID.String(), err)
	}
	return archived.Valid, nil
}

func completeExamSittingLifecycleAudit(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting,
	transition store.ExamSittingLifecycleTransitionCode, changed, replayed bool, originalAuditID string, auditID model.AuditEventID, auditAt time.Time,
) error {
	data := map[string]any{"exam_id": sitting.ExamID.String(), "exam_sitting_id": sitting.ID.String(),
		"state": sitting.State, "revision": sitting.Revision, "reason_code": sitting.ReasonCode,
		"scheduled_end_at": sitting.ScheduledEndAt, "changed": changed}
	if transition != "" {
		data["transition"] = transition
	}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt)
	return err
}

func lockExamSittingLifecycle(ctx context.Context, tx sqlxExecutor) error {
	for _, lock := range []func(context.Context, sqlxExecutor) error{
		lockProgrammeLifecycle, lockProgrammeLevelLifecycle, lockAcademicPeriodLifecycle, lockClassLifecycle,
	} {
		if err := lock(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}
