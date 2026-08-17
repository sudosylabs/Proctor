// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const installationBootstrapLock = "proctor:installation-bootstrap"

type SQLInstallationStore struct {
	*SQLStore
}

type installationStateRow struct {
	InitializedAt       time.Time `db:"initialized_at"`
	InstitutionID       string    `db:"institution_id"`
	AdministratorUserID string    `db:"administrator_user_id"`
}

func newSQLInstallationStore(sqlStore *SQLStore) store.InstallationStore {
	return &SQLInstallationStore{SQLStore: sqlStore}
}

func (s SQLInstallationStore) Get(ctx context.Context) (*model.InstallationState, error) {
	var row installationStateRow
	if err := s.GetMaster().Get(ctx, &row, `
		SELECT initialized_at, institution_id, administrator_user_id
		  FROM installation_states
		 WHERE singleton = 1`); err != nil {
		return nil, translateError("installation", "singleton", err)
	}
	return row.model()
}

func (s SQLInstallationStore) Bootstrap(
	ctx context.Context,
	input *store.InstallationBootstrap,
) (*model.InstallationBootstrapResult, error) {
	prepared, err := prepareInstallationBootstrap(input)
	if err != nil {
		return nil, err
	}

	return runSQLTransaction(ctx, s.GetMaster().Begin, "installation bootstrap", func(ctx context.Context, tx *sqlxTxWrapper) (*model.InstallationBootstrapResult, error) {
		if _, err := tx.Exec(ctx,
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
			return nil, store.NewErrConflict("installation", "installation_already_initialized_or_not_pristine", nil)
		}

		if err := insertInstallationInstitution(ctx, tx, prepared.Institution); err != nil {
			return nil, err
		}
		if err := insertUser(ctx, tx, prepared.Administrator); err != nil {
			return nil, err
		}
		if err := insertUserSettingsDocument(ctx, tx, prepared.AdministratorSettings); err != nil {
			return nil, err
		}
		if err := insertPasswordCredential(ctx, tx, prepared.credential); err != nil {
			return nil, err
		}
		if _, err := insertQueuedJob(ctx, tx, prepared.DefaultProfilePictureJob, false); err != nil {
			return nil, fmt.Errorf("enqueue bootstrap default profile picture generation: %w", translateError("job", prepared.DefaultProfilePictureJob.ID.String(), err))
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
		INSERT INTO installation_states (
			singleton, initialized_at, institution_id, administrator_user_id
		) VALUES (1, $1, $2, $3)`,
			prepared.State.InitializedAt,
			prepared.State.InstitutionID.String(),
			prepared.State.AdministratorUserID.String(),
		); err != nil {
			return nil, fmt.Errorf("save installation state: %w", translateError("installation", "singleton", err))
		}
		return prepared.InstallationBootstrapResult, nil
	})
}

func (s SQLInstallationStore) ReconcileSystemAdministratorRole(
	ctx context.Context,
	input *store.SystemAdministratorRoleReconciliation,
) (*store.SystemAdministratorRoleReconciliationResult, error) {
	if err := validateSystemAdministratorRoleReconciliation(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "reconcile system-administrator role", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SystemAdministratorRoleReconciliationResult, error) {
		var state installationStateRow
		if err := tx.Get(ctx, &state, `
			SELECT initialized_at, institution_id, administrator_user_id
			  FROM installation_states
			 WHERE singleton = 1
			 FOR UPDATE`); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				pristine, pristineErr := installationIsPristine(ctx, tx)
				if pristineErr != nil {
					return nil, pristineErr
				}
				if pristine {
					return &store.SystemAdministratorRoleReconciliationResult{}, nil
				}
				return nil, store.NewErrConflict("installation", "initialized_state_missing", nil)
			}
			return nil, fmt.Errorf("get installation for role reconciliation: %w", err)
		}
		installation, err := state.model()
		if err != nil {
			return nil, err
		}

		var row roleRow
		if err := tx.Get(ctx, &row, `
			SELECT id, created_at, updated_at, archived_at, name, display_name,
			       description, permissions, built_in
			  FROM roles
			 WHERE name = $1
			 FOR UPDATE`, model.SystemAdministratorRoleName); err != nil {
			return nil, fmt.Errorf("get protected role for reconciliation: %w", translateError("role", "name="+model.SystemAdministratorRoleName, err))
		}
		role, err := row.model()
		if err != nil {
			return nil, err
		}
		if !role.BuiltIn || role.IsArchived() {
			return nil, store.NewErrConflict("role", "system_administrator_role_invalid", nil)
		}

		permissions, added := mergeRequiredPermissions(role.Permissions, input.RequiredPermissions)
		if len(added) == 0 {
			return &store.SystemAdministratorRoleReconciliationResult{Role: role}, nil
		}
		candidate := role.Clone()
		candidate.Permissions = permissions
		at := model.TimeFromMillis(input.ReconciledAt)
		if !at.After(candidate.UpdatedAt) {
			at = candidate.UpdatedAt.Add(time.Microsecond)
		}
		candidate.PrepareUpdate(at)
		if err := candidate.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("role", "reconciliation", nil).Wrap(err)
		}
		updated := newRoleRow(candidate)
		if _, err := tx.Exec(ctx, `
			UPDATE roles
			   SET updated_at = $1, permissions = $2
			 WHERE id = $3 AND built_in = TRUE AND archived_at IS NULL`,
			updated.UpdatedAt, updated.Permissions, updated.ID,
		); err != nil {
			return nil, fmt.Errorf("update protected role reconciliation: %w", translateError("role", candidate.ID.String(), err))
		}

		event := input.AuditEvent.Clone()
		event.Resource = model.Resource{Type: model.ResourceInstitution, ID: installation.InstitutionID.String()}
		event.ScopeType = model.RoleScopeInstitution
		event.ScopeID = installation.InstitutionID.String()
		event.Result, err = model.EncodeAuditData(map[string]any{
			"role_id": candidate.ID.String(), "added_permissions": added,
		})
		if err != nil {
			return nil, err
		}
		event.PrepareCreate(model.NewAuditEventID(), at)
		if err := event.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("audit_event", "reconciliation", nil).Wrap(err)
		}
		if err := insertInstallationAudit(ctx, tx, event); err != nil {
			return nil, fmt.Errorf("save protected role reconciliation audit: %w", err)
		}
		return &store.SystemAdministratorRoleReconciliationResult{
			Role: candidate, Changed: true, AddedPermissions: added,
		}, nil
	})
}

func validateSystemAdministratorRoleReconciliation(input *store.SystemAdministratorRoleReconciliation) error {
	if input == nil || input.ReconciledAt <= 0 || input.AuditEvent == nil ||
		!input.AuditEvent.ID.IsZero() || input.AuditEvent.Action != "role.system_admin.reconcile" ||
		input.AuditEvent.Status != model.AuditStatusSuccess || input.AuditEvent.NodeID == "" ||
		!input.AuditEvent.ActorID.IsZero() || !input.AuditEvent.SessionID.IsZero() ||
		len(input.RequiredPermissions) == 0 {
		return store.NewErrInvalidInput("role", "system_administrator_reconciliation", nil)
	}
	seen := make(map[string]struct{}, len(input.RequiredPermissions))
	for _, permission := range input.RequiredPermissions {
		if !model.IsGrantableAction(permission) {
			return store.NewErrInvalidInput("role", "required_permission", permission)
		}
		if _, exists := seen[permission]; exists {
			return store.NewErrInvalidInput("role", "duplicate_required_permission", permission)
		}
		seen[permission] = struct{}{}
	}
	return nil
}

func mergeRequiredPermissions(existing, required []string) ([]string, []string) {
	seen := make(map[string]struct{}, len(existing)+len(required))
	merged := make([]string, 0, len(existing)+len(required))
	for _, permission := range existing {
		if _, duplicate := seen[permission]; duplicate {
			continue
		}
		seen[permission] = struct{}{}
		merged = append(merged, permission)
	}
	added := make([]string, 0, len(required))
	for _, permission := range required {
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		merged = append(merged, permission)
		added = append(added, permission)
	}
	sort.Strings(merged)
	sort.Strings(added)
	return merged, added
}

type preparedInstallationBootstrap struct {
	*model.InstallationBootstrapResult
	credential               *model.PasswordCredential
	auditEvent               *model.AuditEvent
	AdministratorSettings    *model.UserSettingsDocument
	DefaultProfilePictureJob *model.Job
}

func prepareInstallationBootstrap(
	input *store.InstallationBootstrap,
) (*preparedInstallationBootstrap, error) {
	if input == nil || input.Institution == nil || input.Administrator == nil ||
		input.Role == nil || input.RoleBinding == nil || input.AuditEvent == nil ||
		input.AdministratorSettings == nil || input.DefaultProfilePictureJob == nil || input.PasswordHash == "" {
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
	administrator.EmailVerified = false
	administrator.LastLoginAt = model.OptionalTime{}
	administrator.LastActivityAt = model.OptionalTime{}
	administrator.DisabledAt = model.OptionalTime{}
	if err := administrator.Validate(); err != nil {
		return nil, err
	}
	if err := validateUserDefaultProfilePictureJob(&administrator, input.DefaultProfilePictureJob); err != nil {
		return nil, err
	}
	administratorSettings := input.AdministratorSettings.Clone()
	if err := validateInitialUserSettingsDocument(&administrator, administratorSettings); err != nil {
		return nil, err
	}
	at := administrator.CreatedAt
	credential := &model.PasswordCredential{
		UserID: administrator.ID, PasswordHash: input.PasswordHash,
	}
	credential.PrepareCreate(model.NewPasswordCredentialID(), at)
	if err := credential.Validate(); err != nil {
		return nil, err
	}
	role := input.Role.Clone()
	role.ID = ""
	role.CreatedAt = time.Time{}
	role.UpdatedAt = time.Time{}
	role.ArchivedAt = model.OptionalTime{}
	role.PrepareCreate(model.NewRoleID(), at)
	if err := role.Validate(); err != nil {
		return nil, err
	}
	if role.Name != model.SystemAdministratorRoleName || !role.BuiltIn {
		return nil, store.NewErrInvalidInput("installation", "administrator_role", role.Name)
	}
	binding := *input.RoleBinding
	binding.ID = ""
	binding.CreatedAt = time.Time{}
	binding.UpdatedAt = time.Time{}
	binding.ArchivedAt = model.OptionalTime{}
	binding.EndsAt = model.OptionalTime{}
	binding.UserID = administrator.ID
	binding.RoleID = role.ID
	binding.ScopeType = model.RoleScopeInstitution
	binding.ScopeID = institution.ID.String()
	binding.PrepareCreate(model.NewRoleBindingID(), at)
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	event := input.AuditEvent.Clone()
	event.ID = ""
	event.CreatedAt = time.Time{}
	event.UpdatedAt = time.Time{}
	event.ActorID = administrator.ID
	event.Resource = model.Resource{
		Type: model.ResourceInstitution,
		ID:   institution.ID.String(),
	}
	event.ScopeType = model.RoleScopeInstitution
	event.ScopeID = institution.ID.String()
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
	event.PrepareCreate(model.NewAuditEventID(), at)
	if err := event.Validate(); err != nil {
		return nil, err
	}
	state := &model.InstallationState{
		InitializedAt:       event.CreatedAt,
		InstitutionID:       institution.ID,
		AdministratorUserID: administrator.ID,
	}
	return &preparedInstallationBootstrap{
		InstallationBootstrapResult: &model.InstallationBootstrapResult{
			State: state, Institution: institution, Administrator: &administrator,
			Role: role, RoleBinding: &binding,
		},
		credential:               credential,
		auditEvent:               event,
		AdministratorSettings:    administratorSettings,
		DefaultProfilePictureJob: input.DefaultProfilePictureJob,
	}, nil
}

func installationIsPristine(ctx context.Context, executor sqlxExecutor) (bool, error) {
	var present bool
	if err := executor.Get(ctx, &present, `
		SELECT EXISTS (SELECT 1 FROM installation_states)
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
			id, created_at, updated_at, archived_at, name, display_name,
			description
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :name, :display_name,
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
			id, created_at, updated_at, archived_at, name, display_name,
			description, permissions, built_in
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :name, :display_name,
			:description, :permissions, :built_in
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap role: %w",
			translateError("role", role.ID.String(), err),
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
			id, created_at, updated_at, archived_at, user_id, role_id,
			scope_type, scope_id, start_at, end_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :role_id,
			:scope_type, :scope_id, :start_at, :end_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save bootstrap role binding: %w",
			translateError("role_binding", binding.ID.String(), err),
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
			id, created_at, updated_at, actor_id, session_id, action,
			resource_type, resource_id, scope_type, scope_id, status,
			request_id, node_id, client_type, authentication_method,
			ip_address, user_agent, error_code, parameters, prior_state, result
		) VALUES (
			:id, :created_at, :updated_at, :actor_id, :session_id, :action,
			:resource_type, :resource_id, :scope_type, :scope_id, :status,
			:request_id, :node_id, :client_type, :authentication_method,
			:ip_address, :user_agent, :error_code, :parameters, :prior_state, :result
		)`, &row); err != nil {
		return fmt.Errorf(
			"save installation audit event: %w",
			translateError("audit_event", event.ID.String(), err),
		)
	}
	return nil
}

func (row installationStateRow) model() (*model.InstallationState, error) {
	institutionID, err := parsePersistedID("installation", "institution_id", row.InstitutionID, model.ParseInstitutionID)
	if err != nil {
		return nil, err
	}
	administratorUserID, err := parsePersistedID("installation", "administrator_user_id", row.AdministratorUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	state := &model.InstallationState{
		InitializedAt:       row.InitializedAt.UTC(),
		InstitutionID:       institutionID,
		AdministratorUserID: administratorUserID,
	}
	if err := validatePersistedModel("installation", state); err != nil {
		return nil, err
	}
	return state, nil
}

var _ store.InstallationStore = (*SQLInstallationStore)(nil)
