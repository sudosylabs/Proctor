// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// authorizedScopeConstraint is the bounded, persistence-ready result of an
// Access Control evaluation for a list or search query. AcademicUnitRootIDs
// mean the named subtrees; consumers must apply them in persistence rather
// than load rows and filter them in application memory.
type authorizedScopeConstraint struct {
	InstitutionID       string
	InstitutionWide     bool
	AcademicUnitRootIDs []string
	ClassIDs            []string
}

func (s *accessControlService) authorizeUserSearch(
	ctx context.Context,
	invocation Invocation,
) (store.UserVisibilityScope, error) {
	principal := invocation.Principal()
	userScope, err := s.authorizedScopes(ctx, principal, model.ActionUserView, model.ResourceUser)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	classScope, err := s.authorizedScopes(ctx, principal, model.ActionClassMembersView, model.ResourceClass)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	visibility := store.UserVisibilityScope{
		InstitutionWide:     userScope.InstitutionWide,
		ClassIDs:            append([]string(nil), classScope.ClassIDs...),
		AcademicUnitRootIDs: append([]string(nil), classScope.AcademicUnitRootIDs...),
		ActiveAt:            s.now().UnixMilli(),
	}
	institution, err := s.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.UserVisibilityScope{}, authorizationResourceError("institution", err)
	}
	allowed := visibility.InstitutionWide || len(visibility.ClassIDs) > 0 || len(visibility.AcademicUnitRootIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := s.audit.RecordUserSearchDecision(
		ctx, principal, resource, invocation.RequestMetadata(), allowed,
	); err != nil {
		return store.UserVisibilityScope{}, err
	}
	if !allowed {
		return store.UserVisibilityScope{}, authorizationDeniedError("accessControlService.authorizeUserSearch")
	}
	return visibility, nil
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
	if !known || definition.ResourceType != resourceType {
		return constraint, NewError("authorization.request.invalid")
	}
	if principal.CredentialType == model.CredentialPersonalAccessToken &&
		!slices.Contains(principal.CredentialScopes, string(action)) {
		return constraint, nil
	}
	bindings, err := s.bindings.ListActiveByUser(ctx, principal.UserID.String(), model.MillisFromTime(at))
	if err != nil {
		return constraint, authorizationUnavailableError("accessControlService.authorizedScopes.bindings", err)
	}
	if len(bindings) > 256 {
		return constraint, authorizationUnavailableError("accessControlService.authorizedScopes.bound", errors.New("active binding bound exceeded"))
	}
	roleIDs := make([]string, 0, len(bindings))
	seenRoles := map[string]struct{}{}
	for _, binding := range bindings {
		if _, exists := seenRoles[binding.RoleID.String()]; !exists {
			seenRoles[binding.RoleID.String()] = struct{}{}
			roleIDs = append(roleIDs, binding.RoleID.String())
		}
	}
	roles, err := s.roles.GetByIds(ctx, roleIDs)
	if err != nil {
		return constraint, authorizationUnavailableError("accessControlService.authorizedScopes.roles", err)
	}
	grants := map[string]bool{}
	for _, role := range roles {
		grants[role.ID.String()] = slices.Contains(role.Permissions, string(action))
	}
	unitRoots := map[string]struct{}{}
	classIDs := map[string]struct{}{}
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
				unitRoots[principal.AcademicUnitID.String()] = struct{}{}
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
				if _, withinCeiling := resolved.academicUnitID[principal.AcademicUnitID.String()]; !withinCeiling {
					continue
				}
			}
			constraint.InstitutionID = resolved.institutionID
			unitRoots[binding.ScopeID] = struct{}{}
		case model.RoleScopeClass:
			if resourceType != model.ResourceClass {
				continue
			}
			resolved, resolveErr := s.resolver.resolve(ctx, model.Resource{Type: model.ResourceClass, ID: binding.ScopeID})
			if resolveErr != nil {
				return constraint, resolveErr
			}
			if !principal.AcademicUnitID.IsZero() {
				if _, withinCeiling := resolved.academicUnitID[principal.AcademicUnitID.String()]; !withinCeiling {
					continue
				}
			}
			constraint.InstitutionID = resolved.institutionID
			classIDs[binding.ScopeID] = struct{}{}
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
	institutions  store.InstitutionStore
	academicUnits store.AcademicUnitStore
	classes       store.ClassStore
	users         store.UserStore
	classMembers  store.ClassMemberStore
	exams         store.ExamAuthoringStore
	sittings      store.ExamSittingStore
}

func newAccessScopeResolver(
	institutions store.InstitutionStore,
	academicUnits store.AcademicUnitStore,
	classes store.ClassStore,
	users store.UserStore,
	classMembers store.ClassMemberStore,
	exams store.ExamAuthoringStore,
	sittings store.ExamSittingStore,
) (*accessScopeResolver, error) {
	if institutions == nil || academicUnits == nil || classes == nil || users == nil || classMembers == nil || exams == nil || sittings == nil {
		return nil, errors.New("access scope resolver persistence is required")
	}
	return &accessScopeResolver{
		institutions: institutions, academicUnits: academicUnits,
		classes: classes, users: users, classMembers: classMembers, exams: exams, sittings: sittings,
	}, nil
}

func (r *accessScopeResolver) userClasses(ctx context.Context, userID string, at int64) ([]model.Resource, error) {
	memberships, err := r.classMembers.ListActiveByUser(ctx, userID, at)
	if err != nil {
		return nil, authorizationUnavailableError("accessScopeResolver.userClasses", err)
	}
	resources := make([]model.Resource, 0, len(memberships))
	for _, membership := range memberships {
		resource := model.Resource{Type: model.ResourceClass, ID: membership.ClassID.String()}
		if _, resolveErr := r.resolve(ctx, resource); resolveErr != nil {
			if Is(resolveErr, "resource.not_found") {
				continue
			}
			return nil, resolveErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (r *accessScopeResolver) resolve(
	ctx context.Context,
	resource model.Resource,
) (resolvedAuthorizationResource, error) {
	resolved := resolvedAuthorizationResource{academicUnitID: make(map[string]struct{})}
	switch resource.Type {
	case model.ResourceInstitution:
		institution, err := r.institutions.Get(ctx, resource.ID)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		if institution.IsArchived() {
			return resolved, NewError("resource.not_found").WithField("resource", "institution")
		}
		resolved.institutionID = institution.ID.String()
	case model.ResourceAcademicUnit:
		units, err := r.academicUnits.ListAncestors(ctx, resource.ID)
		if err != nil {
			return resolved, authorizationResourceError("academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, NewError("resource.not_found").WithField("resource", "academic_unit")
		}
		resolved.institutionID = units[0].InstitutionID.String()
		resolved.targetAcademicUnitID = resource.ID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, NewError("resource.not_found").WithField("resource", "academic_unit")
			}
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
		}
	case model.ResourceClass:
		class, err := r.classes.Get(ctx, resource.ID)
		if err != nil {
			return resolved, authorizationResourceError("class", err)
		}
		if class.IsArchived() {
			return resolved, NewError("resource.not_found").WithField("resource", "class")
		}
		academicUnitID, err := r.classes.GetAcademicUnitId(ctx, resource.ID)
		if err != nil {
			return resolved, authorizationResourceError("class", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, academicUnitID)
		if err != nil {
			return resolved, authorizationResourceError("class_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, NewError("resource.not_found").WithField("resource", "class")
		}
		resolved.classID = resource.ID
		resolved.institutionID = units[0].InstitutionID.String()
		resolved.targetAcademicUnitID = academicUnitID
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, NewError("resource.not_found").WithField("resource", "class")
			}
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
		}
	case model.ResourceExam:
		if r.exams == nil {
			return resolved, authorizationUnavailableError("accessScopeResolver.exam", errors.New("exam persistence is required"))
		}
		exam, err := r.exams.Resolve(ctx, model.ExamID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("exam", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, exam.AcademicUnitID.String())
		if err != nil {
			return resolved, authorizationResourceError("exam_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, NewError("resource.not_found").WithField("resource", "exam")
		}
		resolved.institutionID = units[0].InstitutionID.String()
		resolved.targetAcademicUnitID = exam.AcademicUnitID.String()
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, NewError("resource.not_found").WithField("resource", "exam")
			}
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
		}
	case model.ResourceExamSitting:
		snapshot, err := r.sittings.Resolve(ctx, model.ExamSittingID(resource.ID))
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting", err)
		}
		if snapshot == nil || snapshot.Sitting == nil {
			return resolved, authorizationUnavailableError("accessScopeResolver.exam_sitting", errors.New("Exam Sitting persistence returned no projection"))
		}
		exam, err := r.exams.Resolve(ctx, snapshot.Sitting.ExamID)
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting_exam", err)
		}
		units, err := r.academicUnits.ListAncestors(ctx, exam.AcademicUnitID.String())
		if err != nil {
			return resolved, authorizationResourceError("exam_sitting_academic_unit", err)
		}
		if len(units) == 0 {
			return resolved, NewError("resource.not_found").WithField("resource", "exam_sitting")
		}
		resolved.institutionID = units[0].InstitutionID.String()
		resolved.targetAcademicUnitID = exam.AcademicUnitID.String()
		for _, unit := range units {
			if unit == nil || unit.IsArchived() {
				return resolved, NewError("resource.not_found").WithField("resource", "exam_sitting")
			}
			resolved.academicUnitID[unit.ID.String()] = struct{}{}
		}
	case model.ResourceUser:
		user, err := r.users.Get(ctx, resource.ID)
		if err != nil {
			return resolved, authorizationResourceError("user", err)
		}
		if !user.IsActive() {
			return resolved, NewError("resource.not_found").WithField("resource", "user")
		}
		institution, err := r.institutions.GetSingleton(ctx)
		if err != nil {
			return resolved, authorizationResourceError("institution", err)
		}
		if institution.IsArchived() {
			return resolved, NewError("resource.not_found").WithField("resource", "institution")
		}
		resolved.institutionID = institution.ID.String()
	default:
		return resolved, NewError("authorization.request.invalid")
	}
	return resolved, nil
}
