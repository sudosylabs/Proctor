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
) (bool, error) {
	allowed, _, appErr := s.evaluate(ctx, principal, action, resource)
	return allowed, appErr
}

func (s *AuthorizationService) evaluate(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, resolvedAuthorizationResource, error) {
	var unresolved resolvedAuthorizationResource
	if !principal.IsValid() {
		return false, unresolved, invalidTokenAppError()
	}
	definition, known := model.DefinitionForAction(action)
	if !known || !resource.IsValid() || definition.ResourceType != resource.Type {
		return false, unresolved, NewError("authorization.request.invalid")
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
	if principal.CredentialType == model.CredentialPersonalAccessToken &&
		!personalAccessTokenAllows(principal, action, resource, resolved) {
		return false, resolved, nil
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
		roleKey := binding.RoleID.String()
		if _, exists := seen[roleKey]; !exists {
			seen[roleKey] = struct{}{}
			roleIDs = append(roleIDs, roleKey)
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
		permissionByRole[role.ID.String()] = permissions
	}
	for _, binding := range bindings {
		if !roleBindingApplies(binding, definition, resource, resolved) {
			continue
		}
		if _, grants := permissionByRole[binding.RoleID.String()][string(action)]; grants {
			return true, resolved, nil
		}
	}
	return false, resolved, nil
}

// personalAccessTokenAllows is only a credential ceiling. It never grants an
// action; ordinary current-state role evaluation still has to grant it.
func personalAccessTokenAllows(
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	resolved resolvedAuthorizationResource,
) bool {
	scoped := false
	for _, scope := range principal.CredentialScopes {
		if scope == string(action) {
			scoped = true
			break
		}
	}
	if !scoped {
		return false
	}
	if principal.AcademicUnitId == "" {
		return true
	}
	switch resource.Type {
	case model.ResourceAcademicUnit, model.ResourceClass:
		_, applies := resolved.academicUnitID[principal.AcademicUnitId]
		return applies
	default:
		return false
	}
}

// Authorize is fail-closed: the requested action is allowed only after the
// decision itself has been durably recorded.
func (s *AuthorizationService) Authorize(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) error {
	return s.authorizeCurrentState(ctx, principal, action, resource, metadata)
}

// authorizeCurrentState performs and audits a fresh authorization decision.
// Migrated use cases call this path so they cannot consume a transport-issued
// preauthorization receipt.
func (s *AuthorizationService) authorizeCurrentState(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) error {
	allowed, appErr := s.preauthorize(ctx, principal, action, resource, metadata)
	if appErr != nil {
		return appErr
	}
	if !allowed {
		return authorizationDeniedError("AuthorizationService.Authorize")
	}
	return nil
}

// authorizeUserViewThroughClass evaluates the contextual class permission but
// records the use-case decision against the user being viewed. The class is an
// authorization input, not the application resource exposed by this decision.
func (s *AuthorizationService) authorizeUserViewThroughClass(
	ctx context.Context,
	principal model.Principal,
	userResource model.Resource,
	classResource model.Resource,
	metadata model.RequestMetadata,
) error {
	allowed, resolved, appErr := s.evaluate(
		ctx, principal, model.ActionClassMembersView, classResource,
	)
	if appErr != nil {
		return appErr
	}
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx,
		principal,
		model.ActionUserView,
		userResource,
		model.RoleScopeInstitution,
		resolved.institutionID,
		metadata,
		allowed,
	); appErr != nil {
		return appErr
	}
	if !allowed {
		return authorizationDeniedError("AuthorizationService.authorizeUserViewThroughClass")
	}
	return nil
}

func (s *AuthorizationService) preauthorize(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) (bool, error) {
	allowed, resolved, appErr := s.evaluate(ctx, principal, action, resource)
	if appErr != nil {
		return false, appErr
	}
	scopeType, scopeID := authorizationAuditScope(resource, resolved)
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx, principal, action, resource, scopeType, scopeID, metadata, allowed,
	); appErr != nil {
		return false, appErr
	}
	return allowed, nil
}

func authorizationDeniedError(where string) error {
	_ = where
	return NewError("authorization.denied")
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
) (resolvedAuthorizationResource, error) {
	resolved := resolvedAuthorizationResource{
		academicUnitID: make(map[string]struct{}),
	}
	switch resource.Type {
	case model.ResourceInstitution:
		institution, err := s.store.Institution().Get(ctx, resource.Id)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		resolved.institutionID = institution.ID.String()
	case model.ResourceAcademicUnit:
		units, err := s.store.AcademicUnit().ListAncestors(ctx, resource.Id)
		if err != nil {
			return resolved, authorizationResourceError("academic_unit", err)
		}
		resolved.institutionID = units[0].InstitutionID.String()
		for _, unit := range units {
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
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
		resolved.institutionID = units[0].InstitutionID.String()
		for _, unit := range units {
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
		}
	case model.ResourceUser:
		if _, err := s.store.User().Get(ctx, resource.Id); err != nil {
			return resolved, authorizationResourceError("user", err)
		}
		institution, err := s.store.Institution().GetSingleton(ctx)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		resolved.institutionID = institution.ID.String()
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
		return definition.InheritInstitutionScope && binding.ScopeID == resolved.institutionID
	case model.RoleScopeAcademicUnit:
		if !definition.InheritAcademicUnitScopes {
			return false
		}
		_, applies := resolved.academicUnitID[binding.ScopeID]
		return applies
	case model.RoleScopeClass:
		return resource.Type == model.ResourceClass &&
			binding.ScopeID == resolved.classID
	default:
		return false
	}
}

func authorizationResourceError(resource string, err error) error {
	if store.IsNotFound(err) {
		return NewError("resource.not_found").WithField("resource", resource).Wrap(err)
	}
	return authorizationUnavailableError("AuthorizationService.resolveResource", err)
}

func authorizationUnavailableError(where string, err error) error {
	_ = where
	return NewError("authorization.unavailable").Wrap(err)
}
