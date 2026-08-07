// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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

type DeleteRoleCommand struct {
	ID string
}

type roleStore interface {
	Get(context.Context, string) (*model.Role, error)
	List(context.Context) ([]*model.Role, error)
	SaveWithAudit(context.Context, *store.RoleCreation) (*model.Role, error)
	UpdateWithAudit(context.Context, *store.RoleUpdate) (*model.Role, error)
	DeleteWithAudit(context.Context, *store.RoleDeletion) (*model.Role, error)
}

type roleAuthorizer interface {
	AuthorizeManage(context.Context, Invocation) (model.Resource, error)
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
	if _, err := s.authorization.AuthorizeManage(ctx, invocation); err != nil {
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
	if _, err := s.authorization.AuthorizeManage(ctx, invocation); err != nil {
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
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionRoleManage, resource, "create", candidate.Auditable(), nil,
	)
	if err != nil {
		return nil, err
	}
	at := s.now().UnixMilli()
	saved, err := s.roles.SaveWithAudit(ctx, &store.RoleCreation{
		Role: candidate, AuditEventID: auditID, AuditAt: at,
	})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	return saved, nil
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
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionRoleManage, resource, "patch",
		map[string]any{"role_id": id}, current.Auditable(),
	)
	if err != nil {
		return nil, err
	}
	at := s.now().UnixMilli()
	updated, err := s.roles.UpdateWithAudit(ctx, &store.RoleUpdate{
		Role: candidate, AuditEventID: auditID, AuditAt: at,
	})
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	s.effects.AuthorizationChanged(ctx)
	return updated, nil
}

func (a *App) DeleteRole(ctx context.Context, invocation Invocation, command DeleteRoleCommand) error {
	return a.roles.Delete(ctx, invocation, command)
}

func (s *roleService) Delete(ctx context.Context, invocation Invocation, command DeleteRoleCommand) error {
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
	auditID, err := s.audit.Begin(
		ctx, invocation, model.ActionRoleManage, resource, "delete",
		map[string]any{"role_id": id}, current.Auditable(),
	)
	if err != nil {
		return err
	}
	at := s.now().UnixMilli()
	if _, err := s.roles.DeleteWithAudit(ctx, &store.RoleDeletion{
		ID: id, DeleteAt: at, AuditEventID: auditID, AuditAt: at,
	}); err != nil {
		return s.failMutation(ctx, auditID, err)
	}
	s.effects.AuthorizationChanged(ctx)
	return nil
}

type roleAuthorization struct {
	authorization *AuthorizationService
	institutions  store.InstitutionStore
}

func (a roleAuthorization) AuthorizeManage(ctx context.Context, invocation Invocation) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, roleError(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, Id: institution.Id}
	if err := a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		model.ActionRoleManage,
		resource,
		invocation.RequestMetadata(),
	); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}

type roleRealtimeEffects struct {
	realtime *RealtimeService
}

func (e roleRealtimeEffects) AuthorizationChanged(ctx context.Context) {
	e.realtime.InvalidateAuthorization(ctx, "")
}

func (s *roleService) failMutation(ctx context.Context, auditID string, err error) error {
	mapped := roleError(err)
	failure, _ := As(mapped)
	if auditErr := s.audit.Fail(ctx, auditID, failure.Code()); auditErr != nil {
		return auditErr
	}
	return mapped
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
		if !model.IsKnownAction(permission) {
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
		if model.IsKnownAction(permission) {
			continue
		}
		if _, preserved := existingUnknown[permission]; preserved {
			continue
		}
		return NewError("role.permission.unknown").WithField("permission", permission)
	}
	return nil
}
