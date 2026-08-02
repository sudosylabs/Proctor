// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing concrete class
// rosters scoped by programme level and academic period.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlClassStore struct {
	*SqlStore
	classesQuery sq.SelectBuilder
}

type classRow struct {
	ID               string `db:"id"`
	CreateAt         int64  `db:"create_at"`
	UpdateAt         int64  `db:"update_at"`
	DeleteAt         int64  `db:"delete_at"`
	ProgrammeLevelID string `db:"programme_level_id"`
	AcademicPeriodID string `db:"academic_period_id"`
	Name             string `db:"name"`
	DisplayName      string `db:"display_name"`
	Description      string `db:"description"`
}

func classSliceColumns() []string {
	return []string{
		"classes.id",
		"classes.create_at",
		"classes.update_at",
		"classes.delete_at",
		"classes.programme_level_id",
		"classes.academic_period_id",
		"classes.name",
		"classes.display_name",
		"classes.description",
	}
}

func newSqlClassStore(sqlStore *SqlStore) store.ClassStore {
	s := &SqlClassStore{SqlStore: sqlStore}
	s.classesQuery = s.getQueryBuilder().
		Select(classSliceColumns()...).
		From("classes")
	return s
}

func (s SqlClassStore) Save(ctx context.Context, class *model.Class) (*model.Class, error) {
	if class == nil {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}
	if class.Id != "" {
		return nil, store.NewErrInvalidInput("class", "id", class.Id)
	}

	candidate := *class
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelId); err != nil {
		return nil, err
	}
	row := newClassRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO classes (
			id, create_at, update_at, delete_at, programme_level_id,
			academic_period_id, name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :programme_level_id,
			:academic_period_id, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save class: %w", translateError("class", candidate.Id, err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class save: %w", err)
	}
	return &candidate, nil
}

func (s SqlClassStore) Get(ctx context.Context, id string) (*model.Class, error) {
	var row classRow
	query := s.classesQuery.Where(sq.Eq{
		"classes.id":        id,
		"classes.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("class", id, err)
	}
	return row.model(), nil
}

func (s SqlClassStore) GetByName(
	ctx context.Context,
	programmeLevelID string,
	academicPeriodID string,
	name string,
) (*model.Class, error) {
	var row classRow
	query := s.classesQuery.Where(sq.Eq{
		"classes.programme_level_id": programmeLevelID,
		"classes.academic_period_id": academicPeriodID,
		"classes.name":               name,
		"classes.delete_at":          int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		key := programmeLevelID + "/" + academicPeriodID + "/" + name
		return nil, translateError("class", key, err)
	}
	return row.model(), nil
}

func (s SqlClassStore) ListByProgrammeLevel(
	ctx context.Context,
	programmeLevelID string,
) ([]*model.Class, error) {
	query := s.classesQuery.
		Join("academic_periods ON academic_periods.id = classes.academic_period_id").
		Where(sq.Eq{
			"classes.programme_level_id": programmeLevelID,
			"classes.delete_at":          int64(0),
		}).
		OrderBy("academic_periods.start_at", "classes.name", "classes.id")
	return s.selectClasses(ctx, query, "list classes by programme level")
}

func (s SqlClassStore) ListByAcademicPeriod(
	ctx context.Context,
	academicPeriodID string,
) ([]*model.Class, error) {
	query := s.classesQuery.
		Where(sq.Eq{
			"classes.academic_period_id": academicPeriodID,
			"classes.delete_at":          int64(0),
		}).
		OrderBy("classes.programme_level_id", "classes.name", "classes.id")
	return s.selectClasses(ctx, query, "list classes by academic period")
}

func (s SqlClassStore) SearchByAcademicUnit(
	ctx context.Context,
	academicUnitID string,
	term string,
	limit int,
) ([]*model.Class, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("class", "limit", limit)
	}
	query := s.classesQuery.
		Join("programme_levels ON programme_levels.id = classes.programme_level_id").
		Join("programmes ON programmes.id = programme_levels.programme_id").
		Where(sq.Eq{
			"programmes.academic_unit_id": academicUnitID,
			"programmes.delete_at":        int64(0),
			"programme_levels.delete_at":  int64(0),
			"classes.delete_at":           int64(0),
		}).
		Where("(classes.name ILIKE ? OR classes.display_name ILIKE ?)",
			"%"+term+"%", "%"+term+"%").
		OrderBy("classes.name", "classes.id").
		Limit(uint64(limit))
	return s.selectClasses(ctx, query, "search classes by academic unit")
}

func (s SqlClassStore) GetAcademicUnitId(ctx context.Context, id string) (string, error) {
	var academicUnitID string
	if err := s.GetMaster().Get(ctx, &academicUnitID, `
		SELECT programmes.academic_unit_id
		  FROM classes
		  JOIN programme_levels ON programme_levels.id = classes.programme_level_id
		  JOIN programmes ON programmes.id = programme_levels.programme_id
		 WHERE classes.id = $1
		   AND classes.delete_at = 0
		   AND programme_levels.delete_at = 0
		   AND programmes.delete_at = 0`, id); err != nil {
		return "", translateError("class", id, err)
	}
	return academicUnitID, nil
}

func (s SqlClassStore) selectClasses(
	ctx context.Context,
	query sq.SelectBuilder,
	operation string,
) ([]*model.Class, error) {
	rows := []classRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	classes := make([]*model.Class, 0, len(rows))
	for _, row := range rows {
		classes = append(classes, row.model())
	}
	return classes, nil
}

func (s SqlClassStore) Update(ctx context.Context, class *model.Class) (*model.Class, error) {
	if class == nil {
		return nil, store.NewErrInvalidInput("class", "value", nil)
	}

	candidate := *class
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	if err := s.validateInstitution(ctx, &candidate); err != nil {
		return nil, err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin class update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgrammeLevel(ctx, tx, candidate.ProgrammeLevelId); err != nil {
		return nil, err
	}
	row := newClassRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE classes
		   SET update_at = :update_at,
		       programme_level_id = :programme_level_id,
		       academic_period_id = :academic_period_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update class: %w", translateError("class", candidate.Id, err))
	}
	if err := requireAffected(result, "class", candidate.Id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit class update: %w", err)
	}
	return &candidate, nil
}

func (s SqlClassStore) Delete(
	ctx context.Context,
	id string,
	deleteAt int64,
) (*model.Class, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("class", "delete_at", deleteAt)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var dependent bool
	if err := s.GetMaster().Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM class_members
			 WHERE class_id = ? AND delete_at = 0 AND end_at = 0
			UNION ALL
			SELECT 1 FROM role_bindings
			 WHERE scope_type = 'class' AND scope_id = ?
			   AND delete_at = 0 AND end_at = 0
		)`, id, id); err != nil {
		return nil, fmt.Errorf("check class archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("class", "class_has_active_dependents", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE classes SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0`, deleteAt, deleteAt, id)
	if err != nil {
		return nil, fmt.Errorf("archive class: %w", err)
	}
	if err := requireAffected(result, "class", id); err != nil {
		return nil, err
	}
	current.UpdateAt, current.DeleteAt = deleteAt, deleteAt
	return current, nil
}

func (s SqlClassStore) validateInstitution(
	ctx context.Context,
	class *model.Class,
) error {
	var unitInstitutionID string
	if err := s.GetMaster().Get(ctx, &unitInstitutionID, `
		SELECT academic_units.institution_id
		  FROM programme_levels
		  JOIN programmes ON programmes.id = programme_levels.programme_id
		  JOIN academic_units ON academic_units.id = programmes.academic_unit_id
		 WHERE programme_levels.id = ?
		   AND programme_levels.delete_at = 0
		   AND programmes.delete_at = 0
		   AND academic_units.delete_at = 0`,
		class.ProgrammeLevelId,
	); err != nil {
		var exists bool
		if existsErr := s.GetMaster().Get(
			ctx,
			&exists,
			"SELECT EXISTS (SELECT 1 FROM programme_levels WHERE id = ?)",
			class.ProgrammeLevelId,
		); existsErr != nil {
			return fmt.Errorf("check programme level reference: %w", existsErr)
		}
		constraint := "classes_active_hierarchy"
		if !exists {
			constraint = "classes_programme_level_id_fkey"
		}
		return store.NewErrReference(
			"class",
			constraint,
			err,
		)
	}
	var periodInstitutionID string
	if err := s.GetMaster().Get(ctx, &periodInstitutionID, `
		SELECT institution_id
		  FROM academic_periods
		 WHERE id = ? AND delete_at = 0`,
		class.AcademicPeriodId,
	); err != nil {
		var exists bool
		if existsErr := s.GetMaster().Get(
			ctx,
			&exists,
			"SELECT EXISTS (SELECT 1 FROM academic_periods WHERE id = ?)",
			class.AcademicPeriodId,
		); existsErr != nil {
			return fmt.Errorf("check academic period reference: %w", existsErr)
		}
		constraint := "classes_active_hierarchy"
		if !exists {
			constraint = "classes_academic_period_id_fkey"
		}
		return store.NewErrReference("class", constraint, err)
	}
	if unitInstitutionID != periodInstitutionID {
		return store.NewErrReference(
			"class",
			"classes_same_institution",
			nil,
		)
	}
	return nil
}

func newClassRow(class *model.Class) classRow {
	return classRow{
		ID:               class.Id,
		CreateAt:         class.CreateAt,
		UpdateAt:         class.UpdateAt,
		DeleteAt:         class.DeleteAt,
		ProgrammeLevelID: class.ProgrammeLevelId,
		AcademicPeriodID: class.AcademicPeriodId,
		Name:             class.Name,
		DisplayName:      class.DisplayName,
		Description:      class.Description,
	}
}

func (row classRow) model() *model.Class {
	return &model.Class{
		Id:               row.ID,
		CreateAt:         row.CreateAt,
		UpdateAt:         row.UpdateAt,
		DeleteAt:         row.DeleteAt,
		ProgrammeLevelId: row.ProgrammeLevelID,
		AcademicPeriodId: row.AcademicPeriodID,
		Name:             row.Name,
		DisplayName:      row.DisplayName,
		Description:      row.Description,
	}
}

var _ store.ClassStore = (*SqlClassStore)(nil)
