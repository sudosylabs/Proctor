// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/authorization.go and
// permissions.go. Proctor retains current-state role resolution, additive
// permissions, deleted-role exclusion, and deny-by-default behavior while
// resolving its own institution, academic-unit, and class scope hierarchy.

package app

import (
	"context"
	"net/http"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type AuthorizationService struct {
	store store.Store
	audit *AuditService
	now   func() time.Time
}

type resolvedAuthorizationResource struct {
	institutionID  string
	academicUnitID map[string]struct{}
	classID        string
}

func newAuthorizationService(
	persistence store.Store,
	audit *AuditService,
) *AuthorizationService {
	return &AuthorizationService{
		store: persistence, audit: audit, now: time.Now,
	}
}

// Can resolves current durable bindings and roles. It performs no auditing and
// is intended for composition inside an application use case. Use Authorize at
// a security boundary so both allowed and denied decisions are durable.
func (s *AuthorizationService) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, *model.AppError) {
	allowed, _, appErr := s.evaluate(ctx, principal, action, resource)
	return allowed, appErr
}

func (s *AuthorizationService) evaluate(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, resolvedAuthorizationResource, *model.AppError) {
	var unresolved resolvedAuthorizationResource
	if !principal.IsValid() {
		return false, unresolved, invalidTokenError("AuthorizationService.Can")
	}
	definition, known := model.DefinitionForAction(action)
	if !known || !resource.IsValid() || definition.ResourceType != resource.Type {
		return false, unresolved, model.NewAppError(
			"AuthorizationService.Can",
			"authorization.request.invalid",
			nil,
			"",
			http.StatusBadRequest,
		)
	}
	if s.store == nil || s.store.Role() == nil || s.store.RoleBinding() == nil {
		return false, unresolved, authorizationUnavailableError(
			"AuthorizationService.Can",
			store.NewErrNotFound("authorization_store", ""),
		)
	}

	resolved, appErr := s.resolveResource(ctx, resource)
	if appErr != nil {
		return false, unresolved, appErr
	}
	bindings, err := s.store.RoleBinding().ListActiveByUser(
		ctx, principal.UserId, s.now().UnixMilli(),
	)
	if err != nil {
		return false, unresolved, authorizationUnavailableError("AuthorizationService.Can.bindings", err)
	}
	roleIDs := make([]string, 0, len(bindings))
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if _, exists := seen[binding.RoleId]; !exists {
			seen[binding.RoleId] = struct{}{}
			roleIDs = append(roleIDs, binding.RoleId)
		}
	}
	roles, err := s.store.Role().GetByIds(ctx, roleIDs)
	if err != nil {
		return false, unresolved, authorizationUnavailableError("AuthorizationService.Can.roles", err)
	}
	permissionByRole := make(map[string]map[string]struct{}, len(roles))
	for _, role := range roles {
		permissions := make(map[string]struct{}, len(role.Permissions))
		for _, permission := range role.Permissions {
			permissions[permission] = struct{}{}
		}
		permissionByRole[role.Id] = permissions
	}
	for _, binding := range bindings {
		if !roleBindingApplies(binding, definition, resource, resolved) {
			continue
		}
		if _, grants := permissionByRole[binding.RoleId][string(action)]; grants {
			return true, resolved, nil
		}
	}
	return false, resolved, nil
}

// Authorize is fail-closed: the requested action is allowed only after the
// decision itself has been durably recorded.
func (s *AuthorizationService) Authorize(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) *model.AppError {
	allowed, resolved, appErr := s.evaluate(ctx, principal, action, resource)
	if appErr != nil {
		return appErr
	}
	scopeType, scopeID := authorizationAuditScope(resource, resolved)
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx, principal, action, resource, scopeType, scopeID, metadata, allowed,
	); appErr != nil {
		return appErr
	}
	if !allowed {
		return model.NewAppError(
			"AuthorizationService.Authorize",
			"authorization.denied",
			nil,
			"",
			http.StatusForbidden,
		)
	}
	return nil
}

func authorizationAuditScope(
	resource model.Resource,
	resolved resolvedAuthorizationResource,
) (model.RoleScopeType, string) {
	switch resource.Type {
	case model.ResourceInstitution:
		return model.RoleScopeInstitution, resolved.institutionID
	case model.ResourceAcademicUnit:
		return model.RoleScopeAcademicUnit, resource.Id
	case model.ResourceClass:
		return model.RoleScopeClass, resource.Id
	case model.ResourceUser:
		return model.RoleScopeInstitution, resolved.institutionID
	default:
		return "", ""
	}
}

func (s *AuthorizationService) resolveResource(
	ctx context.Context,
	resource model.Resource,
) (resolvedAuthorizationResource, *model.AppError) {
	resolved := resolvedAuthorizationResource{
		academicUnitID: make(map[string]struct{}),
	}
	switch resource.Type {
	case model.ResourceInstitution:
		institution, err := s.store.Institution().Get(ctx, resource.Id)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		resolved.institutionID = institution.Id
	case model.ResourceAcademicUnit:
		units, err := s.store.AcademicUnit().ListAncestors(ctx, resource.Id)
		if err != nil {
			return resolved, authorizationResourceError("academic_unit", err)
		}
		resolved.institutionID = units[0].InstitutionId
		for _, unit := range units {
			resolved.academicUnitID[unit.Id] = struct{}{}
		}
	case model.ResourceClass:
		academicUnitID, err := s.store.Class().GetAcademicUnitId(ctx, resource.Id)
		if err != nil {
			return resolved, authorizationResourceError("class", err)
		}
		units, err := s.store.AcademicUnit().ListAncestors(ctx, academicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("class_academic_unit", err)
		}
		resolved.classID = resource.Id
		resolved.institutionID = units[0].InstitutionId
		for _, unit := range units {
			resolved.academicUnitID[unit.Id] = struct{}{}
		}
	case model.ResourceUser:
		if _, err := s.store.User().Get(ctx, resource.Id); err != nil {
			return resolved, authorizationResourceError("user", err)
		}
		institution, err := s.store.Institution().GetSingleton(ctx)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		resolved.institutionID = institution.Id
	}
	return resolved, nil
}

func roleBindingApplies(
	binding *model.RoleBinding,
	definition model.ActionDefinition,
	resource model.Resource,
	resolved resolvedAuthorizationResource,
) bool {
	switch binding.ScopeType {
	case model.RoleScopeInstitution:
		return definition.InheritInstitutionScope && binding.ScopeId == resolved.institutionID
	case model.RoleScopeAcademicUnit:
		if !definition.InheritAcademicUnitScopes {
			return false
		}
		_, applies := resolved.academicUnitID[binding.ScopeId]
		return applies
	case model.RoleScopeClass:
		return resource.Type == model.ResourceClass &&
			binding.ScopeId == resolved.classID
	default:
		return false
	}
}

func authorizationResourceError(resource string, err error) *model.AppError {
	if store.IsNotFound(err) {
		return model.NewAppError(
			"AuthorizationService.resolveResource",
			"resource.not_found",
			nil,
			"",
			http.StatusNotFound,
		).WithSafeFields(map[string]string{"resource": resource})
	}
	return authorizationUnavailableError("AuthorizationService.resolveResource", err)
}

func authorizationUnavailableError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"authorization.unavailable",
		nil,
		"",
		http.StatusInternalServerError,
	).Wrap(err)
}
