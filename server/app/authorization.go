// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
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

	"github.com/sudosylabs/proctor/server/app/apperror"
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

// resourceAuthorizer is the one application authorization port. A use case
// authorizes an Action on a concrete Resource immediately before the work it
// guards; named helpers such as AuthorizeInstitution or AuthorizeWithScope
// resolve or report around this same decision rather than redefining it.
type resourceAuthorizer interface {
	Authorize(context.Context, Invocation, model.Action, model.Resource) error
}

// institutionAuthorizer authorizes an Action on the installation's singleton
// Institution and returns that Resource for the caller's audit.
type institutionAuthorizer interface {
	AuthorizeInstitution(context.Context, Invocation, model.Action) (model.Resource, error)
}

type authorizationDecisionAudit interface {
	RecordAuthorizationDecision(context.Context, model.Principal, model.Action, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error
	RecordUserSearchDecision(context.Context, model.Principal, model.Resource, model.RoleScopeType, string, model.RequestMetadata, bool) error
}

type resolvedAuthorizationResource struct {
	institutionID                  model.InstitutionID
	academicUnitID                 map[model.AcademicUnitID]struct{}
	targetAcademicUnitID           model.AcademicUnitID
	classID                        model.ClassID
	academicPeriodInstitutionOwned bool
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
	if !known || resource.Validate() != nil || !definition.AcceptsResource(resource.Type) {
		return false, unresolved, apperror.New("authorization.request.invalid")
	}
	if definition.RelationshipOnly {
		return false, unresolved, apperror.New("authorization.request.invalid")
	}
	if principal.CredentialType == model.CredentialPersonalAccessToken && definition.PersonalAccessTokenForbidden {
		return false, unresolved, nil
	}
	if resource.Type == model.ResourceAcademicPeriod {
		allowed, coarse, appErr := s.academicPeriodCoarseAuthority(ctx, principal, action)
		if appErr != nil {
			return false, unresolved, appErr
		}
		if !allowed {
			return false, coarse, nil
		}
	}
	resolved, appErr := s.resolver.resolve(ctx, resource)
	if appErr != nil {
		return false, unresolved, appErr
	}
	allowed, appErr := s.evaluateResolved(ctx, principal, action, resource, definition, resolved)
	return allowed, resolved, appErr
}

func (s *accessControlService) evaluateResolved(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	definition model.ActionDefinition,
	resolved resolvedAuthorizationResource,
) (bool, error) {
	if principal.CredentialType == model.CredentialPersonalAccessToken &&
		!personalAccessTokenAllows(principal, action, resource, resolved) {
		return false, nil
	}
	if (action == model.ActionUserView || action == model.ActionUserProfilePictureManage) &&
		principal.UserID.String() == resource.ID {
		return true, nil
	}
	bindings, err := s.bindings.ListActiveByUser(
		ctx, principal.UserID, model.TimeUTC(s.now()),
	)
	if err != nil {
		return false, authorizationUnavailableError(err)
	}
	roleIDs := make([]model.RoleID, 0, len(bindings))
	seen := make(map[model.RoleID]struct{}, len(bindings))
	for _, binding := range bindings {
		if _, exists := seen[binding.RoleID]; !exists {
			seen[binding.RoleID] = struct{}{}
			roleIDs = append(roleIDs, binding.RoleID)
		}
	}
	roles, err := s.roles.GetByIDs(ctx, roleIDs)
	if err != nil {
		return false, authorizationUnavailableError(err)
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
			return true, nil
		}
	}
	return false, nil
}

func (s *accessControlService) AuthorizeAcademicPeriodOwner(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	period *model.AcademicPeriod,
) error {
	if period == nil || period.Validate() != nil {
		return apperror.New("authorization.request.invalid")
	}
	resource := model.Resource{Type: model.ResourceAcademicPeriod, ID: period.ID.String()}
	definition, known := model.DefinitionForAction(action)
	if !known || definition.ResourceType != model.ResourceAcademicPeriod {
		return apperror.New("authorization.request.invalid")
	}
	if appErr := s.authorizeAcademicPeriodPreflight(ctx, invocation, action, resource); appErr != nil {
		return appErr
	}
	principal := invocation.Principal()
	resolved, appErr := s.resolver.resolve(ctx, period.Owner.Resource())
	if appErr != nil {
		return appErr
	}
	allowed, appErr := s.evaluateResolved(ctx, principal, action, resource, definition, resolved)
	if appErr != nil {
		return appErr
	}
	scopeType, scopeID := authorizationAuditScope(resource, resolved)
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx, principal, action, resource, scopeType, scopeID,
		invocation.RequestMetadata(), allowed,
	); appErr != nil {
		return appErr
	}
	if !allowed {
		return authorizationDeniedError()
	}
	return nil
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
	case model.ResourceAcademicUnit, model.ResourceProgramme, model.ResourceProgrammeLevel, model.ResourceAcademicPeriod, model.ResourceClass, model.ResourceExam:
		if resource.Type == model.ResourceAcademicPeriod && resolved.academicPeriodInstitutionOwned {
			return action == model.ActionAcademicPeriodView
		}
		_, applies := resolved.academicUnitID[principal.AcademicUnitID]
		return applies
	default:
		return false
	}
}

// Authorize is the one application authorization shape. It is fail-closed:
// the requested action is allowed only after the decision itself has been
// durably recorded against the invocation's principal and request metadata.
func (s *accessControlService) Authorize(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	return s.authorizeCurrentState(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
}

// AuthorizeWithScope is Authorize that also reports the role-binding scope
// which granted the decision, for use cases that audit at that scope.
func (s *accessControlService) AuthorizeWithScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) (model.RoleScopeType, string, error) {
	return s.authorizeCurrentStateWithScope(ctx, invocation.Principal(), action, resource, invocation.RequestMetadata())
}

// InstitutionResource returns the installation's singleton Institution as an
// authorization Resource.
func (s *accessControlService) InstitutionResource(ctx context.Context) (model.Resource, error) {
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, institutionStoreErrors.Translate(err)
	}
	return model.Resource{Type: model.ResourceInstitution, ID: institution.ResourceID()}, nil
}

// AuthorizeInstitution authorizes action against the singleton Institution
// and returns that Resource for the caller's audit.
func (s *accessControlService) AuthorizeInstitution(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) (model.Resource, error) {
	resource, err := s.InstitutionResource(ctx)
	if err != nil {
		return model.Resource{}, err
	}
	if err := s.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

// AuthorizeUserRead authorizes reading another User through the caller's
// current visibility scope.
func (s *accessControlService) AuthorizeUserRead(ctx context.Context, invocation Invocation, userID string) error {
	_, err := s.authorizeUserRead(ctx, invocation, userID)
	return err
}

// RecordDenial durably records a denied decision at the Academic Unit scope
// without evaluating policy, for use cases that refuse by a domain rule such
// as self-review. The caller returns its own concealing error afterwards.
func (s *accessControlService) RecordDenial(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
	unitID model.AcademicUnitID,
) error {
	return s.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource,
		model.RoleScopeAcademicUnit, unitID.String(), invocation.RequestMetadata(), false)
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
	_, _, appErr := s.authorizeCurrentStateWithScope(ctx, principal, action, resource, metadata)
	return appErr
}

// authorizeUserAccountState is the narrow exception to active User resource
// resolution needed to re-enable a disabled account. It still requires a
// current user.manage grant and records the decision against the exact User.
func (s *accessControlService) authorizeUserAccountState(
	ctx context.Context,
	invocation Invocation,
	userID string,
) error {
	principal := invocation.Principal()
	resource := model.Resource{Type: model.ResourceUser, ID: userID}
	definition, known := model.DefinitionForAction(model.ActionUserManage)
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	if !known || resource.Validate() != nil || !definition.AcceptsResource(resource.Type) {
		return apperror.New("authorization.request.invalid")
	}
	user, err := s.resolver.users.Get(ctx, model.UserID(userID))
	if err != nil {
		return authorizationResourceError("user", err)
	}
	if user.IsArchived() {
		return apperror.New("resource.not_found").WithField("resource", "user")
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return authorizationResourceError("institution", err)
	}
	if institution.IsArchived() {
		return apperror.New("resource.not_found").WithField("resource", "institution")
	}
	resolved := resolvedAuthorizationResource{
		institutionID:  institution.ID,
		academicUnitID: make(map[model.AcademicUnitID]struct{}),
	}
	allowed, appErr := s.evaluateResolved(ctx, principal, model.ActionUserManage, resource, definition, resolved)
	if appErr != nil {
		return appErr
	}
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx, principal, model.ActionUserManage, resource,
		model.RoleScopeInstitution, institution.ID.String(), invocation.RequestMetadata(), allowed,
	); appErr != nil {
		return appErr
	}
	if !allowed {
		return authorizationDeniedError()
	}
	return nil
}

// authorizeCurrentStateWithScope returns the same resolved scope written to
// the durable decision so a following mutation attempt can preserve the exact
// action/resource/scope tuple without resolving the resource a second time.
func (s *accessControlService) authorizeCurrentStateWithScope(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
) (model.RoleScopeType, string, error) {
	allowed, resolved, appErr := s.evaluate(ctx, principal, action, resource)
	if appErr != nil {
		return "", "", appErr
	}
	scopeType, scopeID := authorizationAuditScope(resource, resolved)
	if appErr = s.audit.RecordAuthorizationDecision(
		ctx, principal, action, resource, scopeType, scopeID, metadata, allowed,
	); appErr != nil {
		return "", "", appErr
	}
	if !allowed {
		return "", "", authorizationDeniedError()
	}
	return scopeType, scopeID, nil
}

// authorizeUserRead authorizes reading another User and reports whether the
// grant came from institution-wide User visibility. A scoped grant through an
// Academic Unit or Class reports false with a nil error.
func (s *accessControlService) authorizeUserRead(
	ctx context.Context,
	invocation Invocation,
	userID string,
) (bool, error) {
	principal := invocation.Principal()
	userResource := model.Resource{Type: model.ResourceUser, ID: userID}
	visibility, appErr := s.userVisibilityScope(ctx, principal)
	if appErr != nil {
		return false, appErr
	}
	if visibility.InstitutionWide {
		appErr = s.authorizeCurrentState(ctx, principal, model.ActionUserView, userResource, invocation.RequestMetadata())
		return appErr == nil, appErr
	}
	allowed := false
	match := store.UserVisibilityMatch{}
	if visibility.ClassMemberInstitutionWide || len(visibility.AcademicUnitRootIDs) > 0 ||
		len(visibility.ClassMemberAcademicUnitRootIDs) > 0 || len(visibility.ClassIDs) > 0 {
		var err error
		match, err = s.resolver.users.MatchVisibility(ctx, model.UserID(userID), visibility)
		if err != nil {
			return false, authorizationUnavailableError(err)
		}
		allowed = (match.ScopeType == model.RoleScopeAcademicUnit || match.ScopeType == model.RoleScopeClass) &&
			model.IsValidOpaqueID(match.ScopeID)
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return false, authorizationResourceError("institution", err)
	}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if allowed {
		scopeType, scopeID = match.ScopeType, match.ScopeID
	}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, principal, model.ActionUserView, userResource,
		scopeType, scopeID, invocation.RequestMetadata(), allowed,
	); err != nil {
		return false, err
	}
	if allowed {
		return false, nil
	}
	return false, authorizationDeniedError()
}

func authorizationDeniedError() error {
	return apperror.New("authorization.denied")
}

func authorizationAuditScope(
	resource model.Resource,
	resolved resolvedAuthorizationResource,
) (model.RoleScopeType, string) {
	switch resource.Type {
	case model.ResourceInstitution:
		return model.RoleScopeInstitution, resolved.institutionID.String()
	case model.ResourceAcademicUnit:
		return model.RoleScopeAcademicUnit, resource.ID
	case model.ResourceAcademicPeriod:
		if resolved.targetAcademicUnitID == "" {
			return model.RoleScopeInstitution, resolved.institutionID.String()
		}
		return model.RoleScopeAcademicUnit, resolved.targetAcademicUnitID.String()
	case model.ResourceProgramme, model.ResourceProgrammeLevel, model.ResourceExam, model.ResourceExamSitting, model.ResourceSubmission:
		return model.RoleScopeAcademicUnit, resolved.targetAcademicUnitID.String()
	case model.ResourceClass:
		return model.RoleScopeClass, resource.ID
	case model.ResourceUser:
		return model.RoleScopeInstitution, resolved.institutionID.String()
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
		return definition.InheritInstitutionScope && model.InstitutionID(binding.ScopeID) == resolved.institutionID
	case model.RoleScopeAcademicUnit:
		if !definition.InheritAcademicUnitScopes {
			return false
		}
		if resource.Type == model.ResourceAcademicPeriod && resolved.academicPeriodInstitutionOwned {
			return definition.Action == model.ActionAcademicPeriodView
		}
		_, applies := resolved.academicUnitID[model.AcademicUnitID(binding.ScopeID)]
		return applies
	case model.RoleScopeClass:
		return resource.Type == model.ResourceClass &&
			model.ClassID(binding.ScopeID) == resolved.classID
	default:
		return false
	}
}

func authorizationResourceError(resource string, err error) error {
	return apperror.StoreTable{
		NotFound:    "resource.not_found",
		Unavailable: "authorization.unavailable",
		Fields:      map[string]string{"resource": resource},
	}.Translate(err)
}

func authorizationUnavailableError(err error) error {
	return apperror.New("authorization.unavailable").Wrap(err)
}
