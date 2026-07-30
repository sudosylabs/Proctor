// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/role.go. Proctor retains
// application-owned role lifecycle, protected built-in roles, explicit
// permission validation, and mutation auditing while using institution-scoped
// authorization and separate time-bounded role bindings.

package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (a *App) ListRoles(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
) ([]*model.Role, *model.AppError) {
	if _, appErr := a.authorizeRoleAdministration(ctx, principal, metadata); appErr != nil {
		return nil, appErr
	}
	roles, err := a.Store().Role().List(ctx)
	if err != nil {
		return nil, roleAdministrationError("ListRoles", "role", err)
	}
	return roles, nil
}

func (a *App) GetRole(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	roleID string,
) (*model.Role, *model.AppError) {
	if _, appErr := a.authorizeRoleAdministration(ctx, principal, metadata); appErr != nil {
		return nil, appErr
	}
	role, err := a.Store().Role().Get(ctx, roleID)
	if err != nil {
		return nil, roleAdministrationError("GetRole", "role", err)
	}
	return role, nil
}

func (a *App) CreateRole(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	role *model.Role,
) (*model.Role, *model.AppError) {
	resource, appErr := a.authorizeRoleAdministration(ctx, principal, metadata)
	if appErr != nil {
		return nil, appErr
	}
	if role == nil {
		return nil, invalidAdministrationRequest("CreateRole", "role")
	}
	candidate := role.Clone()
	candidate.Id = ""
	candidate.CreateAt = 0
	candidate.UpdateAt = 0
	candidate.DeleteAt = 0
	candidate.BuiltIn = false
	if appErr := validateKnownPermissions("CreateRole", candidate.Permissions); appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, model.ActionRoleManage, resource, metadata,
		map[string]any{"operation": "create", "role": candidate.Auditable()}, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := a.Store().Role().Save(ctx, candidate)
	if err != nil {
		return nil, a.failRoleMutation(ctx, attempt.Id, "CreateRole", "role", err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", saved.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return saved, nil
}

func (a *App) PatchRole(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	roleID string,
	patch *model.RolePatch,
) (*model.Role, *model.AppError) {
	resource, appErr := a.authorizeRoleAdministration(ctx, principal, metadata)
	if appErr != nil {
		return nil, appErr
	}
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchRole", "patch")
	}
	current, err := a.Store().Role().Get(ctx, roleID)
	if err != nil {
		return nil, roleAdministrationError("PatchRole.get", "role", err)
	}
	if current.BuiltIn {
		return nil, protectedBuiltInRoleError("PatchRole")
	}
	if patch.IsEmpty() {
		return current, nil
	}
	candidate := current.Clone()
	candidate.Patch(patch)
	if appErr := validatePatchedPermissions(
		"PatchRole", current.Permissions, patch.Permissions,
	); appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, model.ActionRoleManage, resource, metadata,
		map[string]any{"operation": "patch", "role_id": roleID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	updated, err := a.Store().Role().Update(ctx, candidate)
	if err != nil {
		return nil, a.failRoleMutation(ctx, attempt.Id, "PatchRole", "role", err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", updated.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return updated, nil
}

func (a *App) DeleteRole(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	roleID string,
) *model.AppError {
	resource, appErr := a.authorizeRoleAdministration(ctx, principal, metadata)
	if appErr != nil {
		return appErr
	}
	current, err := a.Store().Role().Get(ctx, roleID)
	if err != nil {
		return roleAdministrationError("DeleteRole.get", "role", err)
	}
	if current.BuiltIn {
		return protectedBuiltInRoleError("DeleteRole")
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, model.ActionRoleManage, resource, metadata,
		map[string]any{"operation": "delete", "role_id": roleID},
		current.Auditable(),
	)
	if appErr != nil {
		return appErr
	}
	deleted, err := a.Store().Role().Delete(ctx, roleID, time.Now().UnixMilli())
	if err != nil {
		return a.failRoleMutation(ctx, attempt.Id, "DeleteRole", "role", err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", deleted.Auditable(),
	); appErr != nil {
		return appErr
	}
	return nil
}

func (a *App) ListRoleBindingsForUser(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
) ([]*model.RoleBinding, *model.AppError) {
	if _, appErr := a.authorizeRoleAdministration(ctx, principal, metadata); appErr != nil {
		return nil, appErr
	}
	if !model.IsValidId(userID) {
		return nil, invalidAdministrationRequest("ListRoleBindingsForUser", "user_id")
	}
	bindings, err := a.Store().RoleBinding().ListByUser(ctx, userID)
	if err != nil {
		return nil, roleAdministrationError("ListRoleBindingsForUser", "role_binding", err)
	}
	return bindings, nil
}

func (a *App) ListRoleBindingsForScope(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	scopeType model.RoleScopeType,
	scopeID string,
) ([]*model.RoleBinding, *model.AppError) {
	if _, appErr := a.authorizeRoleAdministration(ctx, principal, metadata); appErr != nil {
		return nil, appErr
	}
	if !scopeType.IsValid() || !model.IsValidId(scopeID) {
		return nil, invalidAdministrationRequest("ListRoleBindingsForScope", "scope")
	}
	bindings, err := a.Store().RoleBinding().ListByScope(ctx, scopeType, scopeID)
	if err != nil {
		return nil, roleAdministrationError("ListRoleBindingsForScope", "role_binding", err)
	}
	return bindings, nil
}

func (a *App) CreateRoleBinding(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	binding *model.RoleBinding,
) (*model.RoleBinding, *model.AppError) {
	resource, appErr := a.authorizeRoleAdministration(ctx, principal, metadata)
	if appErr != nil {
		return nil, appErr
	}
	if binding == nil {
		return nil, invalidAdministrationRequest("CreateRoleBinding", "role_binding")
	}
	candidate := *binding
	candidate.Id = ""
	candidate.CreateAt = 0
	candidate.UpdateAt = 0
	candidate.DeleteAt = 0
	if candidate.StartAt == 0 {
		candidate.StartAt = model.GetMillis()
	}
	if !model.IsValidId(candidate.UserId) ||
		!model.IsValidId(candidate.RoleId) ||
		!candidate.ScopeType.IsValid() ||
		!model.IsValidId(candidate.ScopeId) ||
		candidate.StartAt < 0 ||
		(candidate.EndAt != 0 && candidate.EndAt <= candidate.StartAt) {
		return nil, invalidAdministrationRequest("CreateRoleBinding", "role_binding")
	}
	role, err := a.Store().Role().Get(ctx, candidate.RoleId)
	if err != nil {
		return nil, roleAdministrationError("CreateRoleBinding.role", "role", err)
	}
	if role.Name == model.SystemAdministratorRoleName &&
		candidate.ScopeType != model.RoleScopeInstitution {
		return nil, model.NewAppError(
			"CreateRoleBinding",
			"role_binding.system_admin_requires_institution_scope",
			nil,
			"",
			http.StatusBadRequest,
		)
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, model.ActionRoleManage, resource, metadata,
		map[string]any{"operation": "create_binding", "binding": candidate.Auditable()},
		nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := a.Store().RoleBinding().Save(ctx, &candidate)
	if err != nil {
		return nil, a.failRoleMutation(
			ctx, attempt.Id, "CreateRoleBinding", "role_binding", err,
		)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", saved.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return saved, nil
}

func (a *App) EndRoleBinding(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	bindingID string,
) (*model.RoleBinding, *model.AppError) {
	resource, appErr := a.authorizeRoleAdministration(ctx, principal, metadata)
	if appErr != nil {
		return nil, appErr
	}
	current, err := a.Store().RoleBinding().Get(ctx, bindingID)
	if err != nil {
		return nil, roleAdministrationError("EndRoleBinding.get", "role_binding", err)
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, model.ActionRoleManage, resource, metadata,
		map[string]any{"operation": "end_binding", "role_binding_id": bindingID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	ended, err := a.Store().RoleBinding().End(ctx, bindingID, time.Now().UnixMilli())
	if err != nil {
		return nil, a.failRoleMutation(
			ctx, attempt.Id, "EndRoleBinding", "role_binding", err,
		)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx, attempt.Id, model.AuditStatusSuccess, "", ended.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return ended, nil
}

func (a *App) authorizeRoleAdministration(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
) (model.Resource, *model.AppError) {
	return a.authorizePrincipalToSystem(
		ctx,
		principal,
		model.ActionRoleManage,
		metadata,
	)
}

func validateKnownPermissions(where string, permissions []string) *model.AppError {
	for _, permission := range permissions {
		if !model.IsKnownAction(permission) {
			return model.NewAppError(
				where,
				"role.permission.unknown",
				nil,
				"",
				http.StatusBadRequest,
			).WithSafeFields(map[string]string{"permission": permission})
		}
	}
	return nil
}

func validatePatchedPermissions(
	where string,
	current []string,
	permissions *[]string,
) *model.AppError {
	if permissions == nil {
		return nil
	}
	existingUnknown := make(map[string]struct{})
	for _, permission := range current {
		if !model.IsKnownAction(permission) {
			existingUnknown[permission] = struct{}{}
		}
	}
	for _, permission := range *permissions {
		if model.IsKnownAction(permission) {
			continue
		}
		if _, preserved := existingUnknown[permission]; preserved {
			continue
		}
		return model.NewAppError(
			where,
			"role.permission.unknown",
			nil,
			"",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"permission": permission})
	}
	return nil
}

func (a *App) failRoleMutation(
	ctx context.Context,
	auditID string,
	where string,
	resource string,
	err error,
) *model.AppError {
	mapped := roleAdministrationError(where, resource, err)
	if _, auditErr := a.audit.CompleteCriticalAction(
		ctx, auditID, model.AuditStatusFail, mapped.ErrorCode(), nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

func roleAdministrationError(where, resource string, err error) *model.AppError {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if store.IsNotFound(err) {
		return model.NewAppError(
			where, "resource.not_found", nil, "", http.StatusNotFound,
		).WithSafeFields(map[string]string{"resource": resource}).Wrap(err)
	}
	if store.IsConflict(err) {
		var conflict *store.ErrConflict
		_ = errors.As(err, &conflict)
		code := resource + ".conflict"
		if conflict != nil && conflict.Constraint == "role_bindings_last_system_admin" {
			code = "role_binding.last_system_admin"
		}
		return model.NewAppError(
			where, code, nil, "", http.StatusConflict,
		).Wrap(err)
	}
	var invalid *store.ErrInvalidInput
	var reference *store.ErrReference
	if errors.As(err, &invalid) || errors.As(err, &reference) {
		return model.NewAppError(
			where, resource+".invalid", nil, "", http.StatusBadRequest,
		).Wrap(err)
	}
	return model.NewAppError(
		where, "role_administration.unavailable", nil, "",
		http.StatusInternalServerError,
	).Wrap(err)
}

func invalidAdministrationRequest(where, field string) *model.AppError {
	return model.NewAppError(
		where, "request.invalid", nil, "", http.StatusBadRequest,
	).WithSafeFields(map[string]string{"field": field})
}

func protectedBuiltInRoleError(where string) *model.AppError {
	return model.NewAppError(
		where, "role.built_in.protected", nil, "", http.StatusConflict,
	)
}
