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
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListRolesQuery struct{}

type GetRoleQuery struct {
	ID string
}

type CreateRoleCommand struct {
	Name        string
	DisplayName string
	Description string
	Permissions []string
}

type UpdateRoleCommand struct {
	ID          string
	DisplayName *string
	Description *string
	Permissions *[]string
}

type ArchiveRoleCommand struct {
	ID string
}

type roleStore interface {
	Get(context.Context, string) (*model.Role, error)
	List(context.Context) ([]*model.Role, error)
	SaveWithAudit(context.Context, *store.RoleCreation) (*model.Role, error)
	UpdateWithAudit(context.Context, *store.RoleUpdate) (*model.Role, error)
	ArchiveWithAudit(context.Context, *store.RoleArchive) (*model.Role, error)
}

type roleAuthorizer interface {
	AuthorizeView(context.Context, Invocation) (model.Resource, error)
	AuthorizeManage(context.Context, Invocation) (model.Resource, error)
	CanDelegateActionsAtScope(context.Context, Invocation, []string, model.RoleScopeType, string) error
}

type roleEffects interface {
	AuthorizationChanged(context.Context)
}

type roleService struct {
	roles         roleStore
	authorization roleAuthorizer
	audit         mutationAuditor
	effects       roleEffects
	now           func() time.Time
}

func newRoleService(
	roles roleStore,
	authorization roleAuthorizer,
	audit mutationAuditor,
	effects roleEffects,
	now func() time.Time,
) *roleService {
	return &roleService{roles: roles, authorization: authorization, audit: audit, effects: effects, now: now}
}

func (a *App) ListRoles(ctx context.Context, invocation Invocation, _ ListRolesQuery) ([]*model.Role, error) {
	return a.roles.List(ctx, invocation)
}

func (s *roleService) List(ctx context.Context, invocation Invocation) ([]*model.Role, error) {
	if _, err := s.authorization.AuthorizeView(ctx, invocation); err != nil {
		return nil, err
	}
	roles, err := s.roles.List(ctx)
	if err != nil {
		return nil, roleError(err)
	}
	if roles == nil {
		roles = []*model.Role{}
	}
	return roles, nil
}

func (a *App) GetRole(ctx context.Context, invocation Invocation, query GetRoleQuery) (*model.Role, error) {
	return a.roles.Get(ctx, invocation, query)
}

func (s *roleService) Get(ctx context.Context, invocation Invocation, query GetRoleQuery) (*model.Role, error) {
	id := strings.TrimSpace(query.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "role_id")
	}
	if _, err := s.authorization.AuthorizeView(ctx, invocation); err != nil {
		return nil, err
	}
	role, err := s.roles.Get(ctx, id)
	if err != nil {
		return nil, roleError(err)
	}
	return role, nil
}

func (a *App) CreateRole(ctx context.Context, invocation Invocation, command CreateRoleCommand) (*model.Role, error) {
	return a.roles.Create(ctx, invocation, command)
}

func (s *roleService) Create(ctx context.Context, invocation Invocation, command CreateRoleCommand) (*model.Role, error) {
	resource, err := s.authorization.AuthorizeManage(ctx, invocation)
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
	if err := s.authorization.CanDelegateActionsAtScope(
		ctx, invocation, candidate.Permissions, model.RoleScopeInstitution, resource.ID,
	); err != nil {
		return nil, err
	}
	return runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "create",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.roles.SaveWithAudit(ctx, &store.RoleCreation{
				Role: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleError,
	)
}

func (a *App) UpdateRole(ctx context.Context, invocation Invocation, command UpdateRoleCommand) (*model.Role, error) {
	return a.roles.Update(ctx, invocation, command)
}

func (s *roleService) Update(ctx context.Context, invocation Invocation, command UpdateRoleCommand) (*model.Role, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "role_id")
	}
	resource, err := s.authorization.AuthorizeManage(ctx, invocation)
	if err != nil {
		return nil, err
	}
	current, err := s.roles.Get(ctx, id)
	if err != nil {
		return nil, roleError(err)
	}
	if current.BuiltIn {
		return nil, NewError("role.built_in.protected").WithField("resource", "role")
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
		if err := s.authorization.CanDelegateActionsAtScope(
			ctx, invocation, added, model.RoleScopeInstitution, resource.ID,
		); err != nil {
			return nil, err
		}
	}
	updated, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "patch",
			Value:      map[string]any{"role_id": id},
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.roles.UpdateWithAudit(ctx, &store.RoleUpdate{
				Role: candidate, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleError,
	)
	if err != nil {
		return nil, err
	}
	s.effects.AuthorizationChanged(ctx)
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

func (a *App) ArchiveRole(ctx context.Context, invocation Invocation, command ArchiveRoleCommand) error {
	return a.roles.Archive(ctx, invocation, command)
}

func (s *roleService) Archive(ctx context.Context, invocation Invocation, command ArchiveRoleCommand) error {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return NewError("request.invalid").WithField("field", "role_id")
	}
	resource, err := s.authorization.AuthorizeManage(ctx, invocation)
	if err != nil {
		return err
	}
	current, err := s.roles.Get(ctx, id)
	if err != nil {
		return roleError(err)
	}
	if current.BuiltIn {
		return NewError("role.built_in.protected").WithField("resource", "role")
	}
	_, err = runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "archive",
			Value:      map[string]any{"role_id": id},
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.Role, error) {
			return s.roles.ArchiveWithAudit(ctx, &store.RoleArchive{
				ID: id, ArchiveAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleError,
	)
	if err != nil {
		return err
	}
	s.effects.AuthorizationChanged(ctx)
	return nil
}

type roleAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a roleAuthorization) AuthorizeManage(ctx context.Context, invocation Invocation) (model.Resource, error) {
	return a.authorizeInstitution(ctx, invocation, model.ActionRoleManage)
}

func (a roleAuthorization) AuthorizeView(ctx context.Context, invocation Invocation) (model.Resource, error) {
	return a.authorizeInstitution(ctx, invocation, model.ActionRoleView)
}

func (a roleAuthorization) AuthorizeRoleBindingInstitution(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	return a.authorizeInstitution(ctx, invocation, action)
}

func (a roleAuthorization) AuthorizeRoleBindingList(ctx context.Context, invocation Invocation, action model.Action) (store.UserVisibilityScope, error) {
	constraint, err := a.authorization.authorizedScopes(ctx, invocation.Principal(), action, model.ResourceInstitution)
	if err != nil {
		return store.UserVisibilityScope{}, err
	}
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return store.UserVisibilityScope{}, roleError(err)
	}
	allowed := constraint.InstitutionWide || len(constraint.AcademicUnitRootIDs) > 0 || len(constraint.ClassIDs) > 0
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	scopeType, scopeID := model.RoleScopeInstitution, institution.ID.String()
	if len(constraint.AcademicUnitRootIDs) > 0 {
		resource = model.Resource{Type: model.ResourceAcademicUnit, ID: constraint.AcademicUnitRootIDs[0]}
		scopeType, scopeID = model.RoleScopeAcademicUnit, resource.ID
	} else if len(constraint.ClassIDs) > 0 {
		resource = model.Resource{Type: model.ResourceClass, ID: constraint.ClassIDs[0]}
		scopeType, scopeID = model.RoleScopeClass, resource.ID
	}
	if err := a.authorization.audit.RecordAuthorizationDecision(ctx, invocation.Principal(), action, resource, scopeType, scopeID, invocation.RequestMetadata(), allowed); err != nil {
		return store.UserVisibilityScope{}, err
	}
	if !allowed {
		return store.UserVisibilityScope{}, authorizationDeniedError("roleAuthorization.AuthorizeRoleBindingList")
	}
	return store.UserVisibilityScope{
		InstitutionWide:     constraint.InstitutionWide,
		AcademicUnitRootIDs: append([]string(nil), constraint.AcademicUnitRootIDs...),
		ClassIDs:            append([]string(nil), constraint.ClassIDs...),
	}, nil
}

func (a roleAuthorization) AuthorizeRoleBindingPreflight(ctx context.Context, invocation Invocation, action model.Action) error {
	return a.authorization.authorizeRoleBindingPreflight(ctx, invocation, action)
}

func (a roleAuthorization) AuthorizeRoleBindingScope(
	ctx context.Context,
	invocation Invocation,
	action model.Action,
	scopeType model.RoleScopeType,
	scopeID string,
) (model.Resource, error) {
	return a.authorization.authorizeRoleBindingScope(ctx, invocation, action, scopeType, scopeID)
}

func (a roleAuthorization) CanDelegateActionsAtScope(
	ctx context.Context,
	invocation Invocation,
	actions []string,
	scopeType model.RoleScopeType,
	scopeID string,
) error {
	allowed, err := a.authorization.canDelegateActionsAtScope(ctx, invocation.Principal(), actions, scopeType, scopeID)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizationDeniedError("roleAuthorization.CanDelegateActionsAtScope")
	}
	return nil
}

func (a roleAuthorization) authorizeInstitution(ctx context.Context, invocation Invocation, action model.Action) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, roleError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		action,
		resource,
		invocation.RequestMetadata(),
	); err != nil {
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

func roleError(err error) error {
	var appFailure *Error
	if errors.As(err, &appFailure) {
		return err
	}
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "role").Wrap(err)
	case store.IsConflict(err):
		var conflict *store.ErrConflict
		_ = errors.As(err, &conflict)
		if conflict != nil && conflict.Constraint == "roles_built_in_protected" {
			return NewError("role.built_in.protected").WithField("resource", "role").Wrap(err)
		}
		return NewError("role.conflict").WithField("resource", "role").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("role.invalid").WithField("resource", "role").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "role").Wrap(err)
	}
}

func validateKnownPermissions(permissions []string) error {
	for _, permission := range permissions {
		if !model.IsGrantableAction(permission) {
			return NewError("role.permission.unknown").WithField("permission", permission)
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
		return NewError("role.permission.unknown").WithField("permission", permission)
	}
	return nil
}
