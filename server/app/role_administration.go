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
	a.realtime.InvalidateAuthorization(ctx, saved.UserId)
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
	a.realtime.InvalidateAuthorization(ctx, ended.UserId)
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
