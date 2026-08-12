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
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type accessControlService struct {
	roles    store.RoleStore
	bindings store.RoleBindingStore
	resolver *accessScopeResolver
	audit    authorizationDecisionAudit
	now      func() time.Time
}

type authorizationDecisionAudit interface {
	RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error
	RecordUserSearchDecision(context.Context, model.Principal, model.Resource, model.RequestMetadata, bool) error
}

type resolvedAuthorizationResource struct {
	institutionID  string
	academicUnitID map[string]struct{}
	classID        string
}

func newAccessControlService(
	roles store.RoleStore,
	bindings store.RoleBindingStore,
	resolver *accessScopeResolver,
	audit authorizationDecisionAudit,
) (*accessControlService, error) {
	if roles == nil || bindings == nil || resolver == nil || audit == nil {
		return nil, errors.New("authorization dependencies are required")
	}
	return &accessControlService{roles: roles, bindings: bindings, resolver: resolver, audit: audit, now: time.Now}, nil
}

// Can resolves current durable bindings and roles. It performs no auditing and
// is intended for composition inside an application use case. Use Authorize at
// a security boundary so both allowed and denied decisions are durable.
func (s *accessControlService) Can(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, error) {
	allowed, _, appErr := s.evaluate(ctx, principal, action, resource)
	return allowed, appErr
}

func (s *accessControlService) evaluate(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
) (bool, resolvedAuthorizationResource, error) {
	var unresolved resolvedAuthorizationResource
	if principal.Validate() != nil {
		return false, unresolved, invalidTokenAppError()
	}
	definition, known := model.DefinitionForAction(action)
	if !known || resource.Validate() != nil || definition.ResourceType != resource.Type {
		return false, unresolved, NewError("authorization.request.invalid")
	}
	resolved, appErr := s.resolver.resolve(ctx, resource)
	if appErr != nil {
		return false, unresolved, appErr
	}
	if principal.CredentialType == model.CredentialPersonalAccessToken &&
		!personalAccessTokenAllows(principal, action, resource, resolved) {
		return false, resolved, nil
	}
	if (action == model.ActionUserView || action == model.ActionUserProfilePictureManage) &&
		principal.UserID.String() == resource.ID {
		return true, resolved, nil
	}
	bindings, err := s.bindings.ListActiveByUser(
		ctx, principal.UserID.String(), s.now().UnixMilli(),
	)
	if err != nil {
		return false, unresolved, authorizationUnavailableError("accessControlService.Can.bindings", err)
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
	roles, err := s.roles.GetByIds(ctx, roleIDs)
	if err != nil {
		return false, unresolved, authorizationUnavailableError("accessControlService.Can.roles", err)
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
	if principal.AcademicUnitID.IsZero() {
		return true
	}
	switch resource.Type {
	case model.ResourceAcademicUnit, model.ResourceClass:
		_, applies := resolved.academicUnitID[principal.AcademicUnitID.String()]
		return applies
	default:
		return false
	}
}

// Authorize is fail-closed: the requested action is allowed only after the
// decision itself has been durably recorded.
func (s *accessControlService) Authorize(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) error {
	return s.authorizeCurrentState(ctx, principal, action, resource, metadata)
}

// authorizeCurrentState performs and audits a fresh authorization decision for
// the owning application use case.
func (s *accessControlService) authorizeCurrentState(
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
		return authorizationDeniedError("accessControlService.Authorize")
	}
	return nil
}

// authorizeUserViewThroughClass evaluates the contextual class permission but
// records the use-case decision against the user being viewed. The class is an
// authorization input, not the application resource exposed by this decision.
func (s *accessControlService) authorizeUserViewThroughClass(
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
		return authorizationDeniedError("accessControlService.authorizeUserViewThroughClass")
	}
	return nil
}

func (s *accessControlService) authorizeUserRead(
	ctx context.Context,
	invocation Invocation,
	userID string,
) error {
	principal := invocation.Principal()
	userResource := model.Resource{Type: model.ResourceUser, ID: userID}
	allowed, appErr := s.Can(ctx, principal, model.ActionUserView, userResource)
	if appErr != nil && !Is(appErr, "resource.not_found") {
		return appErr
	}
	if allowed {
		return s.authorizeCurrentState(ctx, principal, model.ActionUserView, userResource, invocation.RequestMetadata())
	}
	if appErr == nil {
		classes, err := s.resolver.userClasses(ctx, userID, s.now().UnixMilli())
		if err != nil {
			return err
		}
		for _, classResource := range classes {
			allowed, err = s.Can(ctx, principal, model.ActionClassMembersView, classResource)
			if err != nil {
				return err
			}
			if allowed {
				return s.authorizeUserViewThroughClass(ctx, principal, userResource, classResource, invocation.RequestMetadata())
			}
		}
		return s.authorizeCurrentState(ctx, principal, model.ActionUserView, userResource, invocation.RequestMetadata())
	}
	// Missing/inactive targets are indistinguishable from existing but
	// unauthorized targets. Record the denial against the attempted User.
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return authorizationResourceError("institution", err)
	}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, principal, model.ActionUserView, userResource,
		model.RoleScopeInstitution, institution.ID.String(),
		invocation.RequestMetadata(), false,
	); err != nil {
		return err
	}
	return authorizationDeniedError("accessControlService.authorizeUserRead")
}

func (s *accessControlService) preauthorize(
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
		return model.RoleScopeAcademicUnit, resource.ID
	case model.ResourceClass:
		return model.RoleScopeClass, resource.ID
	case model.ResourceUser:
		return model.RoleScopeInstitution, resolved.institutionID
	default:
		return "", ""
	}
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
	return authorizationUnavailableError("accessControlService.resolveResource", err)
}

func authorizationUnavailableError(where string, err error) error {
	_ = where
	return NewError("authorization.unavailable").Wrap(err)
}
