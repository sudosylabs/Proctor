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
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLProgrammeStore struct {
	*SQLStore
	programmesQuery sq.SelectBuilder
}

const programmeLifecycleLock = "proctor:programme-lifecycle"

type programmeRow struct {
	ID             string       `db:"id"`
	CreatedAt      time.Time    `db:"created_at"`
	UpdatedAt      time.Time    `db:"updated_at"`
	ArchivedAt     sql.NullTime `db:"archived_at"`
	Revision       int64        `db:"revision"`
	AcademicUnitID string       `db:"academic_unit_id"`
	Name           string       `db:"name"`
	DisplayName    string       `db:"display_name"`
	Description    string       `db:"description"`
}

func programmeSliceColumns() []string {
	return []string{
		"programmes.id",
		"programmes.created_at",
		"programmes.updated_at",
		"programmes.archived_at",
		"programmes.revision",
		"programmes.academic_unit_id",
		"programmes.name",
		"programmes.display_name",
		"programmes.description",
	}
}

func newSQLProgrammeStore(sqlStore *SQLStore) store.ProgrammeStore {
	s := &SQLProgrammeStore{SQLStore: sqlStore}
	s.programmesQuery = s.getQueryBuilder().
		Select(programmeSliceColumns()...).
		From("programmes")
	return s
}

func (s SQLProgrammeStore) Create(
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
			id, created_at, updated_at, archived_at, revision, academic_unit_id,
			name, display_name, description
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :academic_unit_id,
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

func (s SQLProgrammeStore) Save(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
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
			id, created_at, updated_at, archived_at, revision, academic_unit_id,
			name, display_name, description
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :academic_unit_id,
			:name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme save: %w", err)
	}
	return &candidate, nil
}

func (s SQLProgrammeStore) Get(ctx context.Context, id string) (*model.Programme, error) {
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.id":          id,
		"programmes.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", id, err)
	}
	return row.model()
}

func (s SQLProgrammeStore) GetByName(
	ctx context.Context,
	academicUnitID string,
	name string,
) (*model.Programme, error) {
	var row programmeRow
	query := s.programmesQuery.Where(sq.Eq{
		"programmes.academic_unit_id": academicUnitID,
		"programmes.name":             name,
		"programmes.archived_at":      nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", academicUnitID+"/"+name, err)
	}
	return row.model()
}

func (s SQLProgrammeStore) ListByAcademicUnit(
	ctx context.Context,
	academicUnitID string,
) ([]*model.Programme, error) {
	query := s.programmesQuery.
		Where(sq.Eq{
			"programmes.academic_unit_id": academicUnitID,
			"programmes.archived_at":      nil,
		}).
		OrderBy("programmes.name", "programmes.id")

	rows := []programmeRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list programmes by academic unit: %w", err)
	}
	programmes := make([]*model.Programme, 0, len(rows))
	for _, row := range rows {
		programme, err := row.model()
		if err != nil {
			return nil, err
		}
		programmes = append(programmes, programme)
	}
	return programmes, nil
}

func (s SQLProgrammeStore) SearchByAcademicUnit(
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
		"programmes.archived_at":      nil,
	}).Where("(programmes.name ILIKE ? OR programmes.display_name ILIKE ?)",
		"%"+term+"%", "%"+term+"%").
		OrderBy("programmes.name", "programmes.id").Limit(uint64(limit))
	rows := []programmeRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("search programmes: %w", err)
	}
	result := make([]*model.Programme, 0, len(rows))
	for _, row := range rows {
		programme, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, programme)
	}
	return result, nil
}

func (s SQLProgrammeStore) Update(ctx context.Context, programme *model.Programme) (*model.Programme, error) {
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
		   SET updated_at = :updated_at,
		       revision = :revision,
		       academic_unit_id = :academic_unit_id,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND archived_at IS NULL
		   AND revision = :expected_revision`, map[string]any{
		"id": candidate.ID.String(), "updated_at": row.UpdatedAt,
		"revision": candidate.Revision, "academic_unit_id": row.AcademicUnitID,
		"name": row.Name, "display_name": row.DisplayName,
		"description": row.Description, "expected_revision": candidate.Revision - 1,
	})
	if err != nil {
		return nil, fmt.Errorf("update programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := requireRevisionAffected(ctx, tx, result, "programme", "programmes", candidate.ID.String()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme update: %w", err)
	}
	return &candidate, nil
}

func (s SQLProgrammeStore) UpdateWithAudit(
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
		   SET updated_at = :updated_at, revision = :revision, name = :name,
		       display_name = :display_name, description = :description
		 WHERE id = :id AND academic_unit_id = :academic_unit_id AND archived_at IS NULL
		   AND revision = :expected_revision`, map[string]any{
		"id": candidate.ID.String(), "updated_at": row.UpdatedAt,
		"revision": candidate.Revision, "academic_unit_id": row.AcademicUnitID,
		"name": row.Name, "display_name": row.DisplayName,
		"description": row.Description, "expected_revision": candidate.Revision - 1,
	})
	if err != nil {
		return nil, fmt.Errorf("update programme: %w", translateError("programme", candidate.ID.String(), err))
	}
	if err := requireOwnedRevisionAffected(
		ctx, tx, result, "programme", "programmes", "academic_unit_id",
		candidate.ID.String(), candidate.AcademicUnitID.String(),
	); err != nil {
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

func (s SQLProgrammeStore) Archive(
	ctx context.Context,
	id string,
	archiveAt int64,
) (*model.Programme, error) {
	if archiveAt <= 0 {
		return nil, store.NewErrInvalidInput("programme", "archived_at", archiveAt)
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
	query := s.programmesQuery.Where(sq.Eq{"programmes.id": id, "programmes.archived_at": nil})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", id, err)
	}
	current, err := row.model()
	if err != nil {
		return nil, err
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (
			SELECT 1 FROM programme_levels
			 WHERE programme_id = ? AND archived_at IS NULL
		)`, id); err != nil {
		return nil, fmt.Errorf("check programme archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("programme", "programme_has_active_levels", nil)
	}
	result, err := tx.Exec(ctx, `
		UPDATE programmes SET updated_at = GREATEST(created_at, ?), archived_at = GREATEST(created_at, ?), revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(archiveAt), model.TimeFromMillis(archiveAt), id, current.Revision)
	if err != nil {
		return nil, fmt.Errorf("archive programme: %w", err)
	}
	if err := requireRevisionAffected(ctx, tx, result, "programme", "programmes", id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit programme archive: %w", err)
	}
	at := model.TimeFromMillis(archiveAt)
	current.UpdatedAt = at
	current.ArchivedAt = model.OptionalTimeFromMillis(archiveAt)
	current.Revision++
	return current, nil
}

func (s SQLProgrammeStore) ArchiveWithAudit(
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
		"programmes.id": input.ID, "programmes.archived_at": nil,
	})
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("programme", input.ID, err)
	}
	var dependent bool
	if err := tx.Get(ctx, &dependent, `
		SELECT EXISTS (SELECT 1 FROM programme_levels
		 WHERE programme_id = ? AND archived_at IS NULL)`, input.ID); err != nil {
		return nil, fmt.Errorf("check programme archive dependencies: %w", err)
	}
	if dependent {
		return nil, store.NewErrConflict("programme", "programme_has_active_levels", nil)
	}
	result, err := tx.Exec(ctx, `
		UPDATE programmes SET updated_at = GREATEST(created_at, ?), archived_at = GREATEST(created_at, ?), revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?`, model.TimeFromMillis(input.ArchiveAt), model.TimeFromMillis(input.ArchiveAt), input.ID, row.Revision)
	if err != nil {
		return nil, fmt.Errorf("archive programme: %w", err)
	}
	if err := requireRevisionAffected(ctx, tx, result, "programme", "programmes", input.ID); err != nil {
		return nil, err
	}
	programme, err := row.model()
	if err != nil {
		return nil, err
	}
	at := model.TimeFromMillis(input.ArchiveAt)
	programme.UpdatedAt = at
	programme.ArchivedAt = model.OptionalTimeFromMillis(input.ArchiveAt)
	programme.Revision++
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
		SELECT EXISTS (SELECT 1 FROM academic_units WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
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
		SELECT EXISTS (SELECT 1 FROM programmes WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
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
		CreatedAt:      UTCTime(programme.CreatedAt),
		UpdatedAt:      UTCTime(programme.UpdatedAt),
		ArchivedAt:     NullTimeFromOptional(programme.ArchivedAt),
		Revision:       programme.Revision,
		AcademicUnitID: programme.AcademicUnitID.String(),
		Name:           programme.Name,
		DisplayName:    programme.DisplayName,
		Description:    programme.Description,
	}
}

func (row programmeRow) model() (*model.Programme, error) {
	id, err := model.ParseProgrammeID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("rehydrate programme %q: %w", row.ID, err)
	}
	academicUnitID, err := model.ParseAcademicUnitID(row.AcademicUnitID)
	if err != nil {
		return nil, fmt.Errorf("rehydrate programme %q: %w", row.ID, err)
	}
	programme := &model.Programme{
		ID:             id,
		CreatedAt:      row.CreatedAt.UTC(),
		UpdatedAt:      row.UpdatedAt.UTC(),
		ArchivedAt:     OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:       row.Revision,
		AcademicUnitID: academicUnitID,
		Name:           row.Name,
		DisplayName:    row.DisplayName,
		Description:    row.Description,
	}
	if err := programme.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate programme %q: %w", row.ID, err)
	}
	return programme, nil
}

var _ store.ProgrammeStore = (*SQLProgrammeStore)(nil)
