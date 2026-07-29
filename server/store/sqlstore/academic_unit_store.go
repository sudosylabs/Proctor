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

func (s SqlAcademicUnitStore) Save(ctx context.Context, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	if unit.Id != "" {
		return nil, store.NewErrInvalidInput("academic_unit", "id", unit.Id)
	}
	candidate := *unit
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateAcademicUnitParent(ctx, tx, candidate.Id, candidate.InstitutionId, candidate.ParentId); err != nil {
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
		return nil, fmt.Errorf("save academic unit: %w", translateError("academic_unit", candidate.Id, err))
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

func (s SqlAcademicUnitStore) Update(ctx context.Context, unit *model.AcademicUnit) (*model.AcademicUnit, error) {
	if unit == nil {
		return nil, store.NewErrInvalidInput("academic_unit", "value", nil)
	}
	candidate := *unit
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin academic unit update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateAcademicUnitParent(ctx, tx, candidate.Id, candidate.InstitutionId, candidate.ParentId); err != nil {
		return nil, err
	}
	row := newAcademicUnitRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE academic_units
		   SET update_at = :update_at,
		       parent_id = :parent_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND institution_id = :institution_id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update academic unit: %w", translateError("academic_unit", candidate.Id, err))
	}
	if err := requireAffected(result, "academic_unit", candidate.Id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit academic unit update: %w", err)
	}
	return &candidate, nil
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
		ID:            unit.Id,
		CreateAt:      unit.CreateAt,
		UpdateAt:      unit.UpdateAt,
		DeleteAt:      unit.DeleteAt,
		InstitutionID: unit.InstitutionId,
		ParentID:      nullableString(unit.ParentId),
		Name:          unit.Name,
		DisplayName:   unit.DisplayName,
		Description:   unit.Description,
	}
}

func (row academicUnitRow) model() *model.AcademicUnit {
	return &model.AcademicUnit{
		Id:            row.ID,
		CreateAt:      row.CreateAt,
		UpdateAt:      row.UpdateAt,
		DeleteAt:      row.DeleteAt,
		InstitutionId: row.InstitutionID,
		ParentId:      row.ParentID.String,
		Name:          row.Name,
		DisplayName:   row.DisplayName,
		Description:   row.Description,
	}
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

var _ store.AcademicUnitStore = (*SqlAcademicUnitStore)(nil)
