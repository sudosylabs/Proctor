// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// sqlExamSittingStore owns the PostgreSQL representation of scheduled Exam
// delivery. Its mutations deliberately acquire the academic lifecycle locks
// in the same global order as the catalog Stores before locking Exam rows.
type sqlExamSittingStore struct{ *SQLStore }

func newSQLExamSittingStore(sqlStore *SQLStore) store.ExamSittingStore {
	return &sqlExamSittingStore{SQLStore: sqlStore}
}

type examSittingRow struct {
	ID               string         `db:"id"`
	ExamID           string         `db:"exam_id"`
	ExamRevisionID   string         `db:"exam_revision_id"`
	ClassID          string         `db:"class_id"`
	ScheduledStartAt time.Time      `db:"scheduled_start_at"`
	ScheduledEndAt   time.Time      `db:"scheduled_end_at"`
	State            string         `db:"state"`
	CreatedAt        time.Time      `db:"created_at"`
	UpdatedAt        time.Time      `db:"updated_at"`
	OpenedAt         sql.NullTime   `db:"opened_at"`
	PausedAt         sql.NullTime   `db:"paused_at"`
	ClosingAt        sql.NullTime   `db:"closing_at"`
	ClosedAt         sql.NullTime   `db:"closed_at"`
	CanceledAt       sql.NullTime   `db:"canceled_at"`
	ReasonCode       sql.NullString `db:"reason_code"`
	Revision         int64          `db:"revision"`
	AcademicUnitID   string         `db:"academic_unit_id"`
}

const examSittingColumns = `id,exam_id,exam_revision_id,class_id,scheduled_start_at,scheduled_end_at,
	state,created_at,updated_at,opened_at,paused_at,closing_at,closed_at,canceled_at,reason_code,revision,
	(SELECT academic_unit_id FROM exams WHERE exams.id=exam_sittings.exam_id) AS academic_unit_id`
const examSittingSelect = `SELECT ` + examSittingColumns + ` FROM exam_sittings`

func (row examSittingRow) model() (*model.ExamSitting, error) {
	id, err := model.ParseExamSittingID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "exam_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.ExamRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "exam_revision_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "class_id", err)
	}
	sitting := &model.ExamSitting{
		ID: id, ExamID: examID, ExamRevisionID: revisionID, ClassID: classID,
		ScheduledStartAt: model.TimeUTC(row.ScheduledStartAt), ScheduledEndAt: model.TimeUTC(row.ScheduledEndAt),
		State: model.ExamSittingState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		OpenedAt: OptionalTimeFromNullTime(row.OpenedAt), PausedAt: OptionalTimeFromNullTime(row.PausedAt),
		ClosingAt: OptionalTimeFromNullTime(row.ClosingAt), ClosedAt: OptionalTimeFromNullTime(row.ClosedAt),
		CanceledAt: OptionalTimeFromNullTime(row.CanceledAt), Revision: row.Revision,
	}
	if row.ReasonCode.Valid {
		sitting.ReasonCode = model.ExamSittingReasonCode(row.ReasonCode.String)
	}
	if err := sitting.Validate(); err != nil {
		return nil, invalidPersistedState("exam_sitting", "value", err)
	}
	return sitting, nil
}

func (s sqlExamSittingStore) Resolve(ctx context.Context, id model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("exam_sitting", "id", nil)
	}
	var row examSittingRow
	if err := s.GetMaster().Get(ctx, &row, examSittingSelect+` WHERE id=?`, id.String()); err != nil {
		return nil, translateError("exam_sitting", id.String(), err)
	}
	return examSittingSnapshot(row)
}

func (s sqlExamSittingStore) Get(ctx context.Context, examID model.ExamID, id model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	if !examID.IsValid() || !id.IsValid() {
		return nil, store.NewErrInvalidInput("exam_sitting", "identity", nil)
	}
	var row examSittingRow
	if err := s.GetMaster().Get(ctx, &row, examSittingSelect+` WHERE exam_id=? AND id=?`, examID.String(), id.String()); err != nil {
		return nil, translateError("exam_sitting", id.String(), err)
	}
	return examSittingSnapshot(row)
}

func examSittingSnapshot(row examSittingRow) (*store.ExamSittingSnapshot, error) {
	sitting, err := row.model()
	if err != nil {
		return nil, err
	}
	unitID, err := model.ParseAcademicUnitID(row.AcademicUnitID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "academic_unit_id", err)
	}
	return &store.ExamSittingSnapshot{Sitting: sitting, AcademicUnitID: unitID}, nil
}

func (s sqlExamSittingStore) List(ctx context.Context, options store.ExamSittingListOptions) ([]store.ExamSittingSnapshot, error) {
	if err := validateExamSittingListOptions(options); err != nil {
		return nil, err
	}
	query := examSittingSelect + ` WHERE exam_id=?`
	args := []any{options.ExamID.String()}
	if options.ClassID.IsValid() {
		query += ` AND class_id=?`
		args = append(args, options.ClassID.String())
	}
	if len(options.States) > 0 {
		states := make([]string, len(options.States))
		for index, state := range options.States {
			states[index] = string(state)
		}
		query += ` AND state=ANY(?)`
		args = append(args, pq.Array(states))
	}
	if !options.OverlapStartAt.IsZero() {
		query += ` AND scheduled_start_at < ? AND scheduled_end_at > ?`
		args = append(args, model.TimeUTC(options.OverlapEndAt), model.TimeUTC(options.OverlapStartAt))
	}
	if !options.BeforeScheduledStartAt.IsZero() {
		query += ` AND (scheduled_start_at,id) < (?,?)`
		args = append(args, model.TimeUTC(options.BeforeScheduledStartAt), options.BeforeSittingID.String())
	}
	query += ` ORDER BY scheduled_start_at DESC,id DESC LIMIT ?`
	args = append(args, options.Limit)
	var rows []examSittingRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Sittings: %w", err)
	}
	items := make([]store.ExamSittingSnapshot, 0, len(rows))
	for _, row := range rows {
		item, err := examSittingSnapshot(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func (s sqlExamSittingStore) ListLifecycleDue(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	if options.Limit < 1 || options.Limit > 201 || (options.AfterDueAt.IsZero() != options.AfterSittingID.IsZero()) {
		return nil, store.NewErrInvalidInput("exam_sitting", "lifecycle_due_options", nil)
	}
	query, args := examSittingLifecycleDueQuery(options)
	var rows []struct {
		examSittingRow
		DueAt time.Time `db:"due_at"`
	}
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list lifecycle-due Exam Sittings: %w", err)
	}
	result := make([]store.ExamSittingLifecycleDue, 0, len(rows))
	for _, row := range rows {
		snapshot, err := examSittingSnapshot(row.examSittingRow)
		if err != nil {
			return nil, err
		}
		result = append(result, store.ExamSittingLifecycleDue{Value: snapshot, DueAt: model.TimeUTC(row.DueAt)})
	}
	return result, nil
}

func examSittingLifecycleDueQuery(options store.ExamSittingLifecycleDueOptions) (string, []any) {
	scheduled := `(SELECT ` + examSittingColumns + `,scheduled_start_at AS due_at FROM exam_sittings
		WHERE state='scheduled' AND scheduled_start_at<=statement_timestamp()`
	deadline := `(SELECT ` + examSittingColumns + `,scheduled_end_at AS due_at FROM exam_sittings
		WHERE state IN ('open','paused') AND scheduled_end_at<=statement_timestamp()`
	closing := `(SELECT ` + examSittingColumns + `,closing_at AS due_at FROM exam_sittings
		WHERE state='closing' AND closing_at<=statement_timestamp()`
	args := []any{}
	if !options.AfterDueAt.IsZero() {
		scheduled += ` AND (scheduled_start_at,id)>(?,?)`
		deadline += ` AND (scheduled_end_at,id)>(?,?)`
		closing += ` AND (closing_at,id)>(?,?)`
		args = append(args, model.TimeUTC(options.AfterDueAt), options.AfterSittingID.String())
	}
	scheduled += ` ORDER BY scheduled_start_at,id LIMIT ?)`
	args = append(args, options.Limit)
	if !options.AfterDueAt.IsZero() {
		args = append(args, model.TimeUTC(options.AfterDueAt), options.AfterSittingID.String())
	}
	deadline += ` ORDER BY scheduled_end_at,id LIMIT ?)`
	args = append(args, options.Limit)
	if !options.AfterDueAt.IsZero() {
		args = append(args, model.TimeUTC(options.AfterDueAt), options.AfterSittingID.String())
	}
	closing += ` ORDER BY closing_at,id LIMIT ?)`
	args = append(args, options.Limit, options.Limit)
	query := `SELECT * FROM (` + scheduled + ` UNION ALL ` + deadline + ` UNION ALL ` + closing + `) due ORDER BY due_at,id LIMIT ?`
	return query, args
}

func validateExamSittingListOptions(options store.ExamSittingListOptions) error {
	if !options.ExamID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		(options.OverlapStartAt.IsZero() != options.OverlapEndAt.IsZero()) ||
		(options.BeforeScheduledStartAt.IsZero() != options.BeforeSittingID.IsZero()) {
		return store.NewErrInvalidInput("exam_sitting", "list_options", nil)
	}
	if !options.OverlapStartAt.IsZero() && !options.OverlapStartAt.Before(options.OverlapEndAt) {
		return store.NewErrInvalidInput("exam_sitting", "overlap", nil)
	}
	seen := make(map[model.ExamSittingState]struct{}, len(options.States))
	for _, state := range options.States {
		if !state.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "state", state)
		}
		if _, duplicate := seen[state]; duplicate {
			return store.NewErrInvalidInput("exam_sitting", "states", nil)
		}
		seen[state] = struct{}{}
	}
	return nil
}

func (s sqlExamSittingStore) Schedule(ctx context.Context, input *store.ExamSittingSchedule, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	if err := prepareExamSittingSchedule(input); err != nil {
		return nil, err
	}
	return s.runExamSittingMutation(ctx, "Exam Sitting scheduling", input.AuditEventID, input.AuditAt, command,
		func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			return scheduleExamSitting(ctx, tx, input)
		})
}

func prepareExamSittingSchedule(input *store.ExamSittingSchedule) error {
	if input == nil || input.Sitting == nil || input.OpenJob == nil || input.DeadlineJob == nil || input.Mail == nil || !input.ActorUserID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_sitting", "schedule", nil)
	}
	if err := input.Sitting.Validate(); err != nil || input.Sitting.State != model.ExamSittingScheduled || input.Sitting.Revision != 1 {
		return store.NewErrInvalidInput("exam_sitting", "value", nil).Wrap(err)
	}
	if err := validateExamSittingLifecycleJob(input.OpenJob, input.Sitting.ID, model.ExamSittingLifecycleJobOpen, 1, input.Sitting.ScheduledStartAt); err != nil {
		return err
	}
	if err := validateExamSittingLifecycleJob(input.DeadlineJob, input.Sitting.ID, model.ExamSittingLifecycleJobDeadline, 1, input.Sitting.ScheduledEndAt); err != nil {
		return err
	}
	return nil
}

func (s sqlExamSittingStore) UpdateSchedule(ctx context.Context, input *store.ExamSittingScheduleUpdate, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	if err := prepareExamSittingUpdate(input); err != nil {
		return nil, err
	}
	return s.runExamSittingMutation(ctx, "Exam Sitting schedule update", input.AuditEventID, input.AuditAt, command,
		func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			return updateExamSittingSchedule(ctx, tx, input)
		})
}

func prepareExamSittingUpdate(input *store.ExamSittingScheduleUpdate) error {
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() || input.OpenJob == nil || input.DeadlineJob == nil || input.Mail == nil ||
		!input.ExamRevisionID.IsValid() || !input.ClassID.IsValid() || input.ExpectedRevision < 1 ||
		input.ScheduledStartAt.IsZero() || input.ScheduledEndAt.IsZero() || !input.ScheduledStartAt.Before(input.ScheduledEndAt) ||
		input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_sitting", "schedule_update", nil)
	}
	resultingRevision := input.ExpectedRevision + 1
	if err := validateExamSittingLifecycleJob(input.OpenJob, input.SittingID, model.ExamSittingLifecycleJobOpen, resultingRevision, input.ScheduledStartAt); err != nil {
		return err
	}
	if err := validateExamSittingLifecycleJob(input.DeadlineJob, input.SittingID, model.ExamSittingLifecycleJobDeadline, resultingRevision, input.ScheduledEndAt); err != nil {
		return err
	}
	return nil
}

func validateExamSittingLifecycleJob(job *model.Job, sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase, revision int64, availableAt time.Time) error {
	if job == nil {
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil)
	}
	wantKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
	if err != nil {
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil).Wrap(err)
	}
	command, decodeErr := model.DecodeExamSittingLifecycleCommand(job.CommandVersion, job.Command)
	validateErr := job.Validate()
	wantType := model.JobTypeExamSittingLifecycle
	if phase == model.ExamSittingLifecycleJobFinalize {
		wantType = model.JobTypeExamSittingSealing
	}
	if validateErr != nil || decodeErr != nil || command.ExamSittingID != sittingID ||
		job.Type != wantType || job.Status != model.JobStatusQueued ||
		job.DedupePolicy != model.JobDedupeActive || job.DedupeKey != wantKey || job.AttemptCount != 0 ||
		job.Revision != 1 || job.WorkReserved != 0 || job.StartedAt.Valid || job.CompletedAt.Valid || job.Progress != nil ||
		job.CheckpointVersion != 0 || len(job.Checkpoint) != 0 || job.ResultVersion != 0 || len(job.Result) != 0 ||
		job.PublicErrorCode != "" || !job.AvailableAt.Equal(model.TimeUTC(availableAt)) {
		if validateErr != nil {
			decodeErr = validateErr
		}
		return store.NewErrInvalidInput("exam_sitting", "lifecycle_job", nil).Wrap(decodeErr)
	}
	return nil
}

func (s sqlExamSittingStore) Cancel(ctx context.Context, input *store.ExamSittingCancellation, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
	if err := prepareExamSittingCancellation(input); err != nil {
		return nil, err
	}
	return s.runExamSittingMutation(ctx, "Exam Sitting cancellation", input.AuditEventID, input.AuditAt, command,
		func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			return cancelExamSitting(ctx, tx, input)
		})
}

func prepareExamSittingCancellation(input *store.ExamSittingCancellation) error {
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() || input.Mail == nil ||
		input.ExpectedRevision < 1 || input.CanceledAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		!utf8.ValidString(input.PrivateReason) || input.PrivateReason != strings.TrimSpace(input.PrivateReason) ||
		utf8.RuneCountInString(input.PrivateReason) < 1 || utf8.RuneCountInString(input.PrivateReason) > 1000 {
		return store.NewErrInvalidInput("exam_sitting", "cancellation", nil)
	}
	return nil
}

func (s sqlExamSittingStore) runExamSittingMutation(ctx context.Context, label, auditID string, auditAt int64, command *store.CommandIdempotency,
	execute func(context.Context, *sqlxTxWrapper) (*model.ExamSitting, error),
) (*store.ExamSittingCommandResult, error) {
	if command == nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, label, idempotentMutation[*model.ExamSitting]{
		command: command, auditEventID: auditID, execute: execute,
		encode: func(sitting *model.ExamSitting) ([]byte, error) { return encodeCommandOutcome(sitting) },
		decode: func(version int, data []byte) (*model.ExamSitting, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Exam Sitting outcome version %d", version)
			}
			var sitting model.ExamSitting
			if err := decodeCommandOutcome(data, &sitting); err != nil {
				return nil, err
			}
			if err := sitting.Validate(); err != nil {
				return nil, fmt.Errorf("decode Exam Sitting outcome: %w", err)
			}
			return &sitting, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, originalAuditID string) error {
			encoded, err := encodeExamSittingAudit(sitting, true, originalAuditID)
			if err != nil {
				return err
			}
			_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt)
			return err
		},
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := s.snapshotFromCommittedOutcome(ctx, result.Value)
	if err != nil {
		return nil, err
	}
	return &store.ExamSittingCommandResult{Value: snapshot, Replayed: result.Replayed}, nil
}

func scheduleExamSitting(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingSchedule) (*model.ExamSitting, error) {
	candidate := *input.Sitting
	disabledEligibilityRevision, err := lockDisabledExamSittingMailChronology(ctx, tx, input.ActorUserID, input.Mail)
	if err != nil {
		return nil, err
	}
	if err := lockExamSittingLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := guardExamSittingSelection(ctx, tx, candidate.ExamID, input.ActorUserID, input.ManagerOverride,
		candidate.ExamRevisionID, candidate.ClassID, candidate.ScheduledStartAt, candidate.ScheduledEndAt); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO exam_sittings (id,exam_id,exam_revision_id,class_id,scheduled_start_at,scheduled_end_at,
		state,created_at,updated_at,revision) VALUES (?,?,?,?,?,?,?,?,?,?)`, candidate.ID.String(), candidate.ExamID.String(),
		candidate.ExamRevisionID.String(), candidate.ClassID.String(), candidate.ScheduledStartAt, candidate.ScheduledEndAt,
		candidate.State, candidate.CreatedAt, candidate.UpdatedAt, candidate.Revision)
	if err != nil {
		return nil, fmt.Errorf("schedule Exam Sitting: %w", translateError("exam_sitting", candidate.ID.String(), err))
	}
	if err := insertExamSittingLifecycleJob(ctx, tx, input.OpenJob); err != nil {
		return nil, err
	}
	if err := insertExamSittingLifecycleJob(ctx, tx, input.DeadlineJob); err != nil {
		return nil, err
	}
	if err := insertExamSittingMailFanout(ctx, tx, input.Mail, &candidate, "", input.ActorUserID, disabledEligibilityRevision); err != nil {
		return nil, err
	}
	if err := completeExamSittingAudit(ctx, tx, &candidate, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func updateExamSittingSchedule(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingScheduleUpdate) (*model.ExamSitting, error) {
	disabledEligibilityRevision, err := lockDisabledExamSittingMailChronology(ctx, tx, input.ActorUserID, input.Mail)
	if err != nil {
		return nil, err
	}
	if err := lockExamSittingLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := guardExamSittingExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride); err != nil {
		return nil, err
	}
	current, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
	if err != nil {
		return nil, err
	}
	if current.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
	}
	if current.State != model.ExamSittingScheduled {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	priorClassID := current.ClassID
	if err := guardExamSittingLineage(ctx, tx, input.ExamID, input.ExamRevisionID, input.ClassID,
		input.ScheduledStartAt, input.ScheduledEndAt); err != nil {
		return nil, err
	}
	changed, err := current.ApplySchedule(input.ExamRevisionID, input.ClassID, input.ScheduledStartAt, input.ScheduledEndAt, input.ChangedAt)
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "schedule", nil).Wrap(err)
	}
	if !changed {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_no_changes", nil)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_sittings SET exam_revision_id=?,class_id=?,scheduled_start_at=?,scheduled_end_at=?,
		updated_at=?,revision=? WHERE exam_id=? AND id=? AND state='scheduled' AND revision=?`, current.ExamRevisionID.String(),
		current.ClassID.String(), current.ScheduledStartAt, current.ScheduledEndAt, current.UpdatedAt, current.Revision,
		current.ExamID.String(), current.ID.String(), input.ExpectedRevision)
	if err != nil {
		return nil, fmt.Errorf("update Exam Sitting: %w", translateError("exam_sitting", current.ID.String(), err))
	}
	if err := requireExamSittingAffected(result); err != nil {
		return nil, err
	}
	if err := insertExamSittingLifecycleJob(ctx, tx, input.OpenJob); err != nil {
		return nil, err
	}
	if err := insertExamSittingLifecycleJob(ctx, tx, input.DeadlineJob); err != nil {
		return nil, err
	}
	if err := insertExamSittingMailFanout(ctx, tx, input.Mail, current, priorClassID, input.ActorUserID, disabledEligibilityRevision); err != nil {
		return nil, err
	}
	if err := completeExamSittingAudit(ctx, tx, current, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return current, nil
}

func cancelExamSitting(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingCancellation) (*model.ExamSitting, error) {
	disabledEligibilityRevision, err := lockDisabledExamSittingMailChronology(ctx, tx, input.ActorUserID, input.Mail)
	if err != nil {
		return nil, err
	}
	if err := lockExamSittingLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := guardExamSittingExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride); err != nil {
		return nil, err
	}
	current, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
	if err != nil {
		return nil, err
	}
	if current.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
	}
	if current.State != model.ExamSittingScheduled {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if err := current.Cancel(model.ExamSittingReasonManagerCanceled, input.CanceledAt); err != nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "cancellation", nil).Wrap(err)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_sittings SET state=?,updated_at=?,canceled_at=?,reason_code=?,
		revision=? WHERE exam_id=? AND id=? AND state='scheduled' AND revision=?`, current.State,
		current.UpdatedAt, current.CanceledAt.Time, current.ReasonCode,
		current.Revision, current.ExamID.String(), current.ID.String(), input.ExpectedRevision)
	if err != nil {
		return nil, fmt.Errorf("cancel Exam Sitting: %w", translateError("exam_sitting", current.ID.String(), err))
	}
	if err := requireExamSittingAffected(result); err != nil {
		return nil, err
	}
	if err := insertExamSittingPrivateAction(ctx, tx, current, input.ActorUserID, "manager_canceled", input.PrivateReason, input.AuditEventID); err != nil {
		return nil, err
	}
	if err := insertExamSittingMailFanout(ctx, tx, input.Mail, current, current.ClassID, input.ActorUserID, disabledEligibilityRevision); err != nil {
		return nil, err
	}
	if err := completeExamSittingAudit(ctx, tx, current, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return current, nil
}

func insertExamSittingMailFanout(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingMailFanout,
	sitting *model.ExamSitting, priorClassID model.ClassID, actorID model.UserID, disabledEligibilityRevision int64,
) error {
	if input == nil {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	if sitting == nil || sitting.Validate() != nil || input.Occurrence == nil || input.ExpansionJob == nil ||
		input.DeliveryLifetime <= 0 || input.DeliveryLifetime > 72*time.Hour {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	wantKey := model.MailTemplateExamSittingScheduled
	switch input.ChangeKind {
	case store.ExamSittingMailScheduled:
		if sitting.Revision != 1 || sitting.State != model.ExamSittingScheduled {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailRescheduled:
		wantKey = model.MailTemplateExamSittingRescheduled
		if sitting.State != model.ExamSittingScheduled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailCancelled:
		wantKey = model.MailTemplateExamSittingCancelled
		if sitting.State != model.ExamSittingCanceled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	case store.ExamSittingMailReconciled:
		if sitting.State != model.ExamSittingScheduled || !priorClassID.IsValid() {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
	default:
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	if input.Occurrence.Validate() != nil || input.Occurrence.Kind != model.MailOccurrenceSittingSchedule ||
		input.Occurrence.TemplateKey != wantKey || input.Occurrence.ActorUserID != actorID || input.ExpansionJob.Validate() != nil ||
		input.ExpansionJob.Type != model.JobTypeMailExpandSitting || input.ExpansionJob.DedupePolicy != model.JobDedupePermanent ||
		input.ExpansionJob.MaximumAttempts != model.MailMaximumAttempts {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
	}
	command, err := model.DecodeSittingMailExpansionCommand(input.ExpansionJob.CommandVersion, input.ExpansionJob.Command)
	wantDedupe, keyErr := model.SittingMailExpansionDedupeKey(input.Occurrence.ID)
	if err != nil || keyErr != nil || command.OccurrenceID != input.Occurrence.ID || input.ExpansionJob.DedupeKey != wantDedupe {
		return store.NewErrInvalidInput("exam_sitting", "mail_fanout", err)
	}
	databaseNow, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return err
	}
	if err = supersedeActiveSittingMailFanouts(ctx, tx, sitting.ID, databaseNow); err != nil {
		return err
	}
	occurrence := *input.Occurrence
	occurrence.CreatedAt = databaseNow
	job := *input.ExpansionJob
	job.CreatedAt, job.UpdatedAt, job.AvailableAt = databaseNow, databaseNow, databaseNow
	deadline := databaseNow.Add(input.DeliveryLifetime)
	var bundleID any
	var completedAt any
	terminalReason := ""
	if input.Bundle == nil {
		if job.Status != model.JobStatusCanceled || !job.CompletedAt.Valid || disabledEligibilityRevision < 1 {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
		job.CompletedAt = model.OptionalTimeFrom(databaseNow)
		completedAt = databaseNow
		terminalReason = "suppressed_disabled"
	} else {
		if disabledEligibilityRevision != 0 || input.Bundle.Validate() != nil || input.Bundle.ID != occurrence.ID || job.Status != model.JobStatusQueued {
			return store.NewErrInvalidInput("exam_sitting", "mail_fanout", nil)
		}
		bundle := *input.Bundle
		bundle.CreatedAt = databaseNow
		payloadKeyID, payloadErr := mailPayloadKeyID(bundle.EncryptedPayload)
		if payloadErr != nil {
			return store.NewErrInvalidInput("mail_fanout_bundle", "encrypted_payload", payloadErr)
		}
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mail_fanout_bundles(id,payload_key_id,encrypted_payload,created_at,revision) VALUES(?,?,?,?,1)`,
			bundle.ID.String(), payloadKeyID, bundle.EncryptedPayload, databaseNow); err != nil {
			return fmt.Errorf("insert Sitting mail fan-out bundle: %w", translateError("mail_fanout_bundle", bundle.ID.String(), err))
		}
		if err = incrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
			return err
		}
		bundleID = bundle.ID.String()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mail_occurrences(id,kind,template_key,actor_user_id,created_at) VALUES(?,?,?,?,?)`,
		occurrence.ID.String(), occurrence.Kind, occurrence.TemplateKey, occurrence.ActorUserID.String(), databaseNow); err != nil {
		return fmt.Errorf("insert Sitting mail occurrence: %w", translateError("mail_occurrence", occurrence.ID.String(), err))
	}
	if err = insertPreparedMailJob(ctx, tx, &job); err != nil {
		return fmt.Errorf("insert Sitting mail expansion Job: %w", translateError("job", job.ID.String(), err))
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_sitting_mail_fanouts
		(occurrence_id,bundle_id,exam_sitting_id,sitting_revision,prior_class_id,change_kind,created_at,deadline,completed_at,terminal_reason)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, occurrence.ID.String(), bundleID, sitting.ID.String(), sitting.Revision, nullableID(priorClassID.String()),
		input.ChangeKind, databaseNow, deadline, completedAt, terminalReason); err != nil {
		return fmt.Errorf("insert Sitting mail fan-out: %w", translateError("exam_sitting_mail_fanout", occurrence.ID.String(), err))
	}
	if terminalReason == "suppressed_disabled" {
		var audienceRevision int64
		if updateErr := tx.Get(ctx, &audienceRevision, `SELECT mail_audience_revision FROM classes WHERE id=? FOR SHARE`,
			sitting.ClassID.String()); updateErr != nil {
			return fmt.Errorf("lock disabled Sitting mail audience: %w", updateErr)
		}
		result, updateErr := tx.Exec(ctx, `UPDATE exam_sittings SET mail_reconciliation_actor_user_id=?,
			mail_disabled_suppressed_revision=?,mail_disabled_suppressed_audience_revision=?,
			mail_disabled_suppressed_eligibility_revision=? WHERE id=? AND revision=?`,
			actorID.String(), sitting.Revision, audienceRevision, disabledEligibilityRevision, sitting.ID.String(), sitting.Revision)
		if updateErr != nil {
			return fmt.Errorf("record disabled Sitting mail convergence: %w", updateErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil || affected != 1 {
			return store.NewErrConflict("exam_sitting", "exam_sitting_revision", affectedErr)
		}
	} else {
		result, updateErr := tx.Exec(ctx, `UPDATE exam_sittings SET mail_reconciliation_actor_user_id=? WHERE id=? AND revision=?`,
			actorID.String(), sitting.ID.String(), sitting.Revision)
		if updateErr != nil {
			return fmt.Errorf("record Sitting mail reconciliation actor: %w", updateErr)
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil || affected != 1 {
			return store.NewErrConflict("exam_sitting", "exam_sitting_revision", affectedErr)
		}
	}
	return nil
}

// lockDisabledExamSittingMailChronology establishes the canonical ordering for
// a disabled-mail terminal fan-out: active Manager User, installation mail
// eligibility singleton, then (at the caller) Class and hierarchy. The
// captured revision is persisted only after those later authority checks.
func lockDisabledExamSittingMailChronology(ctx context.Context, tx *sqlxTxWrapper, actorID model.UserID,
	input *store.ExamSittingMailFanout,
) (int64, error) {
	if input == nil || input.Bundle != nil {
		return 0, nil
	}
	var activeActorID string
	if err := tx.Get(ctx, &activeActorID, `SELECT id FROM users
		WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`, actorID.String()); err != nil {
		if isNoRows(err) {
			return 0, store.NewErrConflict("exam_sitting_mail", "actor_unavailable", nil)
		}
		return 0, fmt.Errorf("lock disabled Sitting mail actor: %w", err)
	}
	return currentUserMailEligibilityRevision(ctx, tx)
}

func supersedeActiveSittingMailFanouts(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID, at time.Time) error {
	var rows []struct {
		OccurrenceID string         `db:"occurrence_id"`
		BundleID     sql.NullString `db:"bundle_id"`
		PayloadKeyID sql.NullString `db:"payload_key_id"`
		JobID        string         `db:"job_id"`
	}
	if err := tx.Select(ctx, &rows, `SELECT f.occurrence_id,f.bundle_id,b.payload_key_id,j.id job_id
		FROM exam_sitting_mail_fanouts f
		LEFT JOIN mail_fanout_bundles b ON b.id=f.bundle_id
		JOIN jobs j ON j.type='mail.expand_sitting' AND j.dedupe_key='sitting-mail:'||f.occurrence_id
		WHERE f.exam_sitting_id=? AND f.completed_at IS NULL ORDER BY f.occurrence_id FOR UPDATE OF f,j`, sittingID.String()); err != nil {
		return fmt.Errorf("lock superseded Sitting mail fan-outs: %w", err)
	}
	for _, row := range rows {
		jobID := model.JobID(row.JobID)
		job, err := getJob(ctx, tx, jobID, true)
		if err != nil {
			return err
		}
		if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
			transitionAt := model.TimeUTC(at)
			if transitionAt.Before(job.UpdatedAt) {
				transitionAt = job.UpdatedAt
			}
			updated, cancelErr := job.RequestCancellation(transitionAt)
			if cancelErr != nil {
				return invalidPersistedState("job", "sitting_mail_supersession", cancelErr)
			}
			if err = updateJob(ctx, tx, updated); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_sitting_mail_fanouts SET bundle_id=NULL,completed_at=?,terminal_reason='superseded'
			WHERE occurrence_id=? AND completed_at IS NULL`, model.TimeUTC(at), row.OccurrenceID); err != nil {
			return fmt.Errorf("complete superseded Sitting mail fan-out: %w", err)
		}
		if row.BundleID.Valid {
			if _, err = tx.Exec(ctx, `DELETE FROM mail_fanout_bundles WHERE id=?`, row.BundleID.String); err != nil {
				return fmt.Errorf("destroy superseded Sitting mail bundle: %w", err)
			}
			if !row.PayloadKeyID.Valid {
				return invalidPersistedState("mail_fanout_bundle", "payload_key_id", errors.New("missing payload key"))
			}
			if err = decrementMailPayloadKeyReference(ctx, tx, row.PayloadKeyID.String); err != nil {
				return err
			}
		}
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
	actionCode, privateReason, auditEventID string,
) error {
	_, err := tx.Exec(ctx, `INSERT INTO exam_sitting_private_actions
		(audit_event_id,exam_sitting_id,actor_user_id,action_code,private_reason,created_at,sitting_revision)
		VALUES (?,?,?,?,?,?,?)`, auditEventID, sitting.ID.String(), actorID.String(), actionCode, privateReason,
		sitting.UpdatedAt, sitting.Revision)
	if err != nil {
		return fmt.Errorf("insert Exam Sitting private action: %w", translateError("exam_sitting_private_action", auditEventID, err))
	}
	return nil
}

func (s sqlExamSittingStore) Pause(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if err := prepareExamSittingManagerTransition(input, false); err != nil {
		return nil, err
	}
	return s.runManagerLifecycleMutation(ctx, "Exam Sitting pause", input, command, store.ExamSittingTransitionManagerPaused, true,
		func(current *model.ExamSitting, at time.Time) error { return current.Pause(at) })
}

func (s sqlExamSittingStore) Resume(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if err := prepareExamSittingManagerTransition(input, false); err != nil {
		return nil, err
	}
	return s.runManagerLifecycleMutation(ctx, "Exam Sitting resume", input, command, store.ExamSittingTransitionManagerResumed, false,
		func(current *model.ExamSitting, at time.Time) error { return current.Resume(at) })
}

func (s sqlExamSittingStore) EarlyClose(ctx context.Context, input *store.ExamSittingManagerTransition, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
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
		input.ExpectedRevision < 1 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		!validExamSittingPrivateReason(input.PrivateReason) || (requireFinalize != (input.FinalizeJob != nil)) {
		return store.NewErrInvalidInput("exam_sitting", "manager_transition", nil)
	}
	return nil
}

func validExamSittingPrivateReason(reason string) bool {
	return utf8.ValidString(reason) && reason == strings.TrimSpace(reason) && utf8.RuneCountInString(reason) >= 1 &&
		utf8.RuneCountInString(reason) <= 1000 && len(reason) <= 4000
}

func (s sqlExamSittingStore) runManagerLifecycleMutation(ctx context.Context, label string, input *store.ExamSittingManagerTransition,
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
		encode: func(sitting *model.ExamSitting) ([]byte, error) { return encodeCommandOutcome(sitting) },
		decode: func(version int, data []byte) (*model.ExamSitting, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Exam Sitting lifecycle outcome version %d", version)
			}
			var sitting model.ExamSitting
			if err := decodeCommandOutcome(data, &sitting); err != nil {
				return nil, err
			}
			if err := sitting.Validate(); err != nil {
				return nil, err
			}
			return &sitting, nil
		},
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

func (s sqlExamSittingStore) Extend(ctx context.Context, input *store.ExamSittingExtension, command *store.CommandIdempotency) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() ||
		input.ExpectedRevision < 1 || input.ScheduledEndAt.IsZero() || input.ChangedAt.IsZero() || input.DeadlineJob == nil ||
		!validExamSittingPrivateReason(input.PrivateReason) || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || command == nil {
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
		encode: func(sitting *model.ExamSitting) ([]byte, error) { return encodeCommandOutcome(sitting) },
		decode: decodeExamSittingLifecycleOutcome,
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

func decodeExamSittingLifecycleOutcome(version int, data []byte) (*model.ExamSitting, error) {
	if version != 1 {
		return nil, fmt.Errorf("unsupported Exam Sitting lifecycle outcome version %d", version)
	}
	var sitting model.ExamSitting
	if err := decodeCommandOutcome(data, &sitting); err != nil {
		return nil, err
	}
	if err := sitting.Validate(); err != nil {
		return nil, err
	}
	return &sitting, nil
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

func guardExamSittingStructure(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, proposedEnd time.Time, requireFutureEnd bool) error {
	valid, contained, future, err := examSittingStructureStatus(ctx, tx, sitting, proposedEnd)
	if err != nil {
		return err
	}
	if !valid {
		return store.NewErrConflict("exam_sitting", "exam_sitting_class_lineage", nil)
	}
	if !contained {
		return store.NewErrConflict("exam_sitting", "exam_sitting_period_containment", nil)
	}
	if requireFutureEnd && !future {
		return store.NewErrConflict("exam_sitting", "exam_sitting_not_future", nil)
	}
	return nil
}

func examSittingStructureStatus(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, proposedEnd time.Time) (bool, bool, bool, error) {
	var status struct {
		SameUnit  bool `db:"same_unit"`
		Contained bool `db:"contained"`
		Future    bool `db:"future"`
	}
	err := tx.Get(ctx, &status, `SELECT p.academic_unit_id=e.academic_unit_id AS same_unit,
		(? >= ap.start_at AND ? <= ap.end_at) AS contained,(? > statement_timestamp()) AS future
		FROM classes c JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id JOIN exams e ON e.id=?
		JOIN exam_revisions r ON r.exam_id=e.id AND r.id=? AND r.sealed=true
		WHERE c.id=? AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL AND e.archived_at IS NULL
		AND ap.institution_id=au.institution_id FOR UPDATE OF c,pl,p,au,ap`, sitting.ScheduledStartAt,
		model.TimeUTC(proposedEnd), model.TimeUTC(proposedEnd), sitting.ExamID.String(), sitting.ExamRevisionID.String(), sitting.ClassID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, nil
	}
	if err != nil {
		return false, false, false, fmt.Errorf("validate current Exam Sitting structure: %w", err)
	}
	return status.SameUnit, status.Contained, status.Future, nil
}

func (s sqlExamSittingStore) AdvanceDue(ctx context.Context, input *store.ExamSittingDueAdvance) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.SittingID.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_sitting", "advance_due", nil)
	}
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "advance due Exam Sitting", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingLifecycleResult, error) {
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
		transition := store.ExamSittingLifecycleTransitionCode("")
		changed := false
		expectedRevision := current.Revision
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
				return nil, err
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
				return nil, store.NewErrInvalidInput("exam_sitting", "finalize_job", nil)
			}
			if err = validateExamSittingLifecycleJob(input.FinalizeJob, current.ID, model.ExamSittingLifecycleJobFinalize,
				current.Revision+1, current.ScheduledEndAt); err != nil {
				if isStaleExamSittingFinalizeJob(input.FinalizeJob, current) {
					return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", err)
				}
				return nil, err
			}
			err = current.EnterClosing(model.ExamSittingReasonScheduledEndReached, now)
			transition = store.ExamSittingTransitionScheduledEndReached
			changed = true
		case model.ExamSittingClosing:
			if input.FinalizeJob == nil || !current.ClosingAt.Valid {
				return nil, store.NewErrInvalidInput("exam_sitting", "finalize_job", nil)
			}
			if err = validateExamSittingLifecycleJob(input.FinalizeJob, current.ID,
				model.ExamSittingLifecycleJobFinalize, current.Revision, current.ClosingAt.Time); err != nil {
				return nil, err
			}
		}
		if err != nil {
			return nil, invalidPersistedState("exam_sitting", "lifecycle_transition", err)
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
	})
	if err != nil {
		return nil, err
	}
	if err := s.completeCommittedSnapshot(ctx, result.Value); err != nil {
		return nil, err
	}
	return result, nil
}

func isStaleExamSittingFinalizeJob(job *model.Job, current *model.ExamSitting) bool {
	if job == nil || current == nil || job.Validate() != nil || job.Type != model.JobTypeExamSittingSealing ||
		job.Status != model.JobStatusQueued || job.DedupePolicy != model.JobDedupeActive || job.AttemptCount != 0 ||
		job.Revision != 1 || job.WorkReserved != 0 || job.StartedAt.Valid || job.CompletedAt.Valid || job.Progress != nil ||
		job.CheckpointVersion != 0 || len(job.Checkpoint) != 0 || job.ResultVersion != 0 || len(job.Result) != 0 ||
		job.PublicErrorCode != "" || !job.AvailableAt.Equal(current.ScheduledEndAt) {
		return false
	}
	command, err := model.DecodeExamSittingLifecycleCommand(job.CommandVersion, job.Command)
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

func (s sqlExamSittingStore) FinishSealing(ctx context.Context, input *store.ExamSittingFinishSealing) (*store.ExamSittingLifecycleResult, error) {
	if input == nil || !input.SittingID.IsValid() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
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

func (s sqlExamSittingStore) ListNoShows(ctx context.Context, options store.ExamSittingNoShowListOptions) ([]store.ExamSittingNoShow, error) {
	if !options.SittingID.IsValid() || (!options.AfterCandidateUserID.IsZero() && !options.AfterCandidateUserID.IsValid()) ||
		options.Limit < 1 || options.Limit > 201 {
		return nil, store.NewErrInvalidInput("exam_sitting", "no_show_list", nil)
	}
	var rows []string
	if err := s.GetMaster().Select(ctx, &rows, `SELECT DISTINCT cm.user_id
		FROM exam_sittings s JOIN class_members cm ON cm.class_id=s.class_id
		WHERE s.id=? AND s.state IN ('closing','closed') AND s.opened_at IS NOT NULL
		AND cm.start_at<=s.opened_at AND (cm.end_at IS NULL OR cm.end_at>s.opened_at)
		AND (cm.archived_at IS NULL OR cm.archived_at>s.opened_at) AND cm.user_id>?
		AND NOT EXISTS (SELECT 1 FROM exam_attempts a WHERE a.exam_sitting_id=s.id AND a.candidate_user_id=cm.user_id)
		ORDER BY cm.user_id LIMIT ?`, options.SittingID.String(), options.AfterCandidateUserID.String(), options.Limit); err != nil {
		return nil, fmt.Errorf("list Exam Sitting no-shows: %w", err)
	}
	result := make([]store.ExamSittingNoShow, 0, len(rows))
	for _, raw := range rows {
		candidateID, err := model.ParseUserID(raw)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting_no_show", "candidate_user_id", err)
		}
		result = append(result, store.ExamSittingNoShow{CandidateUserID: candidateID})
	}
	return result, nil
}

// snapshotFromCommittedOutcome preserves the exact mutation value stored in
// the idempotency outcome. Only the Exam's immutable Academic Unit identity is
// resolved after commit; re-reading the Sitting here would allow a concurrent
// later transition to replace the command's own result.
func (s sqlExamSittingStore) snapshotFromCommittedOutcome(ctx context.Context, sitting *model.ExamSitting) (*store.ExamSittingSnapshot, error) {
	snapshot := &store.ExamSittingSnapshot{Sitting: sitting}
	if err := s.completeCommittedSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s sqlExamSittingStore) completeCommittedSnapshot(ctx context.Context, snapshot *store.ExamSittingSnapshot) error {
	if snapshot == nil || snapshot.Sitting == nil {
		return invalidPersistedState("exam_sitting", "outcome", errors.New("missing committed Sitting"))
	}
	var raw string
	if err := s.GetMaster().Get(ctx, &raw, `SELECT academic_unit_id FROM exams WHERE id=?`, snapshot.Sitting.ExamID.String()); err != nil {
		return translateError("exam", snapshot.Sitting.ExamID.String(), err)
	}
	unitID, err := model.ParseAcademicUnitID(raw)
	if err != nil {
		return invalidPersistedState("exam_sitting", "academic_unit_id", err)
	}
	snapshot.AcademicUnitID = unitID
	return nil
}

func resolveExamSittingExamID(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID) (model.ExamID, error) {
	var raw string
	if err := tx.Get(ctx, &raw, `SELECT exam_id FROM exam_sittings WHERE id=?`, sittingID.String()); err != nil {
		return "", translateError("exam_sitting", sittingID.String(), err)
	}
	id, err := model.ParseExamID(raw)
	if err != nil {
		return "", invalidPersistedState("exam_sitting", "exam_id", err)
	}
	return id, nil
}

func lockExamSittingSystemExam(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID) (bool, error) {
	var archived sql.NullTime
	if err := tx.Get(ctx, &archived, `SELECT archived_at FROM exams WHERE id=? FOR UPDATE`, examID.String()); err != nil {
		return false, translateError("exam", examID.String(), err)
	}
	return archived.Valid, nil
}

func completeExamSittingLifecycleAudit(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting,
	transition store.ExamSittingLifecycleTransitionCode, changed, replayed bool, originalAuditID, auditID string, auditAt int64,
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

func lockExamSitting(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, id model.ExamSittingID) (*model.ExamSitting, error) {
	var row examSittingRow
	if err := tx.Get(ctx, &row, examSittingSelect+` WHERE exam_id=? AND id=? FOR UPDATE`, examID.String(), id.String()); err != nil {
		return nil, translateError("exam_sitting", id.String(), err)
	}
	return row.model()
}

type examSittingExamGuard struct {
	ArchivedAt     sql.NullTime `db:"archived_at"`
	ActorIsManager bool         `db:"actor_is_manager"`
}

func guardExamSittingExam(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, actorID model.UserID, override bool) error {
	return guardExamSittingManagerExam(ctx, tx, examID, actorID, override, false)
}

func guardExamSittingManagerExam(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, actorID model.UserID,
	override, allowArchived bool,
) error {
	var guard examSittingExamGuard
	if err := tx.Get(ctx, &guard, `SELECT e.archived_at,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=e.id AND m.user_id=?) AS actor_is_manager
		FROM exams e WHERE e.id=? FOR UPDATE OF e`, actorID.String(), examID.String()); err != nil {
		return translateError("exam", examID.String(), err)
	}
	if !guard.ActorIsManager && !override {
		return store.NewErrNotFound("exam_manager", actorID.String())
	}
	if guard.ArchivedAt.Valid && !allowArchived {
		return store.NewErrConflict("exam", "exam_archived", nil)
	}
	return nil
}

func guardExamSittingSelection(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, actorID model.UserID, override bool,
	revisionID model.ExamRevisionID, classID model.ClassID, startAt, endAt time.Time,
) error {
	if err := guardExamSittingExam(ctx, tx, examID, actorID, override); err != nil {
		return err
	}
	return guardExamSittingLineage(ctx, tx, examID, revisionID, classID, startAt, endAt)
}

func guardExamSittingLineage(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID,
	revisionID model.ExamRevisionID, classID model.ClassID, startAt, endAt time.Time,
) error {
	var revision struct {
		Exists bool `db:"exists"`
	}
	if err := tx.Get(ctx, &revision, `SELECT EXISTS (SELECT 1 FROM exam_revisions WHERE exam_id=? AND id=? AND sealed=true) AS exists`,
		examID.String(), revisionID.String()); err != nil {
		return err
	}
	if !revision.Exists {
		return store.NewErrConflict("exam_sitting", "exam_sitting_revision_lineage", nil)
	}
	var lineage struct {
		Exists      bool `db:"exists"`
		SameUnit    bool `db:"same_unit"`
		Contained   bool `db:"contained"`
		FutureStart bool `db:"future_start"`
	}
	err := tx.Get(ctx, &lineage, `SELECT true AS exists,p.academic_unit_id=e.academic_unit_id AS same_unit,
		(? >= ap.start_at AND ? <= ap.end_at) AS contained,(? > statement_timestamp()) AS future_start
		FROM classes c JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id JOIN exams e ON e.id=?
		WHERE c.id=? AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL AND ap.institution_id=au.institution_id
		FOR UPDATE OF c,pl,p,au,ap`, model.TimeUTC(startAt), model.TimeUTC(endAt), model.TimeUTC(startAt), examID.String(), classID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return store.NewErrConflict("exam_sitting", "exam_sitting_class_lineage", nil)
	}
	if err != nil {
		return fmt.Errorf("validate Exam Sitting selection: %w", err)
	}
	if !lineage.SameUnit {
		return store.NewErrConflict("exam_sitting", "exam_sitting_class_lineage", nil)
	}
	if !lineage.Contained {
		return store.NewErrConflict("exam_sitting", "exam_sitting_period_containment", nil)
	}
	if !lineage.FutureStart {
		return store.NewErrConflict("exam_sitting", "exam_sitting_not_future", nil)
	}
	return nil
}

func requireExamSittingAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Exam Sitting mutation: %w", err)
	}
	if affected != 1 {
		return store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
	}
	return nil
}

func completeExamSittingAudit(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, auditID string, auditAt int64) error {
	encoded, err := encodeExamSittingAudit(sitting, false, "")
	if err != nil {
		return err
	}
	if _, err := completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Exam Sitting audit: %w", err)
	}
	return nil
}

func encodeExamSittingAudit(sitting *model.ExamSitting, replayed bool, originalAuditID string) ([]byte, error) {
	data := map[string]any{
		"exam_id": sitting.ExamID.String(), "exam_sitting_id": sitting.ID.String(),
		"exam_revision_id": sitting.ExamRevisionID.String(), "class_id": sitting.ClassID.String(),
		"scheduled_start_at": sitting.ScheduledStartAt, "scheduled_end_at": sitting.ScheduledEndAt,
		"state": sitting.State, "reason_code": sitting.ReasonCode, "revision": sitting.Revision,
	}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	return model.EncodeAuditData(data)
}
