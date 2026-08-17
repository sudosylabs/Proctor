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
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLRoleBindingStore struct {
	*SQLStore
	bindingsQuery sq.SelectBuilder
}

// roleBindingRow is the legacy integer-millisecond column layout. Domain
// RoleBinding uses time.Time / OptionalTime; conversion is at this boundary.
type roleBindingRow struct {
	ID         string              `db:"id"`
	CreatedAt  time.Time           `db:"created_at"`
	UpdatedAt  time.Time           `db:"updated_at"`
	ArchivedAt sql.NullTime        `db:"archived_at"`
	UserID     string              `db:"user_id"`
	RoleID     string              `db:"role_id"`
	ScopeType  model.RoleScopeType `db:"scope_type"`
	ScopeID    string              `db:"scope_id"`
	StartAt    time.Time           `db:"start_at"`
	EndAt      sql.NullTime        `db:"end_at"`
}

func roleBindingSliceColumns() []string {
	return []string{
		"role_bindings.id", "role_bindings.created_at", "role_bindings.updated_at",
		"role_bindings.archived_at", "role_bindings.user_id", "role_bindings.role_id",
		"role_bindings.scope_type", "role_bindings.scope_id",
		"role_bindings.start_at", "role_bindings.end_at",
	}
}

func newSQLRoleBindingStore(sqlStore *SQLStore) store.RoleBindingStore {
	s := &SQLRoleBindingStore{SQLStore: sqlStore}
	s.bindingsQuery = s.getQueryBuilder().
		Select(roleBindingSliceColumns()...).
		From("role_bindings")
	return s
}

func (s SQLRoleBindingStore) Save(
	ctx context.Context,
	binding *model.RoleBinding,
) (*model.RoleBinding, error) {
	if binding == nil {
		return nil, store.NewErrInvalidInput("role_binding", "value", nil)
	}
	if !binding.ID.IsZero() {
		return nil, store.NewErrInvalidInput("role_binding", "id", binding.ID.String())
	}
	candidate := *binding
	candidate.PrepareCreate(model.NewRoleBindingID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "role binding save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.RoleBinding, error) {
		if err := insertRoleBinding(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		return &candidate, nil
	})
}

func (s SQLRoleBindingStore) SaveWithAudit(
	ctx context.Context,
	input *store.RoleBindingCreation,
) (*model.RoleBinding, error) {
	if input == nil || input.Binding == nil || !input.Binding.ID.IsZero() ||
		input.ExpectedRoleUpdatedAt.IsZero() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "creation", nil)
	}
	candidate := *input.Binding
	at := model.TimeFromMillis(input.AuditAt)
	if at.IsZero() {
		at = model.NowUTC()
	}
	// Explicit StartsAt from the application is preserved; zero uses audit time.
	candidate.PrepareCreate(model.NewRoleBindingID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("role_binding", "value", nil).Wrap(err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited role binding save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.RoleBinding, error) {
		if err := lockExpectedRoleForBinding(ctx, tx, candidate.RoleID, input.ExpectedRoleUpdatedAt, input.ExpectedRolePermissions); err != nil {
			return nil, err
		}
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
		return &candidate, nil
	})
}

// lockExpectedRoleForBinding closes the read-check-write race between the
// application delegation decision and the binding insert. Permission-changing
// role updates take an exclusive lock on the same row; the exact snapshot is
// rechecked while this shared lock is held through commit.
func lockExpectedRoleForBinding(ctx context.Context, tx *sqlxTxWrapper, roleID model.RoleID, expectedUpdatedAt time.Time, expectedPermissions []string) error {
	var row struct {
		UpdatedAt   time.Time      `db:"updated_at"`
		Permissions pq.StringArray `db:"permissions"`
	}
	if err := tx.Get(ctx, &row, `SELECT updated_at,permissions FROM roles WHERE id=$1 AND archived_at IS NULL FOR SHARE`, roleID.String()); err != nil {
		return translateError("role", roleID.String(), err)
	}
	if !row.UpdatedAt.Equal(model.TimeUTC(expectedUpdatedAt)) || !slices.Equal([]string(row.Permissions), expectedPermissions) {
		return store.NewErrConflict("role_binding", "role_bindings_role_snapshot", nil)
	}
	return nil
}

func insertRoleBinding(ctx context.Context, tx *sqlxTxWrapper, candidate *model.RoleBinding) error {
	if candidate.ScopeType == model.RoleScopeClass {
		if err := lockClassLifecycle(ctx, tx); err != nil {
			return err
		}
	}
	lockKey := candidate.UserID.String() + ":" + candidate.RoleID.String() + ":" +
		string(candidate.ScopeType) + ":" + candidate.ScopeID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock role binding grant: %w", err)
	}
	if err := validateRoleBindingReferences(ctx, tx, candidate); err != nil {
		return err
	}
	if err := validateSystemAdministratorScope(ctx, tx, candidate); err != nil {
		return err
	}
	startAt := candidate.StartsAt
	endAt := NullTimeFromOptional(candidate.EndsAt)
	var overlap bool
	if err := tx.Get(ctx, &overlap, `
		SELECT EXISTS (
			SELECT 1 FROM role_bindings
			 WHERE user_id = $1 AND role_id = $2
			   AND scope_type = $3 AND scope_id = $4
			   AND archived_at IS NULL
			   AND ($6::timestamptz IS NULL OR start_at < $6)
			   AND (end_at IS NULL OR end_at > $5)
		)`, candidate.UserID.String(), candidate.RoleID.String(), candidate.ScopeType,
		candidate.ScopeID, startAt, endAt); err != nil {
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
			id, created_at, updated_at, archived_at, user_id, role_id,
			scope_type, scope_id, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :role_id,
			:scope_type, :scope_id, :start_at, :end_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save role binding: %w",
			translateError("role_binding", candidate.ID.String(), err),
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
		{"users", binding.UserID.String(), "role_bindings_user_id_fkey"},
		{"roles", binding.RoleID.String(), "role_bindings_role_id_fkey"},
	}
	switch binding.ScopeType {
	case model.RoleScopeInstitution:
		checks = append(checks, struct{ table, id, constraint string }{
			"institutions", binding.ScopeID, "role_bindings_institution_scope_fkey",
		})
	case model.RoleScopeAcademicUnit:
		checks = append(checks, struct{ table, id, constraint string }{
			"academic_units", binding.ScopeID, "role_bindings_academic_unit_scope_fkey",
		})
	case model.RoleScopeClass:
		checks = append(checks, struct{ table, id, constraint string }{
			"classes", binding.ScopeID, "role_bindings_class_scope_fkey",
		})
	}
	for _, check := range checks {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1 AND archived_at IS NULL)`, check.table)
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
		`SELECT name FROM roles WHERE id = $1 AND archived_at IS NULL`,
		binding.RoleID.String(),
	); err != nil {
		return translateError("role", binding.RoleID.String(), err)
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

func (s SQLRoleBindingStore) Get(ctx context.Context, id string) (*model.RoleBinding, error) {
	var row roleBindingRow
	query := s.bindingsQuery.Where(sq.Eq{
		"role_bindings.id": id, "role_bindings.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("role_binding", id, err)
	}
	return row.model()
}

func (s SQLRoleBindingStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.RoleBinding, error) {
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{"role_bindings.user_id": userID, "role_bindings.archived_at": nil}).
		OrderBy("role_bindings.start_at", "role_bindings.id"), "list role bindings by user")
}

func (s SQLRoleBindingStore) ListByScope(
	ctx context.Context,
	scopeType model.RoleScopeType,
	scopeID string,
) ([]*model.RoleBinding, error) {
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{
			"role_bindings.scope_type": scopeType, "role_bindings.scope_id": scopeID,
			"role_bindings.archived_at": nil,
		}).
		OrderBy("role_bindings.start_at", "role_bindings.id"), "list role bindings by scope")
}

func (s SQLRoleBindingStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	now int64,
) ([]*model.RoleBinding, error) {
	at := model.TimeFromMillis(now)
	return s.selectBindings(ctx, s.bindingsQuery.
		Where(sq.Eq{"role_bindings.user_id": userID, "role_bindings.archived_at": nil}).
		Where(sq.LtOrEq{"role_bindings.start_at": at}).
		Where(sq.Or{sq.Eq{"role_bindings.end_at": nil}, sq.Gt{"role_bindings.end_at": at}}).
		OrderBy("role_bindings.scope_type", "role_bindings.scope_id", "role_bindings.id"),
		"list active role bindings by user")
}

func (s SQLRoleBindingStore) selectBindings(
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
		binding, err := row.model()
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func (s SQLRoleBindingStore) End(
	ctx context.Context,
	id string,
	endAt int64,
) (*model.RoleBinding, error) {
	if endAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "end_at", endAt)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "role binding end", func(ctx context.Context, tx *sqlxTxWrapper) (*model.RoleBinding, error) {
		return endRoleBinding(ctx, tx, id, endAt)
	})
}

func (s SQLRoleBindingStore) EndWithAudit(
	ctx context.Context,
	input *store.RoleBindingEnd,
) (*model.RoleBinding, error) {
	if input == nil || !model.IsValidId(input.ID) || input.EndAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("role_binding", "end", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited role binding end", func(ctx context.Context, tx *sqlxTxWrapper) (*model.RoleBinding, error) {
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
		return ended, nil
	})
}

func endRoleBinding(ctx context.Context, tx *sqlxTxWrapper, id string, endAt int64) (*model.RoleBinding, error) {
	at := model.TimeFromMillis(endAt)
	var current struct {
		roleBindingRow
		RoleName string `db:"role_name"`
	}
	if err := tx.Get(ctx, &current, `
		SELECT rb.id, rb.created_at, rb.updated_at, rb.archived_at, rb.user_id,
		       rb.role_id, rb.scope_type, rb.scope_id, rb.start_at, rb.end_at,
		       r.name AS role_name
		  FROM role_bindings rb
		  JOIN roles r ON r.id = rb.role_id AND r.archived_at IS NULL
		 WHERE rb.id = $1 AND rb.archived_at IS NULL
		   AND rb.start_at < $2
		   AND (rb.end_at IS NULL OR rb.end_at > $2)
		 FOR UPDATE OF rb`, id, at); err != nil {
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
				 WHERE r.name = $1 AND r.built_in = true AND r.archived_at IS NULL
				   AND u.archived_at IS NULL AND u.disabled_at IS NULL
				   AND rb.id <> $2 AND rb.scope_type = 'institution'
				   AND rb.scope_id = $3 AND rb.archived_at IS NULL
				   AND rb.start_at <= $4
				   AND (rb.end_at IS NULL OR rb.end_at > $4)
			)`,
			model.SystemAdministratorRoleName,
			id,
			current.ScopeID,
			at,
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
		   SET updated_at = GREATEST(updated_at, $1), end_at = $1
		 WHERE id = $2
		RETURNING id, created_at, updated_at, archived_at, user_id, role_id,
		          scope_type, scope_id, start_at, end_at`, at, id); err != nil {
		return nil, translateError("role_binding", id, err)
	}
	return row.model()
}

func newRoleBindingRow(binding *model.RoleBinding) roleBindingRow {
	return roleBindingRow{
		ID: binding.ID.String(), CreatedAt: UTCTime(binding.CreatedAt),
		UpdatedAt: UTCTime(binding.UpdatedAt), ArchivedAt: NullTimeFromOptional(binding.ArchivedAt),
		UserID: binding.UserID.String(), RoleID: binding.RoleID.String(),
		ScopeType: binding.ScopeType, ScopeID: binding.ScopeID,
		StartAt: UTCTime(binding.StartsAt), EndAt: NullTimeFromOptional(binding.EndsAt),
	}
}

func (row roleBindingRow) model() (*model.RoleBinding, error) {
	id, err := parsePersistedID("role_binding", "id", row.ID, model.ParseRoleBindingID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("role_binding", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	roleID, err := parsePersistedID("role_binding", "role_id", row.RoleID, model.ParseRoleID)
	if err != nil {
		return nil, err
	}
	if err := validatePersistedScopeID("role_binding", row.ScopeType, row.ScopeID); err != nil {
		return nil, err
	}
	value := &model.RoleBinding{
		ID: id, CreatedAt: row.CreatedAt.UTC(),
		UpdatedAt: row.UpdatedAt.UTC(), ArchivedAt: OptionalTimeFromNullTime(row.ArchivedAt),
		UserID: userID, RoleID: roleID,
		ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		StartsAt: row.StartAt.UTC(), EndsAt: OptionalTimeFromNullTime(row.EndAt),
	}
	if err := validatePersistedModel("role_binding", value); err != nil {
		return nil, err
	}
	return value, nil
}

func validatePersistedScopeID(entity string, scopeType model.RoleScopeType, raw string) error {
	var err error
	switch scopeType {
	case model.RoleScopeInstitution:
		_, err = model.ParseInstitutionID(raw)
	case model.RoleScopeAcademicUnit:
		_, err = model.ParseAcademicUnitID(raw)
	case model.RoleScopeClass:
		_, err = model.ParseClassID(raw)
	default:
		return nil
	}
	if err != nil {
		return invalidPersistedState(entity, "scope_id", err)
	}
	return nil
}

var _ store.RoleBindingStore = (*SQLRoleBindingStore)(nil)
