// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/role_store.go.
// Proctor retains a dedicated role store, reusable select builder, explicit
// row conversion, lifecycle validation, batch lookup, and archival.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLRoleStore struct {
	*SQLStore
	rolesQuery sq.SelectBuilder
}

// roleRow is the legacy integer-millisecond column layout. Domain Role uses
// time.Time / OptionalTime; conversion is at this boundary.
type roleRow struct {
	ID          string         `db:"id"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	ArchivedAt  sql.NullTime   `db:"archived_at"`
	Name        string         `db:"name"`
	DisplayName string         `db:"display_name"`
	Description string         `db:"description"`
	Permissions pq.StringArray `db:"permissions"`
	BuiltIn     bool           `db:"built_in"`
}

func roleSliceColumns() []string {
	return []string{
		"roles.id", "roles.created_at", "roles.updated_at", "roles.archived_at",
		"roles.name", "roles.display_name", "roles.description",
		"roles.permissions", "roles.built_in",
	}
}

func newSQLRoleStore(sqlStore *SQLStore) store.RoleStore {
	s := &SQLRoleStore{SQLStore: sqlStore}
	s.rolesQuery = s.getQueryBuilder().Select(roleSliceColumns()...).From("roles")
	return s
}

func (s SQLRoleStore) Save(ctx context.Context, role *model.Role) (*model.Role, error) {
	if role == nil {
		return nil, store.NewErrInvalidInput("role", "value", nil)
	}
	if !role.ID.IsZero() {
		return nil, store.NewErrInvalidInput("role", "id", role.ID.String())
	}
	candidate := role.Clone()
	candidate.PrepareCreate(model.NewRoleID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if _, err := insertRole(ctx, s.GetMaster(), candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (s SQLRoleStore) SaveWithAudit(ctx context.Context, input *store.RoleCreation) (*model.Role, error) {
	if input == nil || input.Role == nil || !input.Role.ID.IsZero() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "creation", nil)
	}
	candidate := input.Role.Clone()
	candidate.BuiltIn = false
	at := model.TimeFromMillis(input.AuditAt)
	if at.IsZero() {
		at = model.NowUTC()
	}
	candidate.PrepareCreate(model.NewRoleID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("role", "value", nil).Wrap(err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited role creation", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Role, error) {
		if _, err := insertRole(ctx, tx, candidate); err != nil {
			return nil, err
		}
		encoded, appErr := model.EncodeAuditData(candidate.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
		); err != nil {
			return nil, fmt.Errorf("complete role creation audit: %w", err)
		}
		return candidate, nil
	})
}

func insertRole(ctx context.Context, executor sqlxExecutor, role *model.Role) (roleRow, error) {
	row := newRoleRow(role)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO roles (
			id, created_at, updated_at, archived_at, name, display_name,
			description, permissions, built_in
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :name, :display_name,
			:description, :permissions, :built_in
		)`, &row); err != nil {
		return roleRow{}, fmt.Errorf("save role: %w", translateError("role", role.ID.String(), err))
	}
	return row, nil
}

func (s SQLRoleStore) Get(ctx context.Context, id string) (*model.Role, error) {
	return s.get(ctx, s.rolesQuery.Where(sq.Eq{"roles.id": id, "roles.archived_at": nil}), id)
}

func (s SQLRoleStore) GetByName(ctx context.Context, name string) (*model.Role, error) {
	return s.get(
		ctx,
		s.rolesQuery.Where(sq.Eq{"roles.name": name, "roles.archived_at": nil}),
		"name="+name,
	)
}

func (s SQLRoleStore) get(ctx context.Context, query sq.SelectBuilder, key string) (*model.Role, error) {
	var row roleRow
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("role", key, err)
	}
	return row.model()
}

func (s SQLRoleStore) GetByIds(ctx context.Context, ids []string) ([]*model.Role, error) {
	if len(ids) == 0 {
		return []*model.Role{}, nil
	}
	return s.selectRoles(
		ctx,
		s.rolesQuery.
			Where(sq.Eq{"roles.id": ids, "roles.archived_at": nil}).
			OrderBy("roles.name", "roles.id"),
		"get roles by ids",
	)
}

func (s SQLRoleStore) List(ctx context.Context) ([]*model.Role, error) {
	return s.selectRoles(
		ctx,
		s.rolesQuery.Where(sq.Eq{"roles.archived_at": nil}).OrderBy("roles.name", "roles.id"),
		"list roles",
	)
}

func (s SQLRoleStore) selectRoles(
	ctx context.Context,
	query sq.SelectBuilder,
	operation string,
) ([]*model.Role, error) {
	rows := []roleRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	roles := make([]*model.Role, 0, len(rows))
	for _, row := range rows {
		role, err := row.model()
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func (s SQLRoleStore) Update(ctx context.Context, role *model.Role) (*model.Role, error) {
	if role == nil {
		return nil, store.NewErrInvalidInput("role", "value", nil)
	}
	if role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	expectedUpdatedAt := role.UpdatedAt
	candidate := role.Clone()
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if err := updateRole(ctx, s.GetMaster(), candidate, expectedUpdatedAt); err != nil {
		return nil, err
	}
	// Update is a legacy model-oriented Store operation. Advancing the caller's
	// snapshot lets a subsequent update carry the new optimistic concurrency
	// token while concurrent clones still compete on the same prior instant.
	*role = *candidate.Clone()
	return candidate, nil
}

func (s SQLRoleStore) UpdateWithAudit(ctx context.Context, input *store.RoleUpdate) (*model.Role, error) {
	if input == nil || input.Role == nil || !input.Role.ID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "update", nil)
	}
	if input.Role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	expectedUpdatedAt := input.Role.UpdatedAt
	candidate := input.Role.Clone()
	at := model.TimeFromMillis(input.AuditAt)
	if at.IsZero() {
		at = model.NowUTC()
	}
	candidate.PrepareUpdate(at)
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("role", "value", nil).Wrap(err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited role update", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Role, error) {
		if err := updateRole(ctx, tx, candidate, expectedUpdatedAt); err != nil {
			return nil, err
		}
		encoded, appErr := model.EncodeAuditData(candidate.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
		); err != nil {
			return nil, fmt.Errorf("complete role update audit: %w", err)
		}
		return candidate, nil
	})
}

func updateRole(ctx context.Context, executor sqlxExecutor, role *model.Role, expectedUpdatedAt time.Time) error {
	row := newRoleRow(role)
	result, err := executor.Exec(ctx, `
		UPDATE roles
		   SET updated_at = ?, name = ?, display_name = ?,
		       description = ?, permissions = ?
		 WHERE id = ? AND archived_at IS NULL AND built_in = ? AND updated_at = ?`,
		row.UpdatedAt, row.Name, row.DisplayName, row.Description, row.Permissions,
		row.ID, row.BuiltIn, expectedUpdatedAt)
	if err != nil {
		return fmt.Errorf("update role: %w", translateError("role", role.ID.String(), err))
	}
	return requireRevisionAffected(ctx, executor, result, "role", "roles", role.ID.String())
}

func (s SQLRoleStore) Archive(ctx context.Context, id string, archiveAt int64) (*model.Role, error) {
	if archiveAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "archived_at", archiveAt)
	}
	role, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	if err := archiveRole(ctx, s.GetMaster(), id, archiveAt); err != nil {
		return nil, err
	}
	at := model.TimeFromMillis(archiveAt)
	role.UpdatedAt = at
	role.ArchivedAt = model.OptionalTimeFrom(at)
	return role, nil
}

func (s SQLRoleStore) ArchiveWithAudit(ctx context.Context, input *store.RoleArchive) (*model.Role, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ArchiveAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "archive", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited role archive", func(ctx context.Context, tx *sqlxTxWrapper) (*model.Role, error) {
		var row roleRow
		if err := tx.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, name, display_name,
		       description, permissions, built_in
		  FROM roles
		 WHERE id = ? AND archived_at IS NULL
		 FOR UPDATE`, input.ID); err != nil {
			return nil, translateError("role", input.ID, err)
		}
		role, err := row.model()
		if err != nil {
			return nil, err
		}
		if role.BuiltIn {
			return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
		}
		if err := archiveRole(ctx, tx, input.ID, input.ArchiveAt); err != nil {
			return nil, err
		}
		at := model.TimeFromMillis(input.ArchiveAt)
		role.UpdatedAt = at
		role.ArchivedAt = model.OptionalTimeFrom(at)
		encoded, appErr := model.EncodeAuditData(role.Auditable())
		if appErr != nil {
			return nil, appErr
		}
		if _, err := completeAuditEvent(
			ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
		); err != nil {
			return nil, fmt.Errorf("complete role archive audit: %w", err)
		}
		return role, nil
	})
}

func archiveRole(ctx context.Context, executor sqlxExecutor, id string, archiveAt int64) error {
	result, err := executor.Exec(ctx, `
		UPDATE roles SET updated_at = ?, archived_at = ?
		 WHERE id = ? AND archived_at IS NULL AND built_in = false`, model.TimeFromMillis(archiveAt), model.TimeFromMillis(archiveAt), id)
	if err != nil {
		return fmt.Errorf("archive role: %w", err)
	}
	return requireAffected(result, "role", id)
}

func newRoleRow(role *model.Role) roleRow {
	permissions := pq.StringArray(role.Permissions)
	if permissions == nil {
		permissions = pq.StringArray{}
	}
	return roleRow{
		ID: role.ID.String(), CreatedAt: UTCTime(role.CreatedAt),
		UpdatedAt: UTCTime(role.UpdatedAt), ArchivedAt: NullTimeFromOptional(role.ArchivedAt),
		Name: role.Name, DisplayName: role.DisplayName,
		Description: role.Description, Permissions: permissions,
		BuiltIn: role.BuiltIn,
	}
}

func (row roleRow) model() (*model.Role, error) {
	id, err := parsePersistedID("role", "id", row.ID, model.ParseRoleID)
	if err != nil {
		return nil, err
	}
	value := &model.Role{
		ID: id, CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(), ArchivedAt: OptionalTimeFromNullTime(row.ArchivedAt),
		Name: row.Name, DisplayName: row.DisplayName,
		Description: row.Description, Permissions: append([]string(nil), row.Permissions...),
		BuiltIn: row.BuiltIn,
	}
	if err := validatePersistedModel("role", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.RoleStore = (*SQLRoleStore)(nil)
