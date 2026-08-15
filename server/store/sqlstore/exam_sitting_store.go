// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
}

const examSittingSelect = `SELECT id,exam_id,exam_revision_id,class_id,scheduled_start_at,scheduled_end_at,
	state,created_at,updated_at,opened_at,paused_at,closing_at,closed_at,canceled_at,reason_code,revision
	FROM exam_sittings`

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
	return &store.ExamSittingSnapshot{Sitting: sitting}, nil
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
	if input == nil || input.Sitting == nil || !input.ActorUserID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_sitting", "schedule", nil)
	}
	if err := input.Sitting.Validate(); err != nil || input.Sitting.State != model.ExamSittingScheduled || input.Sitting.Revision != 1 {
		return store.NewErrInvalidInput("exam_sitting", "value", nil).Wrap(err)
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
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() ||
		!input.ExamRevisionID.IsValid() || !input.ClassID.IsValid() || input.ExpectedRevision < 1 ||
		input.ScheduledStartAt.IsZero() || input.ScheduledEndAt.IsZero() || !input.ScheduledStartAt.Before(input.ScheduledEndAt) ||
		input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_sitting", "schedule_update", nil)
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
	if input == nil || !input.ExamID.IsValid() || !input.SittingID.IsValid() || !input.ActorUserID.IsValid() ||
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
	return &store.ExamSittingCommandResult{Value: &store.ExamSittingSnapshot{Sitting: result.Value}, Replayed: result.Replayed}, nil
}

func scheduleExamSitting(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingSchedule) (*model.ExamSitting, error) {
	candidate := *input.Sitting
	if err := lockExamSittingLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := guardExamSittingSelection(ctx, tx, candidate.ExamID, input.ActorUserID, input.ManagerOverride,
		candidate.ExamRevisionID, candidate.ClassID, candidate.ScheduledStartAt, candidate.ScheduledEndAt); err != nil {
		return nil, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO exam_sittings (id,exam_id,exam_revision_id,class_id,scheduled_start_at,scheduled_end_at,
		state,created_at,updated_at,revision) VALUES (?,?,?,?,?,?,?,?,?,?)`, candidate.ID.String(), candidate.ExamID.String(),
		candidate.ExamRevisionID.String(), candidate.ClassID.String(), candidate.ScheduledStartAt, candidate.ScheduledEndAt,
		candidate.State, candidate.CreatedAt, candidate.UpdatedAt, candidate.Revision)
	if err != nil {
		return nil, fmt.Errorf("schedule Exam Sitting: %w", translateError("exam_sitting", candidate.ID.String(), err))
	}
	if err := completeExamSittingAudit(ctx, tx, &candidate, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func updateExamSittingSchedule(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingScheduleUpdate) (*model.ExamSitting, error) {
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
	if err := completeExamSittingAudit(ctx, tx, current, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return current, nil
}

func cancelExamSitting(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamSittingCancellation) (*model.ExamSitting, error) {
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
	result, err := tx.Exec(ctx, `UPDATE exam_sittings SET state=?,updated_at=?,canceled_at=?,reason_code=?,canceled_by_user_id=?,
		cancellation_private_reason=?,revision=? WHERE exam_id=? AND id=? AND state='scheduled' AND revision=?`, current.State,
		current.UpdatedAt, current.CanceledAt.Time, current.ReasonCode, input.ActorUserID.String(), input.PrivateReason,
		current.Revision, current.ExamID.String(), current.ID.String(), input.ExpectedRevision)
	if err != nil {
		return nil, fmt.Errorf("cancel Exam Sitting: %w", translateError("exam_sitting", current.ID.String(), err))
	}
	if err := requireExamSittingAffected(result); err != nil {
		return nil, err
	}
	if err := completeExamSittingAudit(ctx, tx, current, input.AuditEventID, input.AuditAt); err != nil {
		return nil, err
	}
	return current, nil
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
	var guard examSittingExamGuard
	if err := tx.Get(ctx, &guard, `SELECT e.archived_at,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=e.id AND m.user_id=?) AS actor_is_manager
		FROM exams e WHERE e.id=? FOR UPDATE OF e`, actorID.String(), examID.String()); err != nil {
		return translateError("exam", examID.String(), err)
	}
	if !guard.ActorIsManager && !override {
		return store.NewErrNotFound("exam_manager", actorID.String())
	}
	if guard.ArchivedAt.Valid {
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
