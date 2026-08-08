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

const programmeLifecycleLock = "proctor:programme-lifecycle"

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

func (s SqlProgrammeStore) Create(
	ctx context.Context,
	input *store.ProgrammeCreation,
) (*model.Programme, error) {
	if input == nil || input.Programme == nil ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme", "creation", nil)
	}
	if !input.Programme.ID.IsValid() {
		return nil, store.NewErrInvalidInput("programme", "id", input.Programme.ID.String())
	}
	candidate := *input.Programme
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveAcademicUnit(ctx, tx, candidate.AcademicUnitID.String()); err != nil {
		return nil, err
	}
	row := newProgrammeRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO programmes (
			id, create_at, update_at, delete_at, academic_unit_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :academic_unit_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("create programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete programme creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme creation: %w", err)
	}
	return &candidate, nil
}

func (s SqlProgrammeStore) Save(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
	if programme == nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	if !programme.ID.IsZero() {
		return nil, store.NewErrInvalidInput("programme", "id", programme.ID.String())
	}

	id, err := model.ParseProgrammeID(model.NewId())
	if err != nil {
		return nil, err
	}
	candidate := *programme
	candidate.PrepareCreate(id, model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil).Wrap(err)
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveAcademicUnit(ctx, tx, candidate.AcademicUnitID.String()); err != nil {
		return nil, err
	}
	row := newProgrammeRow(&candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO programmes (
			id, create_at, update_at, delete_at, academic_unit_id,
			name, display_name, description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :academic_unit_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme save: %w", err)
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

func (s SqlProgrammeStore) SearchByAcademicUnit(
	ctx context.Context,
	academicUnitID string,
	term string,
	limit int,
) ([]*model.Programme, error) {
	if limit < 1 || limit > 200 {
		return nil, store.NewErrInvalidInput("programme", "limit", limit)
	}
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.academic_unit_id": academicUnitID,
		"programmes.delete_at":        int64(0),
	}).Where("(programmes.name ILIKE ? OR programmes.display_name ILIKE ?)",
		"%"+term+"%", "%"+term+"%").
		OrderBy("programmes.name", "programmes.id").Limit(uint64(limit))
	rows := []programmeRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search programmes: %w", err)
	}
	result := make([]*model.Programme, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.model())
	}
	return result, nil
}

func (s SqlProgrammeStore) Update(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
	if programme == nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil)
	}
	candidate := *programme
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil).Wrap(err)
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateActiveAcademicUnit(ctx, tx, candidate.AcademicUnitID.String()); err != nil {
		return nil, err
	}
	row := newProgrammeRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE programmes
		   SET update_at = :update_at,
		       academic_unit_id = :academic_unit_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := requireAffected(result, "programme", candidate.ID.String()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme update: %w", err)
	}
	return &candidate, nil
}

func (s SqlProgrammeStore) UpdateWithAudit(
	ctx context.Context,
	input *store.ProgrammeUpdate,
) (*model.Programme, error) {
	if input == nil || input.Programme == nil ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme", "update", nil)
	}
	candidate := *input.Programme
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("programme", "value", nil).Wrap(err)
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row := newProgrammeRow(&candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE programmes
		   SET update_at = :update_at, name = :name,
		       display_name = :display_name, description = :description
		 WHERE id = :id AND academic_unit_id = :academic_unit_id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := requireAffected(result, "programme", candidate.ID.String()); err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete programme update audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme update: %w", err)
	}
	return &candidate, nil
}

func (s SqlProgrammeStore) Delete(
	ctx context.Context,
	id string,
	deleteAt int64,
) (*model.Programme, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("programme", "delete_at", deleteAt)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{"programmes.id": id, "programmes.delete_at": int64(0)})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", id, err)
	}
	current := row.model()
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM programme_levels
			 WHERE programme_id = ? AND delete_at = 0
		)`, id); err != nil {
		return nil, fmt.Errorf("check programme archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("programme", "programme_has_active_levels", nil)
	}
	result, err := tx.Exec(ctx, `
		UPDATE programmes SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0`, deleteAt, deleteAt, id)
	if err != nil {
		return nil, fmt.Errorf("archive programme: %w", err)
	}
	if err := requireAffected(result, "programme", id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme delete: %w", err)
	}
	at := model.TimeFromMillis(deleteAt)
	current.UpdatedAt = at
	current.ArchivedAt = model.OptionalTimeFromMillis(deleteAt)
	return current, nil
}

func (s SqlProgrammeStore) ArchiveWithAudit(
	ctx context.Context,
	input *store.ProgrammeArchive,
) (*model.Programme, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("programme", "archive", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin programme archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProgrammeLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.id": input.ID, "programmes.delete_at": int64(0),
	})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", input.ID, err)
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (SELECT 1 FROM programme_levels
		 WHERE programme_id = ? AND delete_at = 0)`, input.ID); err != nil {
		return nil, fmt.Errorf("check programme archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("programme", "programme_has_active_levels", nil)
	}
	result, err := tx.Exec(ctx, `
		UPDATE programmes SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0`, input.ArchiveAt, input.ArchiveAt, input.ID)
	if err != nil {
		return nil, fmt.Errorf("archive programme: %w", err)
	}
	if err := requireAffected(result, "programme", input.ID); err != nil {
		return nil, err
	}
	programme := row.model()
	at := model.TimeFromMillis(input.ArchiveAt)
	programme.UpdatedAt = at
	programme.ArchivedAt = model.OptionalTimeFromMillis(input.ArchiveAt)
	encoded, appErr := model.EncodeAuditData(programme.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete programme archive audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme archive: %w", err)
	}
	return programme, nil
}

func lockProgrammeLifecycle(ctx context.Context, tx sqlxExecutor) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", programmeLifecycleLock); err != nil {
		return fmt.Errorf("lock programme lifecycle: %w", err)
	}
	return nil
}

func validateActiveAcademicUnit(ctx context.Context, executor sqlxExecutor, id string) error {
	var exists bool
	if err := executor.Get(ctx, &exists, `
		SELECT EXISTS (SELECT 1 FROM academic_units WHERE id = ? AND delete_at = 0)`, id); err != nil {
		return fmt.Errorf("validate programme academic unit: %w", err)
	}
	if !exists {
		return store.NewErrReference("programme", "programmes_academic_unit_id_fkey", nil)
	}
	return nil
}

func validateActiveProgramme(ctx context.Context, executor sqlxExecutor, id string) error {
	var exists bool
	if err := executor.Get(ctx, &exists, `
		SELECT EXISTS (SELECT 1 FROM programmes WHERE id = ? AND delete_at = 0)`, id); err != nil {
		return fmt.Errorf("validate programme level programme: %w", err)
	}
	if !exists {
		return store.NewErrReference("programme_level", "programme_levels_programme_id_fkey", nil)
	}
	return nil
}

func newProgrammeRow(programme *model.Programme) programmeRow {
	return programmeRow{
		ID:             programme.ID.String(),
		CreateAt:       model.MillisFromTime(programme.CreatedAt),
		UpdateAt:       model.MillisFromTime(programme.UpdatedAt),
		DeleteAt:       programme.ArchivedAt.Millis(),
		AcademicUnitID: programme.AcademicUnitID.String(),
		Name:           programme.Name,
		DisplayName:    programme.DisplayName,
		Description:    programme.Description,
	}
}

func (row programmeRow) model() *model.Programme {
	id, err := model.ParseProgrammeID(row.ID)
	if err != nil {
		id = model.ProgrammeID(row.ID)
	}
	academicUnitID, err := model.ParseAcademicUnitID(row.AcademicUnitID)
	if err != nil {
		academicUnitID = model.AcademicUnitID(row.AcademicUnitID)
	}
	return &model.Programme{
		ID:             id,
		CreatedAt:      model.TimeFromMillis(row.CreateAt),
		UpdatedAt:      model.TimeFromMillis(row.UpdateAt),
		ArchivedAt:     model.OptionalTimeFromMillis(row.DeleteAt),
		Revision:       1,
		AcademicUnitID: academicUnitID,
		Name:           row.Name,
		DisplayName:    row.DisplayName,
		Description:    row.Description,
	}
}

var _ store.ProgrammeStore = (*SqlProgrammeStore)(nil)
