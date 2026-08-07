// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Structurally adapted from Mattermost's per-model membership and role stores.
// Scope validation, interval overlap protection, and scoped inheritance inputs
// are Proctor-specific.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlRoleBindingStore struct {
	*SqlStore
	bindingsQuery sq.SelectBuilder
}

type roleBindingRow struct {
	ID        string              `db:"id"`
	CreateAt  int64               `db:"create_at"`
	UpdateAt  int64               `db:"update_at"`
	DeleteAt  int64               `db:"delete_at"`
	UserID    string              `db:"user_id"`
	RoleID    string              `db:"role_id"`
	ScopeType model.RoleScopeType `db:"scope_type"`
	ScopeID   string              `db:"scope_id"`
	StartAt   int64               `db:"start_at"`
	EndAt     int64               `db:"end_at"`
}

func roleBindingSliceColumns() []string {
	return []string{
		"role_bindings.id", "role_bindings.create_at", "role_bindings.update_at",
		"role_bindings.delete_at", "role_bindings.user_id", "role_bindings.role_id",
		"role_bindings.scope_type", "role_bindings.scope_id",
		"role_bindings.start_at", "role_bindings.end_at",
	}
}

func newSqlRoleBindingStore(sqlStore *SqlStore) store.RoleBindingStore {
	s := &SqlRoleBindingStore{SqlStore: sqlStore}
	s.bindingsQuery = s.getQueryBuilder().
		Select(roleBindingSliceColumns()...).
		From("role_bindings")
	return s
}

func (s SqlRoleBindingStore) Save(
	ctx context.Context,
	binding *model.RoleBinding,
) (*model.RoleBinding, error) {
	if binding == nil {
		return nil, store.NewErrInvalidInput("role_binding", "value", nil)
	}
	if binding.Id != "" {
		return nil, store.NewErrInvalidInput("role_binding", "id", binding.Id)
	}
	candidate := *binding
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin role binding save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertRoleBinding(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit role binding save: %w", err)
	}
	return &candidate, nil
}

func (s SqlRoleBindingStore) SaveWithAudit(
	ctx context.Context,
	input *store.RoleBindingCreation,
) (*model.RoleBinding, error) {
	if input == nil || input.Binding == nil || input.Binding.Id != "" ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "creation", nil)
	}
	candidate := *input.Binding
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("role_binding", "value", nil).Wrap(appErr)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited role binding save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertRoleBinding(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(candidate.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete role binding creation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited role binding save: %w", err)
	}
	return &candidate, nil
}

func insertRoleBinding(ctx context.Context, tx *sqlxTxWrapper, candidate *model.RoleBinding) error {
	if candidate.ScopeType == model.RoleScopeClass {
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return err
		}
	}
	lockKey := candidate.UserId + ":" + candidate.RoleId + ":" +
		string(candidate.ScopeType) + ":" + candidate.ScopeId
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock role binding grant: %w", err)
	}
	if err := validateRoleBindingReferences(ctx, tx, candidate); err != nil {
		return err
	}
	if err := validateSystemAdministratorScope(ctx, tx, candidate); err != nil {
		return err
	}
	var overlap bool
	if err := tx.Get(ctx, &overlap, `
		SELECT EXISTS (
			SELECT 1 FROM role_bindings
			 WHERE user_id = $1 AND role_id = $2
			   AND scope_type = $3 AND scope_id = $4
			   AND delete_at = 0
			   AND start_at < CASE WHEN $6 = 0 THEN 9223372036854775807 ELSE $6 END
			   AND (end_at = 0 OR end_at > $5)
		)`, candidate.UserId, candidate.RoleId, candidate.ScopeType,
		candidate.ScopeId, candidate.StartAt, candidate.EndAt); err != nil {
		return fmt.Errorf("check role binding overlap: %w", err)
	}
	if overlap {
		return store.NewErrConflict(
			"role_binding", "role_bindings_effective_range_key", nil,
		)
	}
	row := newRoleBindingRow(candidate)
	if _, err := tx.NamedExec(ctx, `
		INSERT INTO role_bindings (
			id, create_at, update_at, delete_at, user_id, role_id,
			scope_type, scope_id, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id, :role_id,
			:scope_type, :scope_id, :start_at, :end_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save role binding: %w",
			translateError("role_binding", candidate.Id, err),
		)
	}
	return nil
}

func validateRoleBindingReferences(
	ctx context.Context,
	executor sqlxExecutor,
	binding *model.RoleBinding,
) error {
	checks := []struct {
		table, id, constraint string
	}{
		{"users", binding.UserId, "role_bindings_user_id_fkey"},
		{"roles", binding.RoleId, "role_bindings_role_id_fkey"},
	}
	switch binding.ScopeType {
	case model.RoleScopeInstitution:
		checks = append(checks, struct{ table, id, constraint string }{
			"institutions", binding.ScopeId, "role_bindings_institution_scope_fkey",
		})
	case model.RoleScopeAcademicUnit:
		checks = append(checks, struct{ table, id, constraint string }{
			"academic_units", binding.ScopeId, "role_bindings_academic_unit_scope_fkey",
		})
	case model.RoleScopeClass:
		checks = append(checks, struct{ table, id, constraint string }{
			"classes", binding.ScopeId, "role_bindings_class_scope_fkey",
		})
	}
	for _, check := range checks {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1 AND delete_at = 0)`, check.table)
		if err := executor.Get(ctx, &exists, query, check.id); err != nil {
			return fmt.Errorf("validate role binding reference: %w", err)
		}
		if !exists {
			return store.NewErrReference("role_binding", check.constraint, sql.ErrNoRows)
		}
	}
	return nil
}

func validateSystemAdministratorScope(
	ctx context.Context,
	executor sqlxExecutor,
	binding *model.RoleBinding,
) error {
	var roleName string
	if err := executor.Get(
		ctx,
		&roleName,
		`SELECT name FROM roles WHERE id = $1 AND delete_at = 0`,
		binding.RoleId,
	); err != nil {
		return translateError("role", binding.RoleId, err)
	}
	if roleName == model.SystemAdministratorRoleName &&
		binding.ScopeType != model.RoleScopeInstitution {
		return store.NewErrConflict(
			"role_binding",
			"role_bindings_system_admin_institution_scope",
			nil,
		)
	}
	return nil
}

func (s SqlRoleBindingStore) Get(ctx context.Context, id string) (*model.RoleBinding, error) {
	var row roleBindingRow
	query := s.bindingsQuery.Where(sq.Eq{
		"role_bindings.id": id, "role_bindings.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("role_binding", id, err)
	}
	return row.model(), nil
}

func (s SqlRoleBindingStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.RoleBinding, error) {
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{"role_bindings.user_id": userID, "role_bindings.delete_at": int64(0)}).
		OrderBy("role_bindings.start_at", "role_bindings.id"), "list role bindings by user")
}

func (s SqlRoleBindingStore) ListByScope(
	ctx context.Context,
	scopeType model.RoleScopeType,
	scopeID string,
) ([]*model.RoleBinding, error) {
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{
			"role_bindings.scope_type": scopeType, "role_bindings.scope_id": scopeID,
			"role_bindings.delete_at": int64(0),
		}).
		OrderBy("role_bindings.start_at", "role_bindings.id"), "list role bindings by scope")
}

func (s SqlRoleBindingStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	now int64,
) ([]*model.RoleBinding, error) {
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{"role_bindings.user_id": userID, "role_bindings.delete_at": int64(0)}).
		Where(sq.LtOrEq{"role_bindings.start_at": now}).
		Where(sq.Or{sq.Eq{"role_bindings.end_at": int64(0)}, sq.Gt{"role_bindings.end_at": now}}).
		OrderBy("role_bindings.scope_type", "role_bindings.scope_id", "role_bindings.id"),
		"list active role bindings by user")
}

func (s SqlRoleBindingStore) selectBindings(
	ctx context.Context,
	query sq.SelectBuilder,
	operation string,
) ([]*model.RoleBinding, error) {
	rows := []roleBindingRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	bindings := make([]*model.RoleBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, row.model())
	}
	return bindings, nil
}

func (s SqlRoleBindingStore) End(
	ctx context.Context,
	id string,
	endAt int64,
) (*model.RoleBinding, error) {
	if endAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "end_at", endAt)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin role binding end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ended, err := endRoleBinding(ctx, tx, id, endAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit role binding end: %w", err)
	}
	return ended, nil
}

func (s SqlRoleBindingStore) EndWithAudit(
	ctx context.Context,
	input *store.RoleBindingEnd,
) (*model.RoleBinding, error) {
	if input == nil || !model.IsValidId(input.ID) || input.EndAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "end", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited role binding end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ended, err := endRoleBinding(ctx, tx, input.ID, input.EndAt)
	if err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(ended.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete role binding end audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited role binding end: %w", err)
	}
	return ended, nil
}

func endRoleBinding(ctx context.Context, tx *sqlxTxWrapper, id string, endAt int64) (*model.RoleBinding, error) {
	var current struct {
		roleBindingRow
		RoleName string `db:"role_name"`
	}
	if err := tx.Get(ctx, &current, `
		SELECT rb.id, rb.create_at, rb.update_at, rb.delete_at, rb.user_id,
		       rb.role_id, rb.scope_type, rb.scope_id, rb.start_at, rb.end_at,
		       r.name AS role_name
		  FROM role_bindings rb
		  JOIN roles r ON r.id = rb.role_id AND r.delete_at = 0
		 WHERE rb.id = $1 AND rb.delete_at = 0
		   AND rb.start_at < $2
		   AND (rb.end_at = 0 OR rb.end_at > $2)
		 FOR UPDATE OF rb`, id, endAt); err != nil {
		return nil, translateError("role_binding", id, err)
	}
	if current.RoleName == model.SystemAdministratorRoleName {
		if _, err := tx.Exec(
			ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"proctor:system-administrator-bindings",
		); err != nil {
			return nil, fmt.Errorf("lock administrator bindings: %w", err)
		}
		var remaining bool
		if err := tx.Get(ctx, &remaining, `
			SELECT EXISTS (
				SELECT 1
				  FROM role_bindings rb
				  JOIN roles r ON r.id = rb.role_id
				  JOIN users u ON u.id = rb.user_id
				 WHERE r.name = $1 AND r.built_in = true AND r.delete_at = 0
				   AND u.delete_at = 0 AND u.disabled_at = 0
				   AND rb.id <> $2 AND rb.scope_type = 'institution'
				   AND rb.scope_id = $3 AND rb.delete_at = 0
				   AND rb.start_at <= $4
				   AND (rb.end_at = 0 OR rb.end_at > $4)
			)`,
			model.SystemAdministratorRoleName,
			id,
			current.ScopeID,
			endAt,
		); err != nil {
			return nil, fmt.Errorf("check remaining administrator binding: %w", err)
		}
		if !remaining {
			return nil, store.NewErrConflict(
				"role_binding",
				"role_bindings_last_system_admin",
				nil,
			)
		}
	}

	var row roleBindingRow
	if err := tx.Get(ctx, &row, `
		UPDATE role_bindings
		   SET update_at = $1, end_at = $1
		 WHERE id = $2
		RETURNING id, create_at, update_at, delete_at, user_id, role_id,
		          scope_type, scope_id, start_at, end_at`, endAt, id); err != nil {
		return nil, translateError("role_binding", id, err)
	}
	return row.model(), nil
}

func newRoleBindingRow(binding *model.RoleBinding) roleBindingRow {
	return roleBindingRow{
		ID: binding.Id, CreateAt: binding.CreateAt, UpdateAt: binding.UpdateAt,
		DeleteAt: binding.DeleteAt, UserID: binding.UserId, RoleID: binding.RoleId,
		ScopeType: binding.ScopeType, ScopeID: binding.ScopeId,
		StartAt: binding.StartAt, EndAt: binding.EndAt,
	}
}

func (row roleBindingRow) model() *model.RoleBinding {
	return &model.RoleBinding{
		Id: row.ID, CreateAt: row.CreateAt, UpdateAt: row.UpdateAt,
		DeleteAt: row.DeleteAt, UserId: row.UserID, RoleId: row.RoleID,
		ScopeType: row.ScopeType, ScopeId: row.ScopeID,
		StartAt: row.StartAt, EndAt: row.EndAt,
	}
}

var _ store.RoleBindingStore = (*SqlRoleBindingStore)(nil)
