// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListRolesQuery struct{}

type GetRoleQuery struct {
	ID model.RoleID
}

type CreateRoleCommand struct {
	Name        string
	DisplayName string
	Description string
	Permissions []string
}

type UpdateRoleCommand struct {
	ID          model.RoleID
	DisplayName *string
	Description *string
	Permissions *[]string
}

type ArchiveRoleCommand struct {
	ID model.RoleID
}

type roleStore interface {
	Get(context.Context, model.RoleID) (*model.Role, error)
	List(context.Context) ([]*model.Role, error)
	Save(context.Context, *store.RoleCreation) (*model.Role, error)
	Update(context.Context, *store.RoleUpdate) (*model.Role, error)
	Archive(context.Context, *store.RoleArchive) (*model.Role, error)
}

type roleAuthorizer interface {
	AuthorizeInstitution(context.Context, invocation.Invocation, model.Action) (model.Resource, error)
	CanDelegateActionsAtScope(context.Context, invocation.Invocation, []string, model.RoleScopeType, string) error
}

type roleEffects interface {
	AuthorizationChanged(context.Context)
}

// roleServiceDependencies are the collaborators of roleService;
// newRoleService validates them once and roleService keeps them in deps.
type roleServiceDependencies struct {
	Roles         roleStore
	Authorization roleAuthorizer
	Audit         mutationAuditor
	Effects       roleEffects
	Now           func() time.Time
}

type roleService struct {
	deps roleServiceDependencies
}

func newRoleService(deps roleServiceDependencies) *roleService {
	return &roleService{deps: deps}
}

func (s *roleService) ListRoles(ctx context.Context, invocation invocation.Invocation, _ ListRolesQuery) ([]*model.Role, error) {
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionRoleView); err != nil {
		return nil, err
	}
	roles, err := s.deps.Roles.List(ctx)
	if err != nil {
		return nil, roleStoreErrors.Translate(err)
	}
	if roles == nil {
		roles = []*model.Role{}
	}
	return roles, nil
}

func (s *roleService) GetRole(ctx context.Context, invocation invocation.Invocation, query GetRoleQuery) (*model.Role, error) {
	id := query.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "role_id")
	}
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionRoleView); err != nil {
		return nil, err
	}
	role, err := s.deps.Roles.Get(ctx, id)
	if err != nil {
		return nil, roleStoreErrors.Translate(err)
	}
	return role, nil
}

func (s *roleService) CreateRole(ctx context.Context, invocation invocation.Invocation, command CreateRoleCommand) (*model.Role, error) {
	resource, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionRoleManage)
	if err != nil {
		return nil, err
	}
	candidate := &model.Role{
		Name: command.Name, DisplayName: command.DisplayName,
		Description: command.Description, Permissions: append([]string(nil), command.Permissions...),
		BuiltIn: false,
	}
	if err := validateKnownPermissions(candidate.Permissions); err != nil {
		return nil, err
	}
	if err := s.deps.Authorization.CanDelegateActionsAtScope(
		ctx, invocation, candidate.Permissions, model.RoleScopeInstitution, resource.ID,
	); err != nil {
		return nil, err
	}
	return runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.deps.Roles.Save(ctx, &store.RoleCreation{
				Role: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleStoreErrors.Translate,
	)
}

func (s *roleService) UpdateRole(ctx context.Context, invocation invocation.Invocation, command UpdateRoleCommand) (*model.Role, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "role_id")
	}
	resource, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionRoleManage)
	if err != nil {
		return nil, err
	}
	current, err := s.deps.Roles.Get(ctx, id)
	if err != nil {
		return nil, roleStoreErrors.Translate(err)
	}
	if current.BuiltIn {
		return nil, apperror.New("role.built_in.protected").WithField("resource", "role")
	}
	patch := &model.RolePatch{
		DisplayName: command.DisplayName,
		Description: command.Description,
		Permissions: command.Permissions,
	}
	if patch.IsEmpty() {
		return current, nil
	}
	candidate := current.Clone()
	candidate.Patch(patch)
	if err := validatePatchedPermissions(current.Permissions, command.Permissions); err != nil {
		return nil, err
	}
	if added := addedRolePermissions(current.Permissions, candidate.Permissions); len(added) > 0 {
		if err = s.deps.Authorization.CanDelegateActionsAtScope(
			ctx, invocation, added, model.RoleScopeInstitution, resource.ID,
		); err != nil {
			return nil, err
		}
	}
	updated, err := runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      map[string]any{"role_id": id},
			Prior:      current.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.deps.Roles.Update(ctx, &store.RoleUpdate{
				Role: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	s.deps.Effects.AuthorizationChanged(ctx)
	return updated, nil
}

func addedRolePermissions(current, candidate []string) []string {
	present := make(map[string]struct{}, len(current))
	for _, permission := range current {
		present[permission] = struct{}{}
	}
	added := make([]string, 0, len(candidate))
	for _, permission := range candidate {
		if _, exists := present[permission]; !exists {
			added = append(added, permission)
		}
	}
	return added
}

func (s *roleService) ArchiveRole(ctx context.Context, invocation invocation.Invocation, command ArchiveRoleCommand) error {
	id := command.ID
	if !id.IsValid() {
		return apperror.New("request.invalid").WithField("field", "role_id")
	}
	resource, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionRoleManage)
	if err != nil {
		return err
	}
	current, err := s.deps.Roles.Get(ctx, id)
	if err != nil {
		return roleStoreErrors.Translate(err)
	}
	if current.BuiltIn {
		return apperror.New("role.built_in.protected").WithField("resource", "role")
	}
	_, err = runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "archive",
			Value:      map[string]any{"role_id": id},
			Prior:      current.Auditable(),
		},
		s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.deps.Roles.Archive(ctx, &store.RoleArchive{
				ID: id, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleStoreErrors.Translate,
	)
	if err != nil {
		return err
	}
	s.deps.Effects.AuthorizationChanged(ctx)
	return nil
}

type roleAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a roleAuthorization) AuthorizeRoleBindingList(ctx context.Context, invocation invocation.Invocation, action model.Action) (store.UserVisibilityScope, error) {
	constraint, err := a.authorization.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return store.UserVisibilityScope{}, roleStoreErrors.Translate(err)
	}
	allowed := constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(constraint.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: constraint.AcademicUnitRootIDs[0].String()}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	} else if len(constraint.ClassIDs) > 0 {
		resource = model.Resource{Type: model.ResourceClass, ID: constraint.ClassIDs[0].String()}
		scopeType, scopeID = model.RoleScopeClass, resource.ID
	}
	if err := a.authorization.deps.Audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.UserVisibilityScope{}, err
	}
	if !allowed {
		return store.UserVisibilityScope{}, authorizationDeniedError()
	}
	return store.UserVisibilityScope{
		InstitutionWide:     constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]model.AcademicUnitID(nil), constraint.AcademicUnitRootIDs...),
		ClassIDs:            append([]model.ClassID(nil), constraint.ClassIDs...),
	}, nil
}

func (a roleAuthorization) AuthorizeRoleBindingPreflight(ctx context.Context, invocation invocation.Invocation, action model.Action) error {
	return a.authorization.authorizeRoleBindingPreflight(ctx, invocation, action)
}

func (a roleAuthorization) AuthorizeRoleBindingScope(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
	scopeType model.RoleScopeType,
	scopeID string,
) (model.Resource, error) {
	return a.authorization.authorizeRoleBindingScope(ctx, invocation, action, scopeType, scopeID)
}

func (a roleAuthorization) CanDelegateActionsAtScope(
	ctx context.Context,
	invocation invocation.Invocation,
	actions []string,
	scopeType model.RoleScopeType,
	scopeID string,
) error {
	allowed, err := a.authorization.canDelegateActionsAtScope(ctx, invocation.Principal(), actions, scopeType, scopeID)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizationDeniedError()
	}
	return nil
}

func (a roleAuthorization) AuthorizeInstitution(ctx context.Context, invocation invocation.Invocation, action model.Action) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, roleStoreErrors.Translate(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := a.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type roleRealtimeEffects struct {
	effects authorizationInvalidationEffects
}

func (e roleRealtimeEffects) AuthorizationChanged(ctx context.Context) {
	e.effects.InvalidateAuthorization(ctx, "")
}

var roleStoreErrors = apperror.AdministrationStoreTable("role", "role.conflict", "role.invalid", map[string]apperror.Code{
	"roles_built_in_protected": "role.built_in.protected",
})

func validateKnownPermissions(permissions []string) error {
	for _, permission := range permissions {
		if !model.IsGrantableAction(permission) {
			return apperror.New("role.permission.invalid").WithField("permission", permission)
		}
	}
	return nil
}

func validatePatchedPermissions(current []string, permissions *[]string) error {
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
		if model.IsGrantableAction(permission) {
			continue
		}
		if _, preserved := existingUnknown[permission]; preserved {
			continue
		}
		return apperror.New("role.permission.invalid").WithField("permission", permission)
	}
	return nil
}
