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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// SQLExamSittingStore owns the PostgreSQL representation of scheduled Exam
// delivery. Its mutations deliberately acquire the academic lifecycle locks
// in the same global order as the catalog Stores before locking Exam rows.
type SQLExamSittingStore struct{ *SQLStore }

func newSQLExamSittingStore(sqlStore *SQLStore) store.ExamSittingStore {
	return &SQLExamSittingStore{SQLStore: sqlStore}
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

// Resolve returns the minimal Sitting snapshot needed to map an authorization
// resource to its Exam, or a not-found error.
func (s SQLExamSittingStore) Resolve(ctx context.Context, id model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	if !id.IsValid() {
		return nil, store.NewErrInvalidInput("exam_sitting", "id", nil)
	}
	var row examSittingRow
	if err := s.GetMaster().Get(ctx, &row, examSittingSelect+` WHERE id=?`, id.String()); err != nil {
		return nil, translateError("exam_sitting", id.String(), err)
	}
	return examSittingSnapshot(row)
}

// Get returns the Sitting of examID with id, or a not-found error.
func (s SQLExamSittingStore) Get(ctx context.Context, examID model.ExamID, id model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
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

// List returns up to Limit (at most 201) Sittings of the Exam in descending
// scheduled start order, optionally filtered by Class, state, overlap window,
// and the Before keyset cursor.
func (s SQLExamSittingStore) List(ctx context.Context, options store.ExamSittingListOptions) ([]store.ExamSittingSnapshot, error) {
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

// ListInvalidationTargetsByClass returns up to limit (1..201) Exam and Sitting
// selectors of the Class's Sittings, ordered by Sitting ID after the exclusive
// afterSittingID cursor.
func (s SQLExamSittingStore) ListInvalidationTargetsByClass(
	ctx context.Context,
	classID model.ClassID,
	afterSittingID model.ExamSittingID,
	limit int,
) ([]store.ExamSittingInvalidationTarget, error) {
	if !classID.IsValid() || limit < 1 || limit > 201 {
		return nil, store.NewErrInvalidInput("exam_sitting", "class_invalidation_targets", nil)
	}
	query := `SELECT exam_id,id FROM exam_sittings WHERE class_id=?`
	args := []any{classID.String()}
	if afterSittingID.IsValid() {
		query += ` AND id>?`
		args = append(args, afterSittingID.String())
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	var rows []struct {
		ExamID    string `db:"exam_id"`
		SittingID string `db:"id"`
	}
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Class Exam Sitting invalidation targets: %w", err)
	}
	targets := make([]store.ExamSittingInvalidationTarget, 0, len(rows))
	for _, row := range rows {
		examID, err := model.ParseExamID(row.ExamID)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting", "exam_id", err)
		}
		sittingID, err := model.ParseExamSittingID(row.SittingID)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting", "id", err)
		}
		targets = append(targets, store.ExamSittingInvalidationTarget{ExamID: examID, SittingID: sittingID})
	}
	return targets, nil
}

// ListInvalidationTargetsByExam returns up to limit (1..201) Sitting selectors
// of the Exam, ordered by Sitting ID after the exclusive afterSittingID cursor.
func (s SQLExamSittingStore) ListInvalidationTargetsByExam(
	ctx context.Context,
	examID model.ExamID,
	afterSittingID model.ExamSittingID,
	limit int,
) ([]store.ExamSittingInvalidationTarget, error) {
	if !examID.IsValid() || limit < 1 || limit > 201 {
		return nil, store.NewErrInvalidInput("exam_sitting", "exam_invalidation_targets", nil)
	}
	query := `SELECT exam_id,id FROM exam_sittings WHERE exam_id=?`
	args := []any{examID.String()}
	if afterSittingID.IsValid() {
		query += ` AND id>?`
		args = append(args, afterSittingID.String())
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)
	var rows []struct {
		ExamID    string `db:"exam_id"`
		SittingID string `db:"id"`
	}
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Sitting invalidation targets: %w", err)
	}
	targets := make([]store.ExamSittingInvalidationTarget, 0, len(rows))
	for _, row := range rows {
		rowExamID, err := model.ParseExamID(row.ExamID)
		if err != nil || rowExamID != examID {
			return nil, invalidPersistedState("exam_sitting", "exam_id", err)
		}
		sittingID, err := model.ParseExamSittingID(row.SittingID)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting", "id", err)
		}
		targets = append(targets, store.ExamSittingInvalidationTarget{ExamID: rowExamID, SittingID: sittingID})
	}
	return targets, nil
}

// ListCandidateInvalidationTargetsBySitting returns up to limit (1..201)
// distinct Users whose candidate activity can include the Sitting, meaning any
// historical Class Member or Candidate with an Attempt, ordered by User ID
// after afterUserID.
func (s SQLExamSittingStore) ListCandidateInvalidationTargetsBySitting(
	ctx context.Context,
	sittingID model.ExamSittingID,
	afterUserID model.UserID,
	limit int,
) ([]model.UserID, error) {
	if !sittingID.IsValid() || limit < 1 || limit > 201 {
		return nil, store.NewErrInvalidInput("exam_sitting", "candidate_invalidation_targets", nil)
	}
	query := `
		WITH candidates AS (
			SELECT member.user_id
			  FROM exam_sittings sitting
			  JOIN class_members member ON member.class_id=sitting.class_id
			 WHERE sitting.id=?
			UNION
			SELECT attempt.candidate_user_id
			  FROM exam_attempts attempt
			 WHERE attempt.exam_sitting_id=?
		)
		SELECT user_id FROM candidates`
	args := []any{sittingID.String(), sittingID.String()}
	if afterUserID.IsValid() {
		query += ` WHERE user_id>?`
		args = append(args, afterUserID.String())
	}
	query += ` ORDER BY user_id LIMIT ?`
	args = append(args, limit)
	var rows []string
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list candidate Exam activity invalidation targets: %w", err)
	}
	targets := make([]model.UserID, 0, len(rows))
	for _, row := range rows {
		userID, err := model.ParseUserID(row)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting", "candidate_user_id", err)
		}
		targets = append(targets, userID)
	}
	return targets, nil
}

// ListLifecycleDue returns up to Limit (1..201) Sittings whose next lifecycle
// transition is due, with the instant it became due, after the optional keyset
// cursor.
func (s SQLExamSittingStore) ListLifecycleDue(ctx context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
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

// Schedule creates a scheduled Sitting under command and returns the retained
// result on replay.
func (s SQLExamSittingStore) Schedule(ctx context.Context, input *store.ExamSittingSchedule, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
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
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
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

// UpdateSchedule changes a scheduled Sitting's Exam Revision, Class, or
// schedule under command, fenced on ExpectedRevision, and returns the retained
// result on replay.
func (s SQLExamSittingStore) UpdateSchedule(ctx context.Context, input *store.ExamSittingScheduleUpdate, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
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
		!input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
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

// Cancel cancels a scheduled Sitting at database time under command, fenced on
// ExpectedRevision, and returns the retained result on replay.
func (s SQLExamSittingStore) Cancel(ctx context.Context, input *store.ExamSittingCancellation, command *store.CommandIdempotency) (*store.ExamSittingCommandResult, error) {
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
		input.ExpectedRevision < 1 || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() ||
		!utf8.ValidString(input.PrivateReason) || input.PrivateReason != strings.TrimSpace(input.PrivateReason) ||
		utf8.RuneCountInString(input.PrivateReason) < 1 || utf8.RuneCountInString(input.PrivateReason) > 1000 {
		return store.NewErrInvalidInput("exam_sitting", "cancellation", nil)
	}
	return nil
}

func (s SQLExamSittingStore) runExamSittingMutation(ctx context.Context, label string, auditID model.AuditEventID, auditAt time.Time, command *store.CommandIdempotency,
	execute func(context.Context, *sqlxTxWrapper) (*model.ExamSitting, error),
) (*store.ExamSittingCommandResult, error) {
	if command == nil {
		return nil, store.NewErrInvalidInput("exam_sitting", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, label, idempotentMutation[*model.ExamSitting]{
		command: command, auditEventID: auditID, execute: execute,
		encode: encodeExamSittingOutcome,
		decode: decodeExamSittingOutcome,
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
	changedAt, err := examSittingStatementTime(ctx, tx)
	if err != nil {
		return nil, err
	}
	changed, err := current.ApplySchedule(input.ExamRevisionID, input.ClassID, input.ScheduledStartAt, input.ScheduledEndAt, changedAt)
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
	canceledAt, err := examSittingStatementTime(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err = current.Cancel(model.ExamSittingReasonManagerCanceled, canceledAt); err != nil {
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

// examSittingDocument spells model.ExamSitting inside persisted command
// outcomes.
type examSittingDocument struct {
	ID               model.ExamSittingID         `json:"id"`
	ExamID           model.ExamID                `json:"exam_id"`
	ExamRevisionID   model.ExamRevisionID        `json:"exam_revision_id"`
	ClassID          model.ClassID               `json:"class_id"`
	ScheduledStartAt time.Time                   `json:"scheduled_start_at"`
	ScheduledEndAt   time.Time                   `json:"scheduled_end_at"`
	State            model.ExamSittingState      `json:"state"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
	OpenedAt         model.OptionalTime          `json:"opened_at"`
	PausedAt         model.OptionalTime          `json:"paused_at"`
	ClosingAt        model.OptionalTime          `json:"closing_at"`
	ClosedAt         model.OptionalTime          `json:"closed_at"`
	CanceledAt       model.OptionalTime          `json:"canceled_at"`
	ReasonCode       model.ExamSittingReasonCode `json:"reason_code,omitempty"`
	Revision         int64                       `json:"revision"`
}

func newExamSittingDocument(sitting *model.ExamSitting) examSittingDocument {
	return examSittingDocument{
		ID: sitting.ID, ExamID: sitting.ExamID, ExamRevisionID: sitting.ExamRevisionID, ClassID: sitting.ClassID,
		ScheduledStartAt: sitting.ScheduledStartAt, ScheduledEndAt: sitting.ScheduledEndAt, State: sitting.State,
		CreatedAt: sitting.CreatedAt, UpdatedAt: sitting.UpdatedAt, OpenedAt: sitting.OpenedAt, PausedAt: sitting.PausedAt,
		ClosingAt: sitting.ClosingAt, ClosedAt: sitting.ClosedAt, CanceledAt: sitting.CanceledAt,
		ReasonCode: sitting.ReasonCode, Revision: sitting.Revision,
	}
}

// model returns the validated Sitting the document spells.
func (d examSittingDocument) model() (*model.ExamSitting, error) {
	sitting := &model.ExamSitting{
		ID: d.ID, ExamID: d.ExamID, ExamRevisionID: d.ExamRevisionID, ClassID: d.ClassID,
		ScheduledStartAt: d.ScheduledStartAt, ScheduledEndAt: d.ScheduledEndAt, State: d.State,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, OpenedAt: d.OpenedAt, PausedAt: d.PausedAt,
		ClosingAt: d.ClosingAt, ClosedAt: d.ClosedAt, CanceledAt: d.CanceledAt,
		ReasonCode: d.ReasonCode, Revision: d.Revision,
	}
	if err := sitting.Validate(); err != nil {
		return nil, fmt.Errorf("decode Exam Sitting outcome: %w", err)
	}
	return sitting, nil
}

// examSittingOutcome is the persisted outcome of an idempotent Sitting
// schedule or lifecycle command: the Sitting as committed.
type examSittingOutcome struct {
	Sitting examSittingDocument `json:"sitting"`
}

func (examSittingOutcome) outcomeDocument() {}

func encodeExamSittingOutcome(sitting *model.ExamSitting) ([]byte, error) {
	return encodeOutcome(examSittingOutcome{Sitting: newExamSittingDocument(sitting)})
}

func decodeExamSittingOutcome(data []byte) (*model.ExamSitting, error) {
	var outcome examSittingOutcome
	if err := decodeOutcome(data, &outcome); err != nil {
		return nil, err
	}
	return outcome.Sitting.model()
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

// ListNoShows returns up to Limit (1..201) Class Members of a Closing or Closed
// Sitting who were members when it opened but never started an Attempt, ordered
// by User ID after AfterCandidateUserID.
func (s SQLExamSittingStore) ListNoShows(ctx context.Context, options store.ExamSittingNoShowListOptions) ([]store.ExamSittingNoShow, error) {
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
func (s SQLExamSittingStore) snapshotFromCommittedOutcome(ctx context.Context, sitting *model.ExamSitting) (*store.ExamSittingSnapshot, error) {
	snapshot := &store.ExamSittingSnapshot{Sitting: sitting}
	if err := s.completeCommittedSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s SQLExamSittingStore) completeCommittedSnapshot(ctx context.Context, snapshot *store.ExamSittingSnapshot) error {
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

func completeExamSittingAudit(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting, auditID model.AuditEventID, auditAt time.Time) error {
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

// examSittingStatementTime is the authoritative time for manager schedule
// and cancellation decisions, matching the system lifecycle transitions so
// an application node's clock never stamps durable Sitting state.
func examSittingStatementTime(ctx context.Context, tx *sqlxTxWrapper) (time.Time, error) {
	var now time.Time
	if err := tx.Get(ctx, &now, `SELECT statement_timestamp()`); err != nil {
		return time.Time{}, fmt.Errorf("read Exam Sitting decision time: %w", err)
	}
	return model.TimeUTC(now), nil
}
