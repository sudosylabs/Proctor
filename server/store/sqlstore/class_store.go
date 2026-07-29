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

	row := newClassRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO classes (
			id, create_at, update_at, delete_at, programme_level_id,
			academic_period_id, name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :programme_level_id,
			:academic_period_id, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save class: %w", translateError("class", candidate.Id, err))
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

	row := newClassRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
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
	return &candidate, nil
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
