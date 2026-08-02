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

const programmeLevelLifecycleLock = "proctor:programme-level-lifecycle"

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

func (s SqlProgrammeLevelStore) Create(ctx context.Context, input *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error) {
	if input == nil || input.Level == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "creation", nil)
	}
	candidate := *input.Level
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(appErr)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeId); err != nil {
		return nil, err
	}
	row := newProgrammeLevelRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO programme_levels (
			id, create_at, update_at, delete_at, programme_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :programme_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("create programme level: %w", translateError("programme_level", candidate.Id, err))
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete programme level creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level creation: %w", err)
	}
	return &candidate, nil
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

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeId); err != nil {
		return nil, err
	}
	row := newProgrammeLevelRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level save: %w", err)
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

func (s SqlProgrammeLevelStore) SearchByProgramme(
	ctx context.Context,
	programmeID string,
	term string,
	limit int,
) ([]*model.ProgrammeLevel, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("programme_level", "limit", limit)
	}
	query := s.programmeLevelsQuery.Where(sq.Eq{
		"programme_levels.programme_id": programmeID,
		"programme_levels.delete_at":    int64(0),
	}).Where("(programme_levels.name ILIKE ? OR programme_levels.display_name ILIKE ?)",
		"%"+term+"%", "%"+term+"%").
		OrderBy("programme_levels.name", "programme_levels.id").Limit(uint64(limit))
	rows := []programmeLevelRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search programme levels: %w", err)
	}
	result := make([]*model.ProgrammeLevel, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.model())
	}
	return result, nil
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

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeId); err != nil {
		return nil, err
	}
	row := newProgrammeLevelRow(&candidate)
	result, err := tx.NamedExec(ctx, `
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level update: %w", err)
	}
	return &candidate, nil
}

func (s SqlProgrammeLevelStore) UpdateWithAudit(ctx context.Context, input *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error) {
	if input == nil || input.Level == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "update", nil)
	}
	candidate := *input.Level
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("programme_level", "value", nil).Wrap(appErr)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level audited update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveProgramme(ctx, tx, candidate.ProgrammeId); err != nil {
		return nil, err
	}
	row := newProgrammeLevelRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE programme_levels
		   SET update_at = :update_at, name = :name,
		       display_name = :display_name, description = :description
		 WHERE id = :id AND programme_id = :programme_id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update programme level: %w", translateError("programme_level", candidate.Id, err))
	}
	if err := requireAffected(result, "programme_level", candidate.Id); err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete programme level update audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level update: %w", err)
	}
	return &candidate, nil
}

func (s SqlProgrammeLevelStore) Delete(
	ctx context.Context,
	id string,
	deleteAt int64,
) (*model.ProgrammeLevel, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "delete_at", deleteAt)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{"programme_levels.id": id, "programme_levels.delete_at": int64(0)})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", id, err)
	}
	current := row.model()
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM classes
			 WHERE programme_level_id = ? AND delete_at = 0
		)`, id); err != nil {
		return nil, fmt.Errorf("check programme level archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict(
			"programme_level",
			"programme_level_has_active_classes",
			nil,
		)
	}
	result, err := tx.Exec(ctx, `
		UPDATE programme_levels SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0`, deleteAt, deleteAt, id)
	if err != nil {
		return nil, fmt.Errorf("archive programme level: %w", err)
	}
	if err := requireAffected(result, "programme_level", id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level delete: %w", err)
	}
	current.UpdateAt, current.DeleteAt = deleteAt, deleteAt
	return current, nil
}

func (s SqlProgrammeLevelStore) ArchiveWithAudit(ctx context.Context, input *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme_level", "archive", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme level archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLevelLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row programmeLevelRow
	query := s.programmeLevelsQuery.Where(sq.Eq{"programme_levels.id": input.ID, "programme_levels.delete_at": int64(0)})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme_level", input.ID, err)
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `SELECT EXISTS (SELECT 1 FROM classes WHERE programme_level_id = ? AND delete_at = 0)`, input.ID); err != nil {
		return nil, fmt.Errorf("check programme level archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("programme_level", "programme_level_has_active_classes", nil)
	}
	result, err := tx.Exec(ctx, `UPDATE programme_levels SET update_at = ?, delete_at = ? WHERE id = ? AND delete_at = 0`, input.ArchiveAt, input.ArchiveAt, input.ID)
	if err != nil {
		return nil, fmt.Errorf("archive programme level: %w", err)
	}
	if err := requireAffected(result, "programme_level", input.ID); err != nil {
		return nil, err
	}
	level := row.model()
	level.UpdateAt, level.DeleteAt = input.ArchiveAt, input.ArchiveAt
	encoded, appErr := model.EncodeAuditData(level.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete programme level archive audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme level archive: %w", err)
	}
	return level, nil
}

func lockProgrammeLevelLifecycle(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", programmeLevelLifecycleLock); err != nil {
		return fmt.Errorf("lock programme level lifecycle: %w", err)
	}
	return nil
}

func validateActiveProgrammeLevel(ctx context.Context, executor sqlxExecutor, id string) error {
	var exists bool
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM programme_levels WHERE id = ? AND delete_at = 0)`, id); err != nil {
		return fmt.Errorf("validate class programme level: %w", err)
	}
	if !exists {
		return store.NewErrReference("class", "classes_programme_level_id_fkey", nil)
	}
	return nil
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
