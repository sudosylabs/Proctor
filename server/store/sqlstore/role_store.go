// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/role_store.go.
// Proctor retains a dedicated role store, reusable select builder, explicit
// row conversion, lifecycle validation, batch lookup, and soft deletion.

package sqlstore

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlRoleStore struct {
	*SqlStore
	rolesQuery sq.SelectBuilder
}

type roleRow struct {
	ID          string         `db:"id"`
	CreateAt    int64          `db:"create_at"`
	UpdateAt    int64          `db:"update_at"`
	DeleteAt    int64          `db:"delete_at"`
	Name        string         `db:"name"`
	DisplayName string         `db:"display_name"`
	Description string         `db:"description"`
	Permissions pq.StringArray `db:"permissions"`
	BuiltIn     bool           `db:"built_in"`
}

func roleSliceColumns() []string {
	return []string{
		"roles.id", "roles.create_at", "roles.update_at", "roles.delete_at",
		"roles.name", "roles.display_name", "roles.description",
		"roles.permissions", "roles.built_in",
	}
}

func newSqlRoleStore(sqlStore *SqlStore) store.RoleStore {
	s := &SqlRoleStore{SqlStore: sqlStore}
	s.rolesQuery = s.getQueryBuilder().Select(roleSliceColumns()...).From("roles")
	return s
}

func (s SqlRoleStore) Save(ctx context.Context, role *model.Role) (*model.Role, error) {
	if role == nil {
		return nil, store.NewErrInvalidInput("role", "value", nil)
	}
	if role.Id != "" {
		return nil, store.NewErrInvalidInput("role", "id", role.Id)
	}
	candidate := role.Clone()
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	if _, err := insertRole(ctx, s.GetMaster(), candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (s SqlRoleStore) SaveWithAudit(ctx context.Context, input *store.RoleCreation) (*model.Role, error) {
	if input == nil || input.Role == nil || input.Role.Id != "" ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "creation", nil)
	}
	candidate := input.Role.Clone()
	candidate.BuiltIn = false
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("role", "value", nil).Wrap(appErr)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited role creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited role creation: %w", err)
	}
	return candidate, nil
}

func insertRole(ctx context.Context, executor sqlxExecutor, role *model.Role) (roleRow, error) {
	row := newRoleRow(role)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO roles (
			id, create_at, update_at, delete_at, name, display_name,
			description, permissions, built_in
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :name, :display_name,
			:description, :permissions, :built_in
		)`, &row); err != nil {
		return roleRow{}, fmt.Errorf("save role: %w", translateError("role", role.Id, err))
	}
	return row, nil
}

func (s SqlRoleStore) Get(ctx context.Context, id string) (*model.Role, error) {
	return s.get(ctx, s.rolesQuery.Where(sq.Eq{"roles.id": id, "roles.delete_at": int64(0)}), id)
}

func (s SqlRoleStore) GetByName(ctx context.Context, name string) (*model.Role, error) {
	return s.get(
		ctx,
		s.rolesQuery.Where(sq.Eq{"roles.name": name, "roles.delete_at": int64(0)}),
		"name="+name,
	)
}

func (s SqlRoleStore) get(ctx context.Context, query sq.SelectBuilder, key string) (*model.Role, error) {
	var row roleRow
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("role", key, err)
	}
	return row.model(), nil
}

func (s SqlRoleStore) GetByIds(ctx context.Context, ids []string) ([]*model.Role, error) {
	if len(ids) == 0 {
		return []*model.Role{}, nil
	}
	return s.selectRoles(
		ctx,
		s.rolesQuery.
			Where(sq.Eq{"roles.id": ids, "roles.delete_at": int64(0)}).
			OrderBy("roles.name", "roles.id"),
		"get roles by ids",
	)
}

func (s SqlRoleStore) List(ctx context.Context) ([]*model.Role, error) {
	return s.selectRoles(
		ctx,
		s.rolesQuery.Where(sq.Eq{"roles.delete_at": int64(0)}).OrderBy("roles.name", "roles.id"),
		"list roles",
	)
}

func (s SqlRoleStore) selectRoles(
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
		roles = append(roles, row.model())
	}
	return roles, nil
}

func (s SqlRoleStore) Update(ctx context.Context, role *model.Role) (*model.Role, error) {
	if role == nil {
		return nil, store.NewErrInvalidInput("role", "value", nil)
	}
	if role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	candidate := role.Clone()
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	if err := updateRole(ctx, s.GetMaster(), candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (s SqlRoleStore) UpdateWithAudit(ctx context.Context, input *store.RoleUpdate) (*model.Role, error) {
	if input == nil || input.Role == nil || !model.IsValidId(input.Role.Id) ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "update", nil)
	}
	if input.Role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	candidate := input.Role.Clone()
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("role", "value", nil).Wrap(appErr)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited role update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateRole(ctx, tx, candidate); err != nil {
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited role update: %w", err)
	}
	return candidate, nil
}

func updateRole(ctx context.Context, executor sqlxExecutor, role *model.Role) error {
	row := newRoleRow(role)
	result, err := executor.NamedExec(ctx, `
		UPDATE roles
		   SET update_at = :update_at, name = :name, display_name = :display_name,
		       description = :description, permissions = :permissions
		 WHERE id = :id AND delete_at = 0 AND built_in = :built_in`, &row)
	if err != nil {
		return fmt.Errorf("update role: %w", translateError("role", role.Id, err))
	}
	return requireAffected(result, "role", role.Id)
}

func (s SqlRoleStore) Delete(ctx context.Context, id string, deleteAt int64) (*model.Role, error) {
	if deleteAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "delete_at", deleteAt)
	}
	role, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	if err := softDeleteRole(ctx, s.GetMaster(), id, deleteAt); err != nil {
		return nil, err
	}
	role.UpdateAt = deleteAt
	role.DeleteAt = deleteAt
	return role, nil
}

func (s SqlRoleStore) DeleteWithAudit(ctx context.Context, input *store.RoleDeletion) (*model.Role, error) {
	if input == nil || !model.IsValidId(input.ID) || input.DeleteAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role", "deletion", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited role deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row roleRow
	if err := tx.Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, name, display_name,
		       description, permissions, built_in
		  FROM roles
		 WHERE id = ? AND delete_at = 0
		 FOR UPDATE`, input.ID); err != nil {
		return nil, translateError("role", input.ID, err)
	}
	role := row.model()
	if role.BuiltIn {
		return nil, store.NewErrConflict("role", "roles_built_in_protected", nil)
	}
	if err := softDeleteRole(ctx, tx, input.ID, input.DeleteAt); err != nil {
		return nil, err
	}
	role.UpdateAt = input.DeleteAt
	role.DeleteAt = input.DeleteAt
	encoded, appErr := model.EncodeAuditData(role.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete role deletion audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited role deletion: %w", err)
	}
	return role, nil
}

func softDeleteRole(ctx context.Context, executor sqlxExecutor, id string, deleteAt int64) error {
	result, err := executor.Exec(ctx, `
		UPDATE roles SET update_at = ?, delete_at = ?
		 WHERE id = ? AND delete_at = 0 AND built_in = false`, deleteAt, deleteAt, id)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	return requireAffected(result, "role", id)
}

func newRoleRow(role *model.Role) roleRow {
	permissions := pq.StringArray(role.Permissions)
	if permissions == nil {
		permissions = pq.StringArray{}
	}
	return roleRow{
		ID: role.Id, CreateAt: role.CreateAt, UpdateAt: role.UpdateAt,
		DeleteAt: role.DeleteAt, Name: role.Name, DisplayName: role.DisplayName,
		Description: role.Description, Permissions: permissions,
		BuiltIn: role.BuiltIn,
	}
}

func (row roleRow) model() *model.Role {
	return &model.Role{
		Id: row.ID, CreateAt: row.CreateAt, UpdateAt: row.UpdateAt,
		DeleteAt: row.DeleteAt, Name: row.Name, DisplayName: row.DisplayName,
		Description: row.Description, Permissions: append([]string(nil), row.Permissions...),
		BuiltIn: row.BuiltIn,
	}
}

var _ store.RoleStore = (*SqlRoleStore)(nil)
