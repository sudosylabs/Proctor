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
	row := newRoleRow(candidate)
	if _, err := s.GetMaster().NamedExec(ctx, `
		INSERT INTO roles (
			id, create_at, update_at, delete_at, name, display_name,
			description, permissions, built_in
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :name, :display_name,
			:description, :permissions, :built_in
		)`, &row); err != nil {
		return nil, fmt.Errorf("save role: %w", translateError("role", candidate.Id, err))
	}
	return candidate, nil
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
	candidate := role.Clone()
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	row := newRoleRow(candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE roles
		   SET update_at = :update_at, name = :name, display_name = :display_name,
		       description = :description, permissions = :permissions
		 WHERE id = :id AND delete_at = 0 AND built_in = :built_in`, &row)
	if err != nil {
		return nil, fmt.Errorf("update role: %w", translateError("role", candidate.Id, err))
	}
	if err := requireAffected(result, "role", candidate.Id); err != nil {
		return nil, err
	}
	return candidate, nil
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
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE roles SET update_at = $1, delete_at = $1
		 WHERE id = $2 AND delete_at = 0 AND built_in = false`, deleteAt, id)
	if err != nil {
		return nil, fmt.Errorf("delete role: %w", err)
	}
	if err := requireAffected(result, "role", id); err != nil {
		return nil, err
	}
	role.UpdateAt = deleteAt
	role.DeleteAt = deleteAt
	return role, nil
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
