// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// authorizedScopeConstraint is the bounded, persistence-ready result of an
// Access Control evaluation for a list or search query. AcademicUnitRootIDs
// mean the named subtrees; consumers must apply them in persistence rather
// than load rows and filter them in application memory.
type authorizedScopeConstraint struct {
	InstitutionID       model.InstitutionID
	InstitutionWide     bool
	AcademicUnitRootIDs []model.AcademicUnitID
	ClassIDs            []model.ClassID
}

// AuthorizePreflight establishes that the principal has some current
// authority for the action's resource family before a caller loads an opaque
// child identifier whose owning scope is not yet known. Callers retain the
// exact resource authorization after the bounded lookup.
func (s *accessControlService) AuthorizePreflight(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resourceType model.ResourceType,
) error {
	constraint, err := s.authorizedScopes(ctx, invocation.Principal(), action, resourceType)
	if err != nil {
		return err
	}
	if constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0 {
		return nil
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return authorizationResourceError("institution", err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, invocation.Principal(), action, resource, model.RoleScopeInstitution,
		institution.ID.String(), invocation.RequestMetadata(), false,
	); err != nil {
		return err
	}
	return authorizationDeniedError()
}

func (s *accessControlService) authorizeAcademicPeriodPreflight(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	resource model.Resource,
) error {
	if resource.Type != model.ResourceAcademicPeriod || resource.Validate() != nil {
		return apperror.New("authorization.request.invalid")
	}
	allowed, resolved, err := s.academicPeriodCoarseAuthority(ctx, invocation.Principal(), action)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, invocation.Principal(), action, resource,
		model.RoleScopeInstitution, resolved.institutionID.String(),
		invocation.RequestMetadata(), false,
	); err != nil {
		return err
	}
	return authorizationDeniedError()
}

func (s *accessControlService) academicPeriodCoarseAuthority(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
) (bool, resolvedAuthorizationResource, error) {
	resolved := resolvedAuthorizationResource{academicUnitID: make(map[model.AcademicUnitID]struct{})}
	constraint, err := s.authorizedScopes(
		ctx, principal, action, model.ResourceAcademicPeriod,
	)
	if err != nil {
		return false, resolved, err
	}
	if constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 {
		return true, resolved, nil
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return false, resolved, authorizationResourceError("institution", err)
	}
	resolved.institutionID = institution.ID
	return false, resolved, nil
}

func (s *accessControlService) AuthorizeAcademicPeriodList(
	ctx context.Context,
	invocation Invocation,
) (store.AcademicPeriodVisibilityScope, error) {
	principal := invocation.Principal()
	constraint, err := s.authorizedScopes(
		ctx, principal, model.ActionAcademicPeriodView, model.ResourceAcademicPeriod,
	)
	if err != nil {
		return store.AcademicPeriodVisibilityScope{}, err
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.AcademicPeriodVisibilityScope{}, authorizationResourceError("institution", err)
	}
	visibility := store.AcademicPeriodVisibilityScope{
		InstitutionID:       institution.ID,
		InstitutionWide:     constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]model.AcademicUnitID(nil), constraint.AcademicUnitRootIDs...),
	}
	allowed := visibility.InstitutionWide || len(visibility.AcademicUnitRootIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, principal, model.ActionAcademicPeriodView, resource,
		model.RoleScopeInstitution, institution.ID.String(),
		invocation.RequestMetadata(), allowed,
	); err != nil {
		return store.AcademicPeriodVisibilityScope{}, err
	}
	if !allowed {
		return store.AcademicPeriodVisibilityScope{}, authorizationDeniedError()
	}
	return visibility, nil
}

func (s *accessControlService) authorizeUserSearch(
	ctx context.Context,
	invocation Invocation,
) (store.UserVisibilityScope, error) {
	principal := invocation.Principal()
	visibility, err := s.userVisibilityScope(ctx, principal)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.UserVisibilityScope{}, authorizationResourceError("institution", err)
	}
	allowed := visibility.InstitutionWide || visibility.ClassMemberInstitutionWide ||
		len(visibility.ClassIDs) > 0 || len(visibility.AcademicUnitRootIDs) > 0 ||
		len(visibility.ClassMemberAcademicUnitRootIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(visibility.AcademicUnitRootIDs) > 0 {
		scopeType, scopeID = model.RoleScopeAcademicUnit, visibility.AcademicUnitRootIDs[0].String()
	} else if len(visibility.ClassMemberAcademicUnitRootIDs) > 0 {
		scopeType, scopeID = model.RoleScopeAcademicUnit, visibility.ClassMemberAcademicUnitRootIDs[0].String()
	} else if len(visibility.ClassIDs) > 0 {
		scopeType, scopeID = model.RoleScopeClass, visibility.ClassIDs[0].String()
	}
	if err := s.audit.RecordUserSearchDecision(
		ctx, principal, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed,
	); err != nil {
		return store.UserVisibilityScope{}, err
	}
	if !allowed {
		return store.UserVisibilityScope{}, authorizationDeniedError()
	}
	return visibility, nil
}

func (s *accessControlService) userVisibilityScope(
	ctx context.Context,
	principal model.Principal,
) (store.UserVisibilityScope, error) {
	decisionAt := model.TimeUTC(s.now())
	userScope, err := s.authorizedScopesAt(ctx, principal, model.ActionUserView, model.ResourceUser, decisionAt)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	classScope, err := s.authorizedScopesAt(ctx, principal, model.ActionClassMembersView, model.ResourceClass, decisionAt)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	visibility := store.UserVisibilityScope{
		InstitutionWide:                userScope.InstitutionWide,
		AcademicUnitRootIDs:            append([]model.AcademicUnitID(nil), userScope.AcademicUnitRootIDs...),
		ClassMemberInstitutionWide:     classScope.InstitutionWide,
		ClassMemberAcademicUnitRootIDs: append([]model.AcademicUnitID(nil), classScope.AcademicUnitRootIDs...),
		ClassIDs:                       append([]model.ClassID(nil), classScope.ClassIDs...),
		ActiveAt:                       decisionAt,
	}
	return visibility, nil
}

func (s *accessControlService) authorizeRoleBindingPreflight(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
) error {
	constraint, err := s.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return err
	}
	if constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0 {
		return nil
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return authorizationResourceError("institution", err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.audit.RecordAuthorizationDecision(
		ctx, invocation.Principal(), action, resource, model.RoleScopeInstitution,
		institution.ID.String(), invocation.RequestMetadata(), false,
	); err != nil {
		return err
	}
	return authorizationDeniedError()
}

func (s *accessControlService) authorizeRoleBindingScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	scopeType model.RoleScopeType,
	scopeID string,
) (model.Resource, error) {
	resource, err := roleScopeResource(scopeType, scopeID)
	if err != nil {
		return model.Resource{}, err
	}
	if err := s.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

func roleScopeResource(scopeType model.RoleScopeType, scopeID string) (model.Resource, error) {
	if !model.IsValidOpaqueID(scopeID) {
		return model.Resource{}, apperror.New("request.invalid").WithField("field", "role_binding")
	}
	var resourceType model.ResourceType
	switch scopeType {
	case model.RoleScopeInstitution:
		resourceType = model.ResourceInstitution
	case model.RoleScopeAcademicUnit:
		resourceType = model.ResourceAcademicUnit
	case model.RoleScopeClass:
		resourceType = model.ResourceClass
	default:
		return model.Resource{}, apperror.New("request.invalid").WithField("field", "role_binding")
	}
	return model.Resource{Type: resourceType, ID: scopeID}, nil
}

func (s *accessControlService) canDelegateActionsAtScope(
	ctx context.Context,
	principal model.Principal,
	actions []string,
	scopeType model.RoleScopeType,
	scopeID string,
) (bool, error) {
	target, err := roleScopeResource(scopeType, scopeID)
	if err != nil {
		return false, err
	}
	resolvedTarget, err := s.resolver.resolve(ctx, target)
	if err != nil {
		return false, err
	}
	for _, value := range actions {
		definition, ok := model.DefinitionForAction(model.Action(value))
		if !ok || definition.RelationshipOnly {
			return false, nil
		}
		constraint, err := s.authorizedScopes(ctx, principal, definition.Action, definition.ResourceType)
		if err != nil {
			return false, err
		}
		strictParent := protectedDelegationAction(definition.Action) && scopeType != model.RoleScopeInstitution
		if !delegationConstraintContains(constraint, target, resolvedTarget, strictParent) {
			return false, nil
		}
	}
	return true, nil
}

func (s *accessControlService) CanDelegateActionsAtScope(
	ctx context.Context,
	principal model.Principal,
	actions []string,
	scopeType model.RoleScopeType,
	scopeID string,
) (bool, error) {
	return s.canDelegateActionsAtScope(ctx, principal, actions, scopeType, scopeID)
}

func delegationConstraintContains(
	constraint authorizedScopeConstraint,
	target model.Resource,
	resolved resolvedAuthorizationResource,
	strictParent bool,
) bool {
	if constraint.InstitutionWide {
		return true
	}
	switch target.Type {
	case model.ResourceAcademicUnit:
		for _, rootID := range constraint.AcademicUnitRootIDs {
			if _, contains := resolved.academicUnitID[rootID]; contains && (!strictParent || rootID != model.AcademicUnitID(target.ID)) {
				return true
			}
		}
	case model.ResourceClass:
		if !strictParent && slices.Contains(constraint.ClassIDs, model.ClassID(target.ID)) {
			return true
		}
		for _, rootID := range constraint.AcademicUnitRootIDs {
			if _, contains := resolved.academicUnitID[rootID]; contains {
				return true
			}
		}
	}
	return false
}

func protectedDelegationAction(action model.Action) bool {
	switch action {
	case model.ActionInstitutionManage, model.ActionRoleManage,
		model.ActionRoleBindingManage, model.ActionAccessPolicyManage,
		model.ActionExternalIdentityManage:
		return true
	default:
		return false
	}
}

func (s *accessControlService) authorizedScopes(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resourceType model.ResourceType,
) (authorizedScopeConstraint, error) {
	return s.authorizedScopesAt(ctx, principal, action, resourceType, s.now())
}

func (s *accessControlService) authorizedScopesAt(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resourceType model.ResourceType,
	at time.Time,
) (authorizedScopeConstraint, error) {
	var constraint authorizedScopeConstraint
	if principal.Validate() != nil {
		return constraint, invalidTokenAppError()
	}
	definition, known := model.DefinitionForAction(action)
	if !known || !definition.AcceptsResource(resourceType) {
		return constraint, apperror.New("authorization.request.invalid")
	}
	if principal.CredentialType == model.CredentialPersonalAccessToken {
		if definition.PersonalAccessTokenForbidden || !slices.Contains(principal.CredentialScopes, string(action)) {
			return constraint, nil
		}
	}
	bindings, err := s.bindings.ListActiveByUser(ctx, principal.UserID, model.TimeUTC(at))
	if err != nil {
		return constraint, authorizationUnavailableError(err)
	}
	if len(bindings) > 256 {
		return constraint, authorizationUnavailableError(errors.New("active binding bound exceeded"))
	}
	roleIDs := make([]model.RoleID, 0, len(bindings))
	seenRoles := map[model.RoleID]struct{}{}
	for _, binding := range bindings {
		if _, exists := seenRoles[binding.RoleID]; !exists {
			seenRoles[binding.RoleID] = struct{}{}
			roleIDs = append(roleIDs, binding.RoleID)
		}
	}
	roles, err := s.roles.GetByIDs(ctx, roleIDs)
	if err != nil {
		return constraint, authorizationUnavailableError(err)
	}
	grants := map[string]bool{}
	for _, role := range roles {
		grants[role.ID.String()] = slices.Contains(role.Permissions, string(action))
	}
	unitRoots := map[model.AcademicUnitID]struct{}{}
	classIDs := map[model.ClassID]struct{}{}
	for _, binding := range bindings {
		if !grants[binding.RoleID.String()] {
			continue
		}
		switch binding.ScopeType {
		case model.RoleScopeInstitution:
			if !definition.InheritInstitutionScope {
				continue
			}
			resolved, resolveErr := s.resolver.resolve(ctx, model.Resource{Type: model.ResourceInstitution, ID: binding.ScopeID})
			if resolveErr != nil {
				return constraint, resolveErr
			}
			constraint.InstitutionID = resolved.institutionID
			if principal.AcademicUnitID.IsZero() {
				constraint.InstitutionWide = true
			} else {
				unitRoots[principal.AcademicUnitID] = struct{}{}
			}
		case model.RoleScopeAcademicUnit:
			if !definition.InheritAcademicUnitScopes {
				continue
			}
			resolved, resolveErr := s.resolver.resolve(ctx, model.Resource{Type: model.ResourceAcademicUnit, ID: binding.ScopeID})
			if resolveErr != nil {
				return constraint, resolveErr
			}
			if !principal.AcademicUnitID.IsZero() {
				if _, withinCeiling := resolved.academicUnitID[principal.AcademicUnitID]; !withinCeiling {
					continue
				}
			}
			constraint.InstitutionID = resolved.institutionID
			unitRoots[model.AcademicUnitID(binding.ScopeID)] = struct{}{}
		case model.RoleScopeClass:
			if !definition.AcceptsResource(model.ResourceClass) {
				continue
			}
			resolved, resolveErr := s.resolver.resolve(ctx, model.Resource{Type: model.ResourceClass, ID: binding.ScopeID})
			if resolveErr != nil {
				return constraint, resolveErr
			}
			if !principal.AcademicUnitID.IsZero() {
				if _, withinCeiling := resolved.academicUnitID[principal.AcademicUnitID]; !withinCeiling {
					continue
				}
			}
			constraint.InstitutionID = resolved.institutionID
			classIDs[model.ClassID(binding.ScopeID)] = struct{}{}
		}
	}
	for id := range unitRoots {
		constraint.AcademicUnitRootIDs = append(constraint.AcademicUnitRootIDs, id)
	}
	for id := range classIDs {
		constraint.ClassIDs = append(constraint.ClassIDs, id)
	}
	slices.Sort(constraint.AcademicUnitRootIDs)
	slices.Sort(constraint.ClassIDs)
	return constraint, nil
}

type accessScopeResolver struct {
	institutions    store.InstitutionStore
	academicUnits   store.AcademicUnitStore
	classes         store.ClassStore
	users           store.UserStore
	classMembers    store.ClassMemberStore
	exams           store.ExamAuthoringStore
	sittings        store.ExamSittingStore
	submissions     store.ExamSubmissionStore
	academicPeriods academicPeriodScopeReader
	programmes      store.ProgrammeStore
	programmeLevels store.ProgrammeLevelStore
}

type academicPeriodScopeReader interface {
	Get(context.Context, model.AcademicPeriodID) (*model.AcademicPeriod, error)
}

func newAccessScopeResolver(
	institutions store.InstitutionStore,
	academicUnits store.AcademicUnitStore,
	classes store.ClassStore,
	users store.UserStore,
	classMembers store.ClassMemberStore,
	exams store.ExamAuthoringStore,
	sittings store.ExamSittingStore,
	submissions store.ExamSubmissionStore,
	academicPeriods academicPeriodScopeReader,
) (*accessScopeResolver, error) {
	if institutions == nil || academicUnits == nil || classes == nil || users == nil || classMembers == nil || exams == nil ||
		sittings == nil || submissions == nil || academicPeriods == nil {
		return nil, errors.New("access scope resolver persistence is required")
	}
	resolver := &accessScopeResolver{
		institutions: institutions, academicUnits: academicUnits,
		classes: classes, users: users, classMembers: classMembers, exams: exams, sittings: sittings, submissions: submissions,
		academicPeriods: academicPeriods,
	}
	return resolver, nil
}

func (r *accessScopeResolver) resolve(
	ctx context.Context,
	resource model.Resource,
) (resolvedAuthorizationResource, error) {
	resolved := resolvedAuthorizationResource{academicUnitID: make(map[model.AcademicUnitID]struct{})}
	switch resource.Type {
	case model.ResourceInstitution:
		institution, err := r.institutions.Get(ctx, model.InstitutionID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		if institution.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "institution")
		}
		resolved.institutionID = institution.ID
	case model.ResourceAcademicUnit:
		units, err := r.academicUnits.ListAncestors(ctx, model.AcademicUnitID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "academic_unit")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = model.AcademicUnitID(resource.ID)
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "academic_unit")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceAcademicPeriod:
		period, err := r.academicPeriods.Get(ctx, model.AcademicPeriodID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("academic_period", err)
		}
		if period == nil || period.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "academic_period")
		}
		if period.Owner.InstitutionID.IsValid() {
			institution, institutionErr := r.institutions.Get(ctx, period.Owner.InstitutionID)
			if institutionErr != nil {
				return resolved, authorizationResourceError("academic_period_institution", institutionErr)
			}
			if institution.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "academic_period")
			}
			resolved.institutionID = institution.ID
			resolved.academicPeriodInstitutionOwned = true
			break
		}
		units, err := r.academicUnits.ListAncestors(ctx, period.Owner.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("academic_period_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "academic_period")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = period.Owner.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "academic_period")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceProgramme:
		if r.programmes == nil {
			return resolved, authorizationUnavailableError(errors.New("Programme persistence is required"))
		}
		programme, err := r.programmes.Get(ctx, model.ProgrammeID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("programme", err)
		}
		if programme == nil || programme.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "programme")
		}
		units, err := r.academicUnits.ListAncestors(ctx, programme.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("programme_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "programme")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = programme.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "programme")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceProgrammeLevel:
		if r.programmeLevels == nil || r.programmes == nil {
			return resolved, authorizationUnavailableError(errors.New("Programme Level persistence is required"))
		}
		level, err := r.programmeLevels.Get(ctx, model.ProgrammeLevelID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("programme_level", err)
		}
		if level == nil || level.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "programme_level")
		}
		programme, err := r.programmes.Get(ctx, level.ProgrammeID)
		if err != nil {
			return resolved, authorizationResourceError("programme_level_programme", err)
		}
		if programme == nil || programme.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "programme_level")
		}
		units, err := r.academicUnits.ListAncestors(ctx, programme.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("programme_level_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "programme_level")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = programme.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "programme_level")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceClass:
		class, err := r.classes.Get(ctx, model.ClassID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("class", err)
		}
		if class.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "class")
		}
		academicUnitID, err := r.classes.GetAcademicUnitID(ctx, model.ClassID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("class", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, academicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("class_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "class")
		}
		resolved.classID = model.ClassID(resource.ID)
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = academicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "class")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
		periodScope, err := r.resolve(ctx, model.Resource{Type: model.ResourceAcademicPeriod, ID: class.AcademicPeriodID.String()})
		if err != nil {
			if apperror.Is(err, "resource.not_found") {
				return resolved, apperror.New("resource.not_found").WithField("resource", "class")
			}
			return resolved, err
		}
		if periodScope.institutionID != resolved.institutionID {
			return resolved, apperror.New("resource.not_found").WithField("resource", "class")
		}
		if !periodScope.academicPeriodInstitutionOwned {
			if _, applicable := resolved.academicUnitID[periodScope.targetAcademicUnitID]; !applicable {
				return resolved, apperror.New("resource.not_found").WithField("resource", "class")
			}
		}
	case model.ResourceExam:
		if r.exams == nil {
			return resolved, authorizationUnavailableError(errors.New("exam persistence is required"))
		}
		exam, err := r.exams.Resolve(ctx, model.ExamID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("exam", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, exam.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("exam_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "exam")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = exam.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "exam")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceExamSitting:
		snapshot, err := r.sittings.Resolve(ctx, model.ExamSittingID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting", err)
		}
		if snapshot == nil || snapshot.Sitting == nil {
			return resolved, authorizationUnavailableError(errors.New("Exam Sitting persistence returned no projection"))
		}
		exam, err := r.exams.Resolve(ctx, snapshot.Sitting.ExamID)
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting_exam", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, exam.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "exam_sitting")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = exam.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "exam_sitting")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceSubmission:
		submissionID, err := model.ParseSubmissionID(resource.ID)
		if err != nil {
			return resolved, apperror.New("authorization.request.invalid").Wrap(err)
		}
		ownership, err := r.submissions.Resolve(ctx, submissionID)
		if err != nil {
			return resolved, authorizationResourceError("submission", err)
		}
		if ownership == nil || ownership.SubmissionID != submissionID || !ownership.ExamID.IsValid() ||
			!ownership.SittingID.IsValid() || !ownership.AttemptID.IsValid() || !ownership.AcademicUnitID.IsValid() {
			return resolved, authorizationUnavailableError(errors.New("Submission persistence returned no ownership projection"))
		}
		units, err := r.academicUnits.ListAncestors(ctx, ownership.AcademicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("submission_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, apperror.New("resource.not_found").WithField("resource", "submission")
		}
		resolved.institutionID = units[0].InstitutionID
		resolved.targetAcademicUnitID = ownership.AcademicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, apperror.New("resource.not_found").WithField("resource", "submission")
			}
			resolved.academicUnitID[unit.ID] = struct{}{}
		}
	case model.ResourceUser:
		user, err := r.users.Get(ctx, model.UserID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("user", err)
		}
		if !user.IsActive() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "user")
		}
		institution, err := r.institutions.GetSingleton(ctx)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		if institution.IsArchived() {
			return resolved, apperror.New("resource.not_found").WithField("resource", "institution")
		}
		resolved.institutionID = institution.ID
	default:
		return resolved, apperror.New("authorization.request.invalid")
	}
	return resolved, nil
}
