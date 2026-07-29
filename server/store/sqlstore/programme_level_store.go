// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model SQL store, reusable select builder, named writes,
// model lifecycle, and store-error boundary while implementing curriculum
// levels scoped to programmes.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlProgrammeLevelStore struct {
	*SqlStore
	programmeLevelsQuery sq.SelectBuilder
}

type programmeLevelRow struct {
	ID          string `db:"id"`
	CreateAt    int64  `db:"create_at"`
	UpdateAt    int64  `db:"update_at"`
	DeleteAt    int64  `db:"delete_at"`
	ProgrammeID string `db:"programme_id"`
	Name        string `db:"name"`
	DisplayName string `db:"display_name"`
	Description string `db:"description"`
}

func programmeLevelSliceColumns() []string {
	return []string{
		"programme_levels.id",
		"programme_levels.create_at",
		"programme_levels.update_at",
		"programme_levels.delete_at",
		"programme_levels.programme_id",
		"programme_levels.name",
		"programme_levels.display_name",
		"programme_levels.description",
	}
}

func newSqlProgrammeLevelStore(sqlStore *SqlStore) store.ProgrammeLevelStore {
	s := &SqlProgrammeLevelStore{SqlStore: sqlStore}
	s.programmeLevelsQuery = s.getQueryBuilder().
		Select(programmeLevelSliceColumns()...).
		From("programme_levels")
	return s
}

func (s SqlProgrammeLevelStore) Save(
	ctx context.Context,
	level *model.ProgrammeLevel,
) (*model.ProgrammeLevel, error) {
	if level == nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}
	if level.Id != "" {
		return nil, store.NewErrInvalidInput("programme_level", "id", level.Id)
	}

	candidate := *level
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newProgrammeLevelRow(&candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO programme_levels (
			id, create_at, update_at, delete_at, programme_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :programme_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf(
			"save programme level: %w",
			translateError("programme_level", candidate.Id, err),
		)
	}
	return &candidate, nil
}

func (s SqlProgrammeLevelStore) Get(ctx context.Context, id string) (*model.ProgrammeLevel, error) {
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.id":        id,
		"programme_levels.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", id, err)
	}
	return row.model(), nil
}

func (s SqlProgrammeLevelStore) GetByName(
	ctx context.Context,
	programmeID string,
	name string,
) (*model.ProgrammeLevel, error) {
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.programme_id": programmeID,
		"programme_levels.name":         name,
		"programme_levels.delete_at":    int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", programmeID+"/"+name, err)
	}
	return row.model(), nil
}

func (s SqlProgrammeLevelStore) ListByProgramme(
	ctx context.Context,
	programmeID string,
) ([]*model.ProgrammeLevel, error) {
	query := s.programmeLevelsQuery.
		Where(sq.Eq{
			"programme_levels.programme_id": programmeID,
			"programme_levels.delete_at":    int64(0),
		}).
		OrderBy("programme_levels.name", "programme_levels.id")

	rows := []programmeLevelRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list programme levels by programme: %w", err)
	}
	levels := make([]*model.ProgrammeLevel, 0, len(rows))
	for _, row := range rows {
		levels = append(levels, row.model())
	}
	return levels, nil
}

func (s SqlProgrammeLevelStore) Update(
	ctx context.Context,
	level *model.ProgrammeLevel,
) (*model.ProgrammeLevel, error) {
	if level == nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil)
	}

	candidate := *level
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newProgrammeLevelRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE programme_levels
		   SET update_at = :update_at,
		       programme_id = :programme_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf(
			"update programme level: %w",
			translateError("programme_level", candidate.Id, err),
		)
	}
	if err := requireAffected(result, "programme_level", candidate.Id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func newProgrammeLevelRow(level *model.ProgrammeLevel) programmeLevelRow {
	return programmeLevelRow{
		ID:          level.Id,
		CreateAt:    level.CreateAt,
		UpdateAt:    level.UpdateAt,
		DeleteAt:    level.DeleteAt,
		ProgrammeID: level.ProgrammeId,
		Name:        level.Name,
		DisplayName: level.DisplayName,
		Description: level.Description,
	}
}

func (row programmeLevelRow) model() *model.ProgrammeLevel {
	return &model.ProgrammeLevel{
		Id:          row.ID,
		CreateAt:    row.CreateAt,
		UpdateAt:    row.UpdateAt,
		DeleteAt:    row.DeleteAt,
		ProgrammeId: row.ProgrammeID,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		Description: row.Description,
	}
}

var _ store.ProgrammeLevelStore = (*SqlProgrammeLevelStore)(nil)
