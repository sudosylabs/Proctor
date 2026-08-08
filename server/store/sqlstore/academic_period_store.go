// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing institution-wide
// academic periods.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAcademicPeriodStore struct {
	*SqlStore
	academicPeriodsQuery sq.SelectBuilder
}

const academicPeriodLifecycleLock = "proctor:academic-period-lifecycle"

type academicPeriodRow struct {
	ID            string       `db:"id"`
	CreatedAt     time.Time    `db:"created_at"`
	UpdatedAt     time.Time    `db:"updated_at"`
	ArchivedAt    sql.NullTime `db:"archived_at"`
	Revision      int64        `db:"revision"`
	InstitutionID string       `db:"institution_id"`
	Name          string       `db:"name"`
	DisplayName   string       `db:"display_name"`
	Description   string       `db:"description"`
	StartAt       time.Time    `db:"start_at"`
	EndAt         time.Time    `db:"end_at"`
}

func academicPeriodSliceColumns() []string {
	return []string{
		"academic_periods.id",
		"academic_periods.created_at",
		"academic_periods.updated_at",
		"academic_periods.archived_at",
		"academic_periods.revision",
		"academic_periods.institution_id",
		"academic_periods.name",
		"academic_periods.display_name",
		"academic_periods.description",
		"academic_periods.start_at",
		"academic_periods.end_at",
	}
}

func newSqlAcademicPeriodStore(sqlStore *SqlStore) store.AcademicPeriodStore {
	s := &SqlAcademicPeriodStore{SqlStore: sqlStore}
	s.academicPeriodsQuery = s.getQueryBuilder().
		Select(academicPeriodSliceColumns()...).
		From("academic_periods")
	return s
}

func (s SqlAcademicPeriodStore) Create(ctx context.Context, input *store.AcademicPeriodCreation) (*model.AcademicPeriod, error) {
	if input == nil || input.Period == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_period", "creation", nil)
	}
	if !input.Period.ID.IsValid() {
		return nil, store.NewErrInvalidInput("academic_period", "id", input.Period.ID.String())
	}
	candidate := *input.Period
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic period creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row := newAcademicPeriodRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO academic_periods (
			id, created_at, updated_at, archived_at, revision, institution_id,
			name, display_name, description, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :institution_id,
			:name, :display_name, :description, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf("create academic period: %w", translateError("academic_period", candidate.ID.String(), err))
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete academic period creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic period creation: %w", err)
	}
	return &candidate, nil
}

func (s SqlAcademicPeriodStore) Save(
	ctx context.Context,
	period *model.AcademicPeriod,
) (*model.AcademicPeriod, error) {
	if period == nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}
	if !period.ID.IsZero() {
		return nil, store.NewErrInvalidInput("academic_period", "id", period.ID.String())
	}

	id, err := model.ParseAcademicPeriodID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *period
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil).Wrap(err)
	}

	row := newAcademicPeriodRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO academic_periods (
			id, created_at, updated_at, archived_at, revision, institution_id,
			name, display_name, description, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :institution_id,
			:name, :display_name, :description, :start_at, :end_at
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save academic period: %w",
			translateError("academic_period", candidate.ID.String(), err),
		)
	}
	return &candidate, nil
}

func (s SqlAcademicPeriodStore) Get(ctx context.Context, id string) (*model.AcademicPeriod, error) {
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{
		"academic_periods.id":          id,
		"academic_periods.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", id, err)
	}
	return row.model()
}

func (s SqlAcademicPeriodStore) GetByName(
	ctx context.Context,
	institutionID string,
	name string,
) (*model.AcademicPeriod, error) {
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{
		"academic_periods.institution_id": institutionID,
		"academic_periods.name":           name,
		"academic_periods.archived_at":    nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", institutionID+"/"+name, err)
	}
	return row.model()
}

func (s SqlAcademicPeriodStore) ListByInstitution(
	ctx context.Context,
	institutionID string,
) ([]*model.AcademicPeriod, error) {
	query := s.academicPeriodsQuery.
		Where(sq.Eq{
			"academic_periods.institution_id": institutionID,
			"academic_periods.archived_at":    nil,
		}).
		OrderBy("academic_periods.start_at", "academic_periods.name", "academic_periods.id")

	rows := []academicPeriodRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list academic periods by institution: %w", err)
	}
	periods := make([]*model.AcademicPeriod, 0, len(rows))
	for _, row := range rows {
		period, err := row.model()
		if err != nil {
			return nil, err
		}
		periods = append(periods, period)
	}
	return periods, nil
}

func (s SqlAcademicPeriodStore) SearchByInstitution(
	ctx context.Context,
	institutionID string,
	term string,
	limit int,
) ([]*model.AcademicPeriod, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("academic_period", "limit", limit)
	}
	query := s.academicPeriodsQuery.Where(sq.Eq{
		"academic_periods.institution_id": institutionID,
		"academic_periods.archived_at":    nil,
	}).Where("(academic_periods.name ILIKE ? OR academic_periods.display_name ILIKE ?)",
		"%"+term+"%", "%"+term+"%").
		OrderBy("academic_periods.start_at", "academic_periods.id").Limit(uint64(limit))
	rows := []academicPeriodRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search academic periods: %w", err)
	}
	result := make([]*model.AcademicPeriod, 0, len(rows))
	for _, row := range rows {
		period, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, period)
	}
	return result, nil
}

func (s SqlAcademicPeriodStore) Update(
	ctx context.Context,
	period *model.AcademicPeriod,
) (*model.AcademicPeriod, error) {
	if period == nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil)
	}

	candidate := *period
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil).Wrap(err)
	}

	row := newAcademicPeriodRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE academic_periods
		   SET updated_at = :updated_at,
		       revision = :revision,
		       institution_id = :institution_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description,
		       start_at = :start_at,
		       end_at = :end_at
		 WHERE id = :id AND archived_at IS NULL
		   AND revision = :expected_revision`, map[string]any{
		"id": candidate.ID.String(), "updated_at": row.UpdatedAt,
		"revision": candidate.Revision, "institution_id": row.InstitutionID,
		"name": row.Name, "display_name": row.DisplayName, "description": row.Description,
		"start_at": row.StartAt, "end_at": row.EndAt,
		"expected_revision": candidate.Revision - 1,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"update academic period: %w",
			translateError("academic_period", candidate.ID.String(), err),
		)
	}
	if err := requireRevisionAffected(ctx, s.GetMaster(), result, "academic_period", "academic_periods", candidate.ID.String()); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (s SqlAcademicPeriodStore) UpdateWithAudit(ctx context.Context, input *store.AcademicPeriodUpdate) (*model.AcademicPeriod, error) {
	if input == nil || input.Period == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_period", "update", nil)
	}
	candidate := *input.Period
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_period", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic period audited update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row := newAcademicPeriodRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE academic_periods
		   SET updated_at = :updated_at, revision = :revision, name = :name, display_name = :display_name,
		       description = :description, start_at = :start_at, end_at = :end_at
		 WHERE id = :id AND institution_id = :institution_id AND archived_at IS NULL
		   AND revision = :expected_revision`, map[string]any{
		"id": candidate.ID.String(), "updated_at": row.UpdatedAt,
		"revision": candidate.Revision, "institution_id": row.InstitutionID,
		"name": row.Name, "display_name": row.DisplayName, "description": row.Description,
		"start_at": row.StartAt, "end_at": row.EndAt,
		"expected_revision": candidate.Revision - 1,
	})
	if err != nil {
		return nil, fmt.Errorf("update academic period: %w", translateError("academic_period", candidate.ID.String(), err))
	}
	if err := requireOwnedRevisionAffected(
		ctx, tx, result, "academic_period", "academic_periods", "institution_id",
		candidate.ID.String(), candidate.InstitutionID.String(),
	); err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete academic period update audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic period update: %w", err)
	}
	return &candidate, nil
}

func (s SqlAcademicPeriodStore) Delete(
	ctx context.Context,
	id string,
	deleteAt int64,
) (*model.AcademicPeriod, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_period", "archived_at", deleteAt)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic period delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{"academic_periods.id": id, "academic_periods.archived_at": nil})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", id, err)
	}
	current, err := row.model()
	if err != nil {
		return nil, err
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM classes WHERE academic_period_id = ? AND archived_at IS NULL
			UNION ALL
			SELECT 1 FROM class_members WHERE academic_period_id = ? AND archived_at IS NULL
		)`, id, id); err != nil {
		return nil, fmt.Errorf("check academic period archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict(
			"academic_period",
			"academic_period_has_active_dependents",
			nil,
		)
	}
	result, err := tx.Exec(ctx, `
		UPDATE academic_periods SET updated_at = ?, archived_at = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(deleteAt), model.TimeFromMillis(deleteAt), id, current.Revision)
	if err != nil {
		return nil, fmt.Errorf("archive academic period: %w", err)
	}
	if err := requireRevisionAffected(ctx, tx, result, "academic_period", "academic_periods", id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic period delete: %w", err)
	}
	at := model.TimeFromMillis(deleteAt)
	current.UpdatedAt = at
	current.ArchivedAt = model.OptionalTimeFromMillis(deleteAt)
	current.Revision++
	return current, nil
}

func (s SqlAcademicPeriodStore) ArchiveWithAudit(ctx context.Context, input *store.AcademicPeriodArchive) (*model.AcademicPeriod, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_period", "archive", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic period archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicPeriodLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row academicPeriodRow
	query := s.academicPeriodsQuery.Where(sq.Eq{"academic_periods.id": input.ID, "academic_periods.archived_at": nil})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_period", input.ID, err)
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `SELECT EXISTS (
		SELECT 1 FROM classes WHERE academic_period_id = ? AND archived_at IS NULL
		UNION ALL SELECT 1 FROM class_members WHERE academic_period_id = ? AND archived_at IS NULL
	)`, input.ID, input.ID); err != nil {
		return nil, fmt.Errorf("check academic period archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("academic_period", "academic_period_has_active_dependents", nil)
	}
	result, err := tx.Exec(ctx, `UPDATE academic_periods SET updated_at = ?, archived_at = ?, revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(input.ArchiveAt), model.TimeFromMillis(input.ArchiveAt), input.ID, row.Revision)
	if err != nil {
		return nil, fmt.Errorf("archive academic period: %w", err)
	}
	if err := requireRevisionAffected(ctx, tx, result, "academic_period", "academic_periods", input.ID); err != nil {
		return nil, err
	}
	period, err := row.model()
	if err != nil {
		return nil, err
	}
	at := model.TimeFromMillis(input.ArchiveAt)
	period.UpdatedAt = at
	period.ArchivedAt = model.OptionalTimeFromMillis(input.ArchiveAt)
	period.Revision++
	encoded, appErr := model.EncodeAuditData(period.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete academic period archive audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic period archive: %w", err)
	}
	return period, nil
}

func lockAcademicPeriodLifecycle(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", academicPeriodLifecycleLock); err != nil {
		return fmt.Errorf("lock academic period lifecycle: %w", err)
	}
	return nil
}

func validateActiveAcademicPeriod(ctx context.Context, executor sqlxExecutor, id string) error {
	var exists bool
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM academic_periods WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
		return fmt.Errorf("validate class academic period: %w", err)
	}
	if !exists {
		return store.NewErrReference("class", "classes_academic_period_id_fkey", nil)
	}
	return nil
}

func newAcademicPeriodRow(period *model.AcademicPeriod) academicPeriodRow {
	return academicPeriodRow{
		ID:            period.ID.String(),
		CreatedAt:     UTCTime(period.CreatedAt),
		UpdatedAt:     UTCTime(period.UpdatedAt),
		ArchivedAt:    NullTimeFromOptional(period.ArchivedAt),
		Revision:      period.Revision,
		InstitutionID: period.InstitutionID.String(),
		Name:          period.Name,
		DisplayName:   period.DisplayName,
		Description:   period.Description,
		StartAt:       UTCTime(period.StartsAt),
		EndAt:         UTCTime(period.EndsAt),
	}
}

func (row academicPeriodRow) model() (*model.AcademicPeriod, error) {
	id, err := model.ParseAcademicPeriodID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("rehydrate academic period %q: %w", row.ID, err)
	}
	institutionID, err := model.ParseInstitutionID(row.InstitutionID)
	if err != nil {
		return nil, fmt.Errorf("rehydrate academic period %q: %w", row.ID, err)
	}
	period := &model.AcademicPeriod{
		ID:            id,
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
		ArchivedAt:    OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:      row.Revision,
		InstitutionID: institutionID,
		Name:          row.Name,
		DisplayName:   row.DisplayName,
		Description:   row.Description,
		StartsAt:      row.StartAt.UTC(),
		EndsAt:        row.EndAt.UTC(),
	}
	if err := period.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate academic period %q: %w", row.ID, err)
	}
	return period, nil
}

var _ store.AcademicPeriodStore = (*SqlAcademicPeriodStore)(nil)
