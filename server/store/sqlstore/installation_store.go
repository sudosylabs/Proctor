// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const installationBootstrapLock = "proctor:installation-bootstrap"

type SqlInstallationStore struct {
	*SqlStore
}

type installationStateRow struct {
	InitializedAt       int64  `db:"initialized_at"`
	InstitutionID       string `db:"institution_id"`
	AdministratorUserID string `db:"administrator_user_id"`
}

func newSqlInstallationStore(sqlStore *SqlStore) store.InstallationStore {
	return &SqlInstallationStore{SqlStore: sqlStore}
}

func (s SqlInstallationStore) Get(ctx context.Context) (*model.InstallationState, error) {
	var row installationStateRow
	if err := s.GetMaster().Get(ctx, &row, `
		SELECT initialized_at, institution_id, administrator_user_id
		  FROM installation_state
		 WHERE singleton = 1`); err != nil {
		return nil, translateError("installation", "singleton", err)
	}
	return row.model(), nil
}

func (s SqlInstallationStore) Bootstrap(
	ctx context.Context,
	input *store.InstallationBootstrap,
) (*model.InstallationBootstrapResult, error) {
	prepared, err := prepareInstallationBootstrap(input)
	if err != nil {
		return nil, err
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin installation bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		installationBootstrapLock,
	); err != nil {
		return nil, fmt.Errorf("lock installation bootstrap: %w", err)
	}
	pristine, err := installationIsPristine(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !pristine {
		return nil, store.NewErrConflict(
			"installation",
			"installation_already_initialized_or_not_pristine",
			nil,
		)
	}

	if err := insertInstallationInstitution(ctx, tx, prepared.Institution); err != nil {
		return nil, err
	}
	if err := insertUser(ctx, tx, prepared.Administrator); err != nil {
		return nil, err
	}
	if err := insertPasswordCredential(ctx, tx, prepared.credential); err != nil {
		return nil, err
	}
	if err := insertInstallationRole(ctx, tx, prepared.Role); err != nil {
		return nil, err
	}
	if err := insertInstallationRoleBinding(ctx, tx, prepared.RoleBinding); err != nil {
		return nil, err
	}
	if err := insertInstallationAudit(ctx, tx, prepared.auditEvent); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO installation_state (
			singleton, initialized_at, institution_id, administrator_user_id
		) VALUES (1, $1, $2, $3)`,
		prepared.State.InitializedAt,
		prepared.State.InstitutionId,
		prepared.State.AdministratorUserId,
	); err != nil {
		return nil, fmt.Errorf(
			"save installation state: %w",
			translateError("installation", "singleton", err),
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit installation bootstrap: %w", err)
	}
	return prepared.InstallationBootstrapResult, nil
}

type preparedInstallationBootstrap struct {
	*model.InstallationBootstrapResult
	credential *model.PasswordCredential
	auditEvent *model.AuditEvent
}

func prepareInstallationBootstrap(
	input *store.InstallationBootstrap,
) (*preparedInstallationBootstrap, error) {
	if input == nil || input.Institution == nil || input.Administrator == nil ||
		input.Role == nil || input.RoleBinding == nil || input.AuditEvent == nil ||
		input.PasswordHash == "" {
		return nil, store.NewErrInvalidInput("installation", "bootstrap", nil)
	}
	institutionID, err := model.ParseInstitutionID(model.NewId())
	if err != nil {
		return nil, err
	}
	institution, err := model.NewInstitution(
		institutionID,
		input.Institution.Name,
		input.Institution.DisplayName,
		input.Institution.Description,
		model.NowUTC(),
	)
	if err != nil {
		return nil, store.NewErrInvalidInput("institution", "value", nil).Wrap(err)
	}
	administrator := *input.Administrator
	administrator.Id = ""
	administrator.CreateAt = 0
	administrator.UpdateAt = 0
	administrator.DeleteAt = 0
	administrator.EmailVerified = false
	administrator.LastLoginAt = 0
	administrator.LastActivityAt = 0
	administrator.DisabledAt = 0
	administrator.PreSave()
	if appErr := administrator.IsValid(); appErr != nil {
		return nil, appErr
	}
	credential := &model.PasswordCredential{
		UserId: administrator.Id, PasswordHash: input.PasswordHash,
	}
	credential.PreSave()
	if appErr := credential.IsValid(); appErr != nil {
		return nil, appErr
	}
	role := input.Role.Clone()
	role.Id = ""
	role.CreateAt = 0
	role.UpdateAt = 0
	role.DeleteAt = 0
	role.PreSave()
	if appErr := role.IsValid(); appErr != nil {
		return nil, appErr
	}
	if role.Name != model.SystemAdministratorRoleName || !role.BuiltIn {
		return nil, store.NewErrInvalidInput("installation", "administrator_role", role.Name)
	}
	binding := *input.RoleBinding
	binding.Id = ""
	binding.CreateAt = 0
	binding.UpdateAt = 0
	binding.DeleteAt = 0
	binding.EndAt = 0
	binding.UserId = administrator.Id
	binding.RoleId = role.Id
	binding.ScopeType = model.RoleScopeInstitution
	binding.ScopeId = institution.ID.String()
	binding.PreSave()
	if appErr := binding.IsValid(); appErr != nil {
		return nil, appErr
	}
	event := input.AuditEvent.Clone()
	event.Id = ""
	event.CreateAt = 0
	event.UpdateAt = 0
	event.ActorId = administrator.Id
	event.Resource = model.Resource{
		Type: model.ResourceInstitution,
		Id:   institution.ID.String(),
	}
	event.ScopeType = model.RoleScopeInstitution
	event.ScopeId = institution.ID.String()
	event.Status = model.AuditStatusSuccess
	parameters, appErr := model.EncodeAuditData(map[string]any{
		"institution":   institution.Auditable(),
		"administrator": administrator.Auditable(),
		"role":          role.Auditable(),
		"role_binding":  binding.Auditable(),
	})
	if appErr != nil {
		return nil, appErr
	}
	event.Parameters = parameters
	event.PreSave()
	if appErr := event.IsValid(); appErr != nil {
		return nil, appErr
	}
	state := &model.InstallationState{
		InitializedAt:       event.CreateAt,
		InstitutionId:       institution.ID.String(),
		AdministratorUserId: administrator.Id,
	}
	return &preparedInstallationBootstrap{
		InstallationBootstrapResult: &model.InstallationBootstrapResult{
			State: state, Institution: institution, Administrator: &administrator,
			Role: role, RoleBinding: &binding,
		},
		credential: credential,
		auditEvent: event,
	}, nil
}

func installationIsPristine(ctx context.Context, executor sqlxExecutor) (bool, error) {
	var present bool
	if err := executor.Get(ctx, &present, `
		SELECT EXISTS (SELECT 1 FROM installation_state)
		    OR EXISTS (SELECT 1 FROM institutions)
		    OR EXISTS (SELECT 1 FROM users)
		    OR EXISTS (SELECT 1 FROM roles)
		    OR EXISTS (SELECT 1 FROM role_bindings)`); err != nil {
		return false, fmt.Errorf("check installation state: %w", err)
	}
	return !present, nil
}

func insertInstallationInstitution(
	ctx context.Context,
	executor sqlxExecutor,
	institution *model.Institution,
) error {
	row := newInstitutionRow(institution)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO institutions (
			id, create_at, update_at, delete_at, name, display_name,
			description
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :name, :display_name,
			:description
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap institution: %w",
			translateError("institution", institution.ID.String(), err),
		)
	}
	return nil
}

func insertInstallationRole(
	ctx context.Context,
	executor sqlxExecutor,
	role *model.Role,
) error {
	row := newRoleRow(role)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO roles (
			id, create_at, update_at, delete_at, name, display_name,
			description, permissions, built_in
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :name, :display_name,
			:description, :permissions, :built_in
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap role: %w",
			translateError("role", role.Id, err),
		)
	}
	return nil
}

func insertInstallationRoleBinding(
	ctx context.Context,
	executor sqlxExecutor,
	binding *model.RoleBinding,
) error {
	if err := validateRoleBindingReferences(ctx, executor, binding); err != nil {
		return err
	}
	row := newRoleBindingRow(binding)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO role_bindings (
			id, create_at, update_at, delete_at, user_id, role_id,
			scope_type, scope_id, start_at, end_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id, :role_id,
			:scope_type, :scope_id, :start_at, :end_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap role binding: %w",
			translateError("role_binding", binding.Id, err),
		)
	}
	return nil
}

func insertInstallationAudit(
	ctx context.Context,
	executor sqlxExecutor,
	event *model.AuditEvent,
) error {
	row := newAuditRow(event)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO audit_events (
			id, create_at, update_at, actor_id, session_id, action,
			resource_type, resource_id, scope_type, scope_id, status,
			request_id, node_id, client_type, authentication_method,
			ip_address, user_agent, error_code, parameters, prior_state, result
		) VALUES (
			:id, :create_at, :update_at, :actor_id, :session_id, :action,
			:resource_type, :resource_id, :scope_type, :scope_id, :status,
			:request_id, :node_id, :client_type, :authentication_method,
			:ip_address, :user_agent, :error_code, :parameters, :prior_state, :result
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap audit event: %w",
			translateError("audit_event", event.Id, err),
		)
	}
	return nil
}

func (row installationStateRow) model() *model.InstallationState {
	return &model.InstallationState{
		InitializedAt:       row.InitializedAt,
		InstitutionId:       row.InstitutionID,
		AdministratorUserId: row.AdministratorUserID,
	}
}

var _ store.InstallationStore = (*SqlInstallationStore)(nil)
