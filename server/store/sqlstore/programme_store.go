// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing programmes
// scoped to academic units.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlProgrammeStore struct {
	*SqlStore
	programmesQuery sq.SelectBuilder
}

type programmeRow struct {
	ID             string `db:"id"`
	CreateAt       int64  `db:"create_at"`
	UpdateAt       int64  `db:"update_at"`
	DeleteAt       int64  `db:"delete_at"`
	AcademicUnitID string `db:"academic_unit_id"`
	Name           string `db:"name"`
	DisplayName    string `db:"display_name"`
	Description    string `db:"description"`
}

func programmeSliceColumns() []string {
	return []string{
		"programmes.id",
		"programmes.create_at",
		"programmes.update_at",
		"programmes.delete_at",
		"programmes.academic_unit_id",
		"programmes.name",
		"programmes.display_name",
		"programmes.description",
	}
}

func newSqlProgrammeStore(sqlStore *SqlStore) store.ProgrammeStore {
	s := &SqlProgrammeStore{SqlStore: sqlStore}
	s.programmesQuery = s.getQueryBuilder().
		Select(programmeSliceColumns()...).
		From("programmes")
	return s
}

func (s SqlProgrammeStore) Save(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
	if programme == nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	if programme.Id != "" {
		return nil, store.NewErrInvalidInput("programme", "id", programme.Id)
	}

	candidate := *programme
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newProgrammeRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO programmes (
			id, create_at, update_at, delete_at, academic_unit_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :academic_unit_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save programme: %w", translateError("programme", candidate.Id, err))
	}
	return &candidate, nil
}

func (s SqlProgrammeStore) Get(ctx context.Context, id string) (*model.Programme, error) {
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.id":        id,
		"programmes.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", id, err)
	}
	return row.model(), nil
}

func (s SqlProgrammeStore) GetByName(
	ctx context.Context,
	academicUnitID string,
	name string,
) (*model.Programme, error) {
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.academic_unit_id": academicUnitID,
		"programmes.name":             name,
		"programmes.delete_at":        int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", academicUnitID+"/"+name, err)
	}
	return row.model(), nil
}

func (s SqlProgrammeStore) ListByAcademicUnit(
	ctx context.Context,
	academicUnitID string,
) ([]*model.Programme, error) {
	query := s.programmesQuery.
		Where(sq.Eq{
			"programmes.academic_unit_id": academicUnitID,
			"programmes.delete_at":        int64(0),
		}).
		OrderBy("programmes.name", "programmes.id")

	rows := []programmeRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list programmes by academic unit: %w", err)
	}
	programmes := make([]*model.Programme, 0, len(rows))
	for _, row := range rows {
		programmes = append(programmes, row.model())
	}
	return programmes, nil
}

func (s SqlProgrammeStore) Update(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
	if programme == nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	candidate := *programme
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newProgrammeRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE programmes
		   SET update_at = :update_at,
		       academic_unit_id = :academic_unit_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update programme: %w", translateError("programme", candidate.Id, err))
	}
	if err := requireAffected(result, "programme", candidate.Id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func newProgrammeRow(programme *model.Programme) programmeRow {
	return programmeRow{
		ID:             programme.Id,
		CreateAt:       programme.CreateAt,
		UpdateAt:       programme.UpdateAt,
		DeleteAt:       programme.DeleteAt,
		AcademicUnitID: programme.AcademicUnitId,
		Name:           programme.Name,
		DisplayName:    programme.DisplayName,
		Description:    programme.Description,
	}
}

func (row programmeRow) model() *model.Programme {
	return &model.Programme{
		Id:             row.ID,
		CreateAt:       row.CreateAt,
		UpdateAt:       row.UpdateAt,
		DeleteAt:       row.DeleteAt,
		AcademicUnitId: row.AcademicUnitID,
		Name:           row.Name,
		DisplayName:    row.DisplayName,
		Description:    row.Description,
	}
}

var _ store.ProgrammeStore = (*SqlProgrammeStore)(nil)
