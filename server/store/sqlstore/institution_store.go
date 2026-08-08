// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/team_store.go. Proctor
// retains the per-model Sql<Model>Store, embedded root store, reusable select
// builder, named writes, model lifecycle, and store-error boundary while
// implementing Proctor's singleton-institution semantics.

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

type SqlInstitutionStore struct {
	*SqlStore
	institutionsQuery sq.SelectBuilder
}

type institutionRow struct {
	ID          string       `db:"id"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
	ArchivedAt  sql.NullTime `db:"archived_at"`
	Revision    int64        `db:"revision"`
	Name        string       `db:"name"`
	DisplayName string       `db:"display_name"`
	Description string       `db:"description"`
}

func institutionSliceColumns() []string {
	return []string{
		"institutions.id",
		"institutions.created_at",
		"institutions.updated_at",
		"institutions.archived_at",
		"institutions.revision",
		"institutions.name",
		"institutions.display_name",
		"institutions.description",
	}
}

func newSqlInstitutionStore(sqlStore *SqlStore) store.InstitutionStore {
	s := &SqlInstitutionStore{SqlStore: sqlStore}
	s.institutionsQuery = s.getQueryBuilder().
		Select(institutionSliceColumns()...).
		From("institutions")
	return s
}

func (s SqlInstitutionStore) Save(ctx context.Context, institution *model.Institution) (*model.Institution, error) {
	if institution == nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil)
	}
	if !institution.ID.IsZero() {
		return nil, store.NewErrInvalidInput("institution", "id", institution.ID.String())
	}

	id, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		return nil, err
	}
	at := model.NowUTC()
	created, err := model.NewInstitution(
		id, institution.Name, institution.DisplayName, institution.Description, at,
	)
	if err != nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil).Wrap(err)
	}

	row := newInstitutionRow(created)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO institutions (
			id, created_at, updated_at, archived_at, revision, name, display_name, description
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :name, :display_name, :description
		)`, &row); err != nil {
		return nil, fmt.Errorf("save institution: %w", translateError("institution", created.ID.String(), err))
	}
	return created, nil
}

func (s SqlInstitutionStore) Get(ctx context.Context, id string) (*model.Institution, error) {
	var row institutionRow
	query := s.institutionsQuery.Where(sq.Eq{
		"institutions.id":          id,
		"institutions.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("institution", id, err)
	}
	return row.model()
}

func (s SqlInstitutionStore) GetSingleton(ctx context.Context) (*model.Institution, error) {
	var row institutionRow
	query := s.institutionsQuery.Where(sq.Eq{
		"institutions.singleton":   true,
		"institutions.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("institution", "singleton", err)
	}
	return row.model()
}

func (s SqlInstitutionStore) Update(ctx context.Context, institution *model.Institution) (*model.Institution, error) {
	if institution == nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil)
	}
	candidate := *institution
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil).Wrap(err)
	}
	return s.updateInstitution(ctx, &candidate, "", 0)
}

func (s SqlInstitutionStore) UpdateWithAudit(
	ctx context.Context,
	input *store.InstitutionUpdate,
) (*model.Institution, error) {
	if input == nil || input.Institution == nil ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("institution", "update", nil)
	}
	candidate := *input.Institution
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil).Wrap(err)
	}
	return s.updateInstitution(ctx, &candidate, input.AuditEventID, input.AuditAt)
}

func (s SqlInstitutionStore) updateInstitution(
	ctx context.Context,
	candidate *model.Institution,
	auditEventID string,
	auditAt int64,
) (*model.Institution, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin institution update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := newInstitutionRow(candidate)
	result, err := tx.NamedExec(ctx, `
		UPDATE institutions
		   SET updated_at = :updated_at,
		       revision = :revision,
		       name = :name,
		       display_name = :display_name,
		       description = :description
		 WHERE id = :id AND singleton = TRUE AND archived_at IS NULL
		   AND revision = :expected_revision`, map[string]any{
		"id": candidate.ID.String(), "updated_at": row.UpdatedAt,
		"revision": candidate.Revision, "name": row.Name,
		"display_name": row.DisplayName, "description": row.Description,
		"expected_revision": candidate.Revision - 1,
	})
	if err != nil {
		return nil, fmt.Errorf("update institution: %w", translateError("institution", candidate.ID.String(), err))
	}
	if err := requireRevisionAffected(ctx, tx, result, "institution", "institutions", candidate.ID.String()); err != nil {
		return nil, err
	}
	if auditEventID != "" {
		encoded, appErr := model.EncodeAuditData(candidate.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, auditEventID, model.AuditStatusSuccess, "", encoded, auditAt,
		); err != nil {
			return nil, fmt.Errorf("complete institution update audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit institution update: %w", err)
	}
	return candidate, nil
}

func (s SqlInstitutionStore) Archive(ctx context.Context, id string, archiveAt int64) error {
	if archiveAt <= 0 {
		return store.NewErrInvalidInput("institution", "archived_at", archiveAt)
	}
	result, err := s.GetMaster().Exec(
		ctx,
		"UPDATE institutions SET updated_at = ?, archived_at = ?, revision = revision + 1 WHERE id = ? AND archived_at IS NULL",
		model.TimeFromMillis(archiveAt),
		model.TimeFromMillis(archiveAt),
		id,
	)
	if err != nil {
		return fmt.Errorf("archive institution: %w", translateError("institution", id, err))
	}
	return requireAffected(result, "institution", id)
}

func newInstitutionRow(institution *model.Institution) institutionRow {
	return institutionRow{
		ID:          institution.ID.String(),
		CreatedAt:   UTCTime(institution.CreatedAt),
		UpdatedAt:   UTCTime(institution.UpdatedAt),
		ArchivedAt:  NullTimeFromOptional(institution.ArchivedAt),
		Revision:    institution.Revision,
		Name:        institution.Name,
		DisplayName: institution.DisplayName,
		Description: institution.Description,
	}
}

func (row institutionRow) model() (*model.Institution, error) {
	id, err := model.ParseInstitutionID(row.ID)
	if err != nil {
		return nil, fmt.Errorf("rehydrate institution %q: %w", row.ID, err)
	}
	institution := &model.Institution{
		ID:          id,
		CreatedAt:   row.CreatedAt.UTC(),
		UpdatedAt:   row.UpdatedAt.UTC(),
		ArchivedAt:  OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:    row.Revision,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		Description: row.Description,
	}
	if err := institution.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate institution %q: %w", row.ID, err)
	}
	return institution, nil
}

func requireAffected(result sql.Result, resource, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s affected rows: %w", resource, err)
	}
	if affected == 0 {
		return store.NewErrNotFound(resource, id).Wrap(sql.ErrNoRows)
	}
	return nil
}

func requireRevisionAffected(
	ctx context.Context,
	executor sqlxExecutor,
	result sql.Result,
	resource, table, id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s affected rows: %w", resource, err)
	}
	if affected != 0 {
		return nil
	}
	var exists bool
	query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = ? AND archived_at IS NULL)", table)
	if err := executor.Get(ctx, &exists, query, id); err != nil {
		return fmt.Errorf("check %s revision conflict: %w", resource, err)
	}
	if exists {
		return store.NewErrConflict(resource, resource+"_changed", nil)
	}
	return store.NewErrNotFound(resource, id).Wrap(sql.ErrNoRows)
}

func requireOwnedRevisionAffected(
	ctx context.Context,
	executor sqlxExecutor,
	result sql.Result,
	resource, table, ownerColumn, id, expectedOwner string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s affected rows: %w", resource, err)
	}
	if affected != 0 {
		return nil
	}
	var owner string
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = ? AND archived_at IS NULL", ownerColumn, table)
	if err := executor.Get(ctx, &owner, query, id); err != nil {
		return translateError(resource, id, err)
	}
	if owner != expectedOwner {
		return store.NewErrNotFound(resource, id).Wrap(sql.ErrNoRows)
	}
	return store.NewErrConflict(resource, resource+"_changed", nil)
}

var _ store.InstitutionStore = (*SqlInstitutionStore)(nil)
