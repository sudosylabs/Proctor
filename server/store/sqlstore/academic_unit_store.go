// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const academicUnitHierarchyLock = "proctor:academic-unit-hierarchy"

type SqlAcademicUnitStore struct {
	*SqlStore
	academicUnitsQuery sq.SelectBuilder
}

type academicUnitRow struct {
	ID            string         `db:"id"`
	CreateAt      int64          `db:"create_at"`
	UpdateAt      int64          `db:"update_at"`
	DeleteAt      int64          `db:"delete_at"`
	InstitutionID string         `db:"institution_id"`
	ParentID      sql.NullString `db:"parent_id"`
	Name          string         `db:"name"`
	DisplayName   string         `db:"display_name"`
	Description   string         `db:"description"`
}

func academicUnitSliceColumns() []string {
	return []string{
		"academic_units.id",
		"academic_units.create_at",
		"academic_units.update_at",
		"academic_units.delete_at",
		"academic_units.institution_id",
		"academic_units.parent_id",
		"academic_units.name",
		"academic_units.display_name",
		"academic_units.description",
	}
}

func newSqlAcademicUnitStore(sqlStore *SqlStore) store.AcademicUnitStore {
	s := &SqlAcademicUnitStore{SqlStore: sqlStore}
	s.academicUnitsQuery = s.getQueryBuilder().
		Select(academicUnitSliceColumns()...).
		From("academic_units")
	return s
}

func (s SqlAcademicUnitStore) Create(
	ctx context.Context,
	input *store.AcademicUnitCreation,
) (*model.AcademicUnit, error) {
	if input == nil || input.Unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "creation", nil)
	}
	if !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit", "audit", nil)
	}
	if !input.Unit.ID.IsValid() {
		return nil, store.NewErrInvalidInput("academic_unit", "id", input.Unit.ID.String())
	}
	candidate := *input.Unit
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput(
			"academic_unit", "value", nil,
		).Wrap(err)
	}
	result, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateAcademicUnitParent(
		ctx, tx,
		candidate.ID.String(),
		candidate.InstitutionID.String(),
		candidate.ParentID.String(),
	); err != nil {
		return nil, err
	}
	row := newAcademicUnitRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO academic_units (
			id, create_at, update_at, delete_at, institution_id, parent_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :institution_id,
			:parent_id, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"create academic unit: %w",
			translateError("academic_unit", candidate.ID.String(), err),
		)
	}
	if _, err := completeAuditEvent(
		ctx,
		tx,
		input.AuditEventID,
		model.AuditStatusSuccess,
		"",
		result,
		input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete academic unit creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit creation: %w", err)
	}
	return &candidate, nil
}

func (s SqlAcademicUnitStore) Save(ctx context.Context, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	if !unit.ID.IsZero() {
		return nil, store.NewErrInvalidInput("academic_unit", "id", unit.ID.String())
	}
	id, err := model.ParseAcademicUnitID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *unit
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil).Wrap(err)
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateAcademicUnitParent(
		ctx, tx,
		candidate.ID.String(),
		candidate.InstitutionID.String(),
		candidate.ParentID.String(),
	); err != nil {
		return nil, err
	}
	row := newAcademicUnitRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO academic_units (
			id, create_at, update_at, delete_at, institution_id, parent_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :institution_id,
			:parent_id, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save academic unit: %w",
			translateError("academic_unit", candidate.ID.String(), err),
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit save: %w", err)
	}
	return &candidate, nil
}

func (s SqlAcademicUnitStore) Get(ctx context.Context, id string) (*model.AcademicUnit, error) {
	var row academicUnitRow
	query := s.academicUnitsQuery.Where(sq.Eq{
		"academic_units.id":        id,
		"academic_units.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("academic_unit", id, err)
	}
	return row.model(), nil
}

// ListAncestors returns the target unit first, followed by each parent up to
// the root. The recursive query is bounded by the cycle invariant enforced by
// Save and Update.
func (s SqlAcademicUnitStore) ListAncestors(
	ctx context.Context,
	id string,
) ([]*model.AcademicUnit, error) {
	rows := []academicUnitRow{}
	if err := s.GetMaster().Select(ctx, &rows, `
		WITH RECURSIVE ancestors AS (
			SELECT id, create_at, update_at, delete_at, institution_id, parent_id,
			       name, display_name, description, 0 AS depth
			  FROM academic_units
			 WHERE id = $1 AND delete_at = 0
			UNION ALL
			SELECT parent.id, parent.create_at, parent.update_at, parent.delete_at,
			       parent.institution_id, parent.parent_id, parent.name,
			       parent.display_name, parent.description, ancestors.depth + 1
			  FROM academic_units parent
			  JOIN ancestors ON parent.id = ancestors.parent_id
			 WHERE parent.delete_at = 0
		)
		SELECT id, create_at, update_at, delete_at, institution_id, parent_id,
		       name, display_name, description
		  FROM ancestors
		 ORDER BY depth`, id); err != nil {
		return nil, fmt.Errorf("list academic unit ancestors: %w", err)
	}
	if len(rows) == 0 {
		return nil, store.NewErrNotFound("academic_unit", id)
	}
	units := make([]*model.AcademicUnit, 0, len(rows))
	for _, row := range rows {
		units = append(units, row.model())
	}
	return units, nil
}

func (s SqlAcademicUnitStore) ListChildren(ctx context.Context, institutionID, parentID string) ([]*model.AcademicUnit, error) {
	query := s.academicUnitsQuery.
		Where(sq.Eq{
			"academic_units.institution_id": institutionID,
			"academic_units.delete_at":      int64(0),
		}).
		Where(sq.Expr("academic_units.parent_id IS NOT DISTINCT FROM NULLIF(?, '')", parentID)).
		OrderBy("academic_units.name", "academic_units.id")

	rows := []academicUnitRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list academic unit children: %w", err)
	}
	units := make([]*model.AcademicUnit, 0, len(rows))
	for _, row := range rows {
		units = append(units, row.model())
	}
	return units, nil
}

func (s SqlAcademicUnitStore) Search(
	ctx context.Context,
	institutionID string,
	term string,
	limit int,
) ([]*model.AcademicUnit, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("academic_unit", "limit", limit)
	}
	query := s.academicUnitsQuery.
		Where(sq.Eq{
			"academic_units.institution_id": institutionID,
			"academic_units.delete_at":      int64(0),
		}).
		Where("(academic_units.name ILIKE ? OR academic_units.display_name ILIKE ?)",
			"%"+term+"%", "%"+term+"%").
		OrderBy("academic_units.name", "academic_units.id").
		Limit(uint64(limit))
	rows := []academicUnitRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search academic units: %w", err)
	}
	units := make([]*model.AcademicUnit, 0, len(rows))
	for _, row := range rows {
		units = append(units, row.model())
	}
	return units, nil
}

type academicUnitAuditCompletion struct {
	eventID string
	at      int64
}

func (s SqlAcademicUnitStore) UpdateWithAudit(
	ctx context.Context,
	input *store.AcademicUnitUpdate,
) (*model.AcademicUnit, error) {
	if input == nil || input.Unit == nil ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit", "update", nil)
	}
	candidate := *input.Unit
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil).Wrap(err)
	}
	return s.updateAcademicUnit(ctx, &candidate, &academicUnitAuditCompletion{
		eventID: input.AuditEventID, at: input.AuditAt,
	})
}

func (s SqlAcademicUnitStore) Update(ctx context.Context, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	candidate := *unit
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil).Wrap(err)
	}
	return s.updateAcademicUnit(ctx, &candidate, nil)
}

func (s SqlAcademicUnitStore) updateAcademicUnit(
	ctx context.Context,
	candidate *model.AcademicUnit,
	audit *academicUnitAuditCompletion,
) (*model.AcademicUnit, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateAcademicUnitParent(
		ctx, tx,
		candidate.ID.String(),
		candidate.InstitutionID.String(),
		candidate.ParentID.String(),
	); err != nil {
		return nil, err
	}
	row := newAcademicUnitRow(candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE academic_units
		   SET update_at = :update_at,
		       parent_id = :parent_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND institution_id = :institution_id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf(
			"update academic unit: %w",
			translateError("academic_unit", candidate.ID.String(), err),
		)
	}
	if err := requireAffected(result, "academic_unit", candidate.ID.String()); err != nil {
		return nil, err
	}
	if audit != nil {
		encoded, appErr := model.EncodeAuditData(candidate.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, audit.eventID, model.AuditStatusSuccess, "", encoded, audit.at,
		); err != nil {
			return nil, fmt.Errorf("complete academic unit update audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit update: %w", err)
	}
	return candidate, nil
}

func (s SqlAcademicUnitStore) ArchiveWithAudit(
	ctx context.Context,
	input *store.AcademicUnitArchive,
) (*model.AcademicUnit, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit", "archive", nil)
	}
	return s.archiveAcademicUnit(
		ctx, input.ID, input.ArchiveAt,
		&academicUnitAuditCompletion{eventID: input.AuditEventID, at: input.AuditAt},
	)
}

func (s SqlAcademicUnitStore) Delete(
	ctx context.Context,
	id string,
	deleteAt int64,
) (*model.AcademicUnit, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("academic_unit", "delete_at", deleteAt)
	}
	return s.archiveAcademicUnit(ctx, id, deleteAt, nil)
}

func (s SqlAcademicUnitStore) archiveAcademicUnit(
	ctx context.Context,
	id string,
	deleteAt int64,
	audit *academicUnitAuditCompletion,
) (*model.AcademicUnit, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	current, err := academicUnitFromExecutor(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM academic_units WHERE parent_id = ? AND delete_at = 0
			UNION ALL
			SELECT 1 FROM programmes WHERE academic_unit_id = ? AND delete_at = 0
			UNION ALL
			SELECT 1 FROM academic_unit_members WHERE academic_unit_id = ? AND delete_at = 0 AND end_at = 0
			UNION ALL
			SELECT 1 FROM role_bindings WHERE scope_type = 'academic_unit' AND scope_id = ? AND delete_at = 0 AND end_at = 0
		)`, id, id, id, id); err != nil {
		return nil, fmt.Errorf("check academic unit archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("academic_unit", "academic_unit_has_active_dependents", nil)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE academic_units SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0`, deleteAt, deleteAt, id); err != nil {
		return nil, fmt.Errorf("archive academic unit: %w", err)
	}
	at := model.TimeFromMillis(deleteAt)
	current.UpdatedAt = at
	current.ArchivedAt = model.OptionalTimeFromMillis(deleteAt)
	if audit != nil {
		encoded, appErr := model.EncodeAuditData(current.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, audit.eventID, model.AuditStatusSuccess, "", encoded, audit.at,
		); err != nil {
			return nil, fmt.Errorf("complete academic unit archive audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit archive: %w", err)
	}
	return current, nil
}

func academicUnitFromExecutor(
	ctx context.Context,
	executor sqlxExecutor,
	id string,
) (*model.AcademicUnit, error) {
	var row academicUnitRow
	if err := executor.Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, institution_id, parent_id,
		       name, display_name, description
		  FROM academic_units WHERE id = ? AND delete_at = 0 FOR UPDATE`, id); err != nil {
		return nil, translateError("academic_unit", id, err)
	}
	return row.model(), nil
}

func lockAcademicUnitHierarchy(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", academicUnitHierarchyLock); err != nil {
		return fmt.Errorf("lock academic unit hierarchy: %w", err)
	}
	return nil
}

func validateAcademicUnitParent(
	ctx context.Context,
	executor sqlxExecutor,
	unitID string,
	institutionID string,
	parentID string,
) error {
	if parentID == "" {
		return nil
	}
	var parentInstitutionID string
	err := executor.Get(
		ctx,
		&parentInstitutionID,
		"SELECT institution_id FROM academic_units WHERE id = ? AND delete_at = 0",
		parentID,
	)
	if err != nil {
		return translateError("academic_unit", parentID, err)
	}
	if parentInstitutionID != institutionID {
		return store.NewErrReference("academic_unit", "academic_units_parent_same_institution", nil)
	}
	if unitID == "" {
		return nil
	}

	var createsCycle bool
	err = executor.Get(ctx, &createsCycle, `
		WITH RECURSIVE descendants AS (
			SELECT id FROM academic_units WHERE parent_id = ? AND delete_at = 0
			UNION ALL
			SELECT child.id
			  FROM academic_units child
			  JOIN descendants parent ON child.parent_id = parent.id
			 WHERE child.delete_at = 0
		)
		SELECT EXISTS (SELECT 1 FROM descendants WHERE id = ?)`,
		unitID,
		parentID,
	)
	if err != nil {
		return fmt.Errorf("check academic unit hierarchy cycle: %w", err)
	}
	if createsCycle {
		return store.NewErrConflict("academic_unit", "academic_units_acyclic", nil)
	}
	return nil
}

func newAcademicUnitRow(unit *model.AcademicUnit) academicUnitRow {
	return academicUnitRow{
		ID:            unit.ID.String(),
		CreateAt:      model.MillisFromTime(unit.CreatedAt),
		UpdateAt:      model.MillisFromTime(unit.UpdatedAt),
		DeleteAt:      unit.ArchivedAt.Millis(),
		InstitutionID: unit.InstitutionID.String(),
		ParentID:      nullableString(unit.ParentID.String()),
		Name:          unit.Name,
		DisplayName:   unit.DisplayName,
		Description:   unit.Description,
	}
}

func (row academicUnitRow) model() *model.AcademicUnit {
	id, err := model.ParseAcademicUnitID(row.ID)
	if err != nil {
		id = model.AcademicUnitID(row.ID)
	}
	institutionID, err := model.ParseInstitutionID(row.InstitutionID)
	if err != nil {
		institutionID = model.InstitutionID(row.InstitutionID)
	}
	var parentID model.AcademicUnitID
	if row.ParentID.Valid && row.ParentID.String != "" {
		parsed, parseErr := model.ParseAcademicUnitID(row.ParentID.String)
		if parseErr != nil {
			parentID = model.AcademicUnitID(row.ParentID.String)
		} else {
			parentID = parsed
		}
	}
	return &model.AcademicUnit{
		ID:            id,
		CreatedAt:     model.TimeFromMillis(row.CreateAt),
		UpdatedAt:     model.TimeFromMillis(row.UpdateAt),
		ArchivedAt:    model.OptionalTimeFromMillis(row.DeleteAt),
		Revision:      1,
		InstitutionID: institutionID,
		ParentID:      parentID,
		Name:          row.Name,
		DisplayName:   row.DisplayName,
		Description:   row.Description,
	}
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

var _ store.AcademicUnitStore = (*SqlAcademicUnitStore)(nil)
