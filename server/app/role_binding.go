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

type ListRoleBindingsQuery struct {
	UserID    string
	ScopeType model.RoleScopeType
	ScopeID   string
}

type CreateRoleBindingCommand struct {
	UserID    string
	RoleID    string
	ScopeType model.RoleScopeType
	ScopeID   string
	StartAt   int64
	EndAt     int64
}

type EndRoleBindingCommand struct {
	ID string
}

type roleBindingStore interface {
	Get(context.Context, string) (*model.RoleBinding, error)
	ListByUser(context.Context, string) ([]*model.RoleBinding, error)
	ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error)
	SaveWithAudit(context.Context, *store.RoleBindingCreation) (*model.RoleBinding, error)
	EndWithAudit(context.Context, *store.RoleBindingEnd) (*model.RoleBinding, error)
}

type roleBindingRoleStore interface {
	Get(context.Context, string) (*model.Role, error)
}

type roleBindingAuthorizer interface {
	AuthorizeRoleBindingInstitution(context.Context, Invocation, model.Action) (model.Resource, error)
	AuthorizeRoleBindingPreflight(context.Context, Invocation, model.Action) error
	AuthorizeRoleBindingScope(context.Context, Invocation, model.Action, model.RoleScopeType, string) (model.Resource, error)
	CanDelegateActionsAtScope(context.Context, Invocation, []string, model.RoleScopeType, string) error
}

type roleBindingEffects interface {
	AuthorizationChangedForUser(context.Context, string)
}

type roleBindingService struct {
	bindings      roleBindingStore
	roles         roleBindingRoleStore
	authorization roleBindingAuthorizer
	audit         mutationAuditor
	effects       roleBindingEffects
	now           func() time.Time
}

func newRoleBindingService(
	bindings roleBindingStore,
	roles roleBindingRoleStore,
	authorization roleBindingAuthorizer,
	audit mutationAuditor,
	effects roleBindingEffects,
	now func() time.Time,
) *roleBindingService {
	return &roleBindingService{
		bindings: bindings, roles: roles, authorization: authorization,
		audit: audit, effects: effects, now: now,
	}
}

func (a *App) ListRoleBindings(ctx context.Context, invocation Invocation, query ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	return a.roleBindings.List(ctx, invocation, query)
}

func (s *roleBindingService) List(ctx context.Context, invocation Invocation, query ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	userID := strings.TrimSpace(query.UserID)
	scopeID := strings.TrimSpace(query.ScopeID)
	switch {
	case userID != "" && query.ScopeType == "" && scopeID == "":
		if !model.IsValidId(userID) {
			return nil, NewError("request.invalid").WithField("field", "user_id")
		}
		_, err := s.authorization.AuthorizeRoleBindingInstitution(ctx, invocation, model.ActionRoleBindingView)
		if err != nil {
			return nil, err
		}
		bindings, err := s.bindings.ListByUser(ctx, userID)
		if err != nil {
			return nil, roleBindingError(err)
		}
		if bindings == nil {
			bindings = []*model.RoleBinding{}
		}
		return bindings, nil
	case userID == "" && query.ScopeType.IsValid() && model.IsValidId(scopeID):
		if _, err := s.authorization.AuthorizeRoleBindingScope(ctx, invocation, model.ActionRoleBindingView, query.ScopeType, scopeID); err != nil {
			return nil, err
		}
		bindings, err := s.bindings.ListByScope(ctx, query.ScopeType, scopeID)
		if err != nil {
			return nil, roleBindingError(err)
		}
		if bindings == nil {
			bindings = []*model.RoleBinding{}
		}
		return bindings, nil
	default:
		return nil, NewError("request.invalid").WithField("field", "scope")
	}
}

func (a *App) CreateRoleBinding(ctx context.Context, invocation Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	return a.roleBindings.Create(ctx, invocation, command)
}

func (s *roleBindingService) Create(ctx context.Context, invocation Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return nil, NewError("request.invalid").WithField("field", "role_id")
	}
	scopeID := strings.TrimSpace(command.ScopeID)
	if !command.ScopeType.IsValid() || !model.IsValidId(scopeID) {
		return nil, NewError("request.invalid").WithField("field", "role_binding")
	}
	if command.StartAt < 0 || (command.EndAt != 0 && command.EndAt <= command.StartAt) {
		return nil, NewError("request.invalid").WithField("field", "role_binding")
	}
	resource, err := s.authorization.AuthorizeRoleBindingScope(
		ctx, invocation, model.ActionRoleBindingManage, command.ScopeType, scopeID,
	)
	if err != nil {
		return nil, err
	}
	candidate := &model.RoleBinding{
		UserID: userID, RoleID: roleID,
		ScopeType: command.ScopeType, ScopeID: scopeID,
	}
	if command.StartAt > 0 {
		candidate.StartsAt = model.TimeFromMillis(command.StartAt)
	}
	if command.EndAt > 0 {
		candidate.EndsAt = model.OptionalTimeFromMillis(command.EndAt)
	}
	role, err := s.roles.Get(ctx, candidate.RoleID.String())
	if err != nil {
		return nil, roleBindingError(err)
	}
	if role.Name == model.SystemAdministratorRoleName &&
		candidate.ScopeType != model.RoleScopeInstitution {
		return nil, NewError("role_binding.system_admin_requires_institution_scope")
	}
	delegatedPermissions := role.Permissions
	if role.BuiltIn && role.Name == model.SystemAdministratorRoleName {
		// Downgrade-preserved unknown permissions remain dormant and must not
		// make the protected administrator Role impossible to bind. Every
		// action recognized by this binary is still checked.
		delegatedPermissions = model.AllActions()
	}
	if err := s.authorization.CanDelegateActionsAtScope(
		ctx, invocation, delegatedPermissions, candidate.ScopeType, candidate.ScopeID,
	); err != nil {
		return nil, err
	}
	saved, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleBindingManage,
			Resource:   resource,
			Operation:  "create_binding",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			return s.bindings.SaveWithAudit(ctx, &store.RoleBindingCreation{
				Binding: candidate, ExpectedRoleUpdatedAt: role.UpdatedAt,
				ExpectedRolePermissions: append([]string(nil), role.Permissions...),
				AuditEventID:            reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleBindingError,
	)
	if err != nil {
		return nil, err
	}
	s.effects.AuthorizationChangedForUser(ctx, saved.UserID.String())
	return saved, nil
}

func (a *App) EndRoleBinding(ctx context.Context, invocation Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	return a.roleBindings.End(ctx, invocation, command)
}

func (s *roleBindingService) End(ctx context.Context, invocation Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	id := strings.TrimSpace(command.ID)
	if !model.IsValidId(id) {
		return nil, NewError("request.invalid").WithField("field", "role_binding_id")
	}
	if err := s.authorization.AuthorizeRoleBindingPreflight(ctx, invocation, model.ActionRoleBindingManage); err != nil {
		return nil, err
	}
	current, err := s.bindings.Get(ctx, id)
	if err != nil {
		return nil, roleBindingError(err)
	}
	resource, err := s.authorization.AuthorizeRoleBindingScope(
		ctx, invocation, model.ActionRoleBindingManage, current.ScopeType, current.ScopeID,
	)
	if err != nil {
		return nil, err
	}
	ended, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleBindingManage,
			Resource:   resource,
			Operation:  "end_binding",
			Value:      map[string]any{"role_binding_id": id},
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			return s.bindings.EndWithAudit(ctx, &store.RoleBindingEnd{
				ID: id, EndAt: reference.MutationAtMillis,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		roleBindingError,
	)
	if err != nil {
		return nil, err
	}
	s.effects.AuthorizationChangedForUser(ctx, ended.UserID.String())
	return ended, nil
}

type roleBindingRealtimeEffects struct {
	effects authorizationInvalidationEffects
}

func (e roleBindingRealtimeEffects) AuthorizationChangedForUser(ctx context.Context, userID string) {
	e.effects.InvalidateAuthorization(ctx, userID)
}

func roleBindingError(err error) error {
	var appFailure *Error
	if errors.As(err, &appFailure) {
		return err
	}
	switch {
	case store.IsNotFound(err):
		return NewError("resource.not_found").WithField("resource", "role_binding").Wrap(err)
	case store.IsConflict(err):
		var conflict *store.ErrConflict
		_ = errors.As(err, &conflict)
		if conflict != nil && conflict.Constraint == "role_bindings_last_system_admin" {
			return NewError("role_binding.last_system_admin").WithField("resource", "role_binding").Wrap(err)
		}
		if conflict != nil && conflict.Constraint == "role_bindings_system_admin_institution_scope" {
			return NewError("role_binding.system_admin_requires_institution_scope").WithField("resource", "role_binding").Wrap(err)
		}
		return NewError("role_binding.conflict").WithField("resource", "role_binding").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		var reference *store.ErrReference
		if errors.As(err, &invalid) || errors.As(err, &reference) {
			return NewError("role_binding.invalid").WithField("resource", "role_binding").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "role_binding").Wrap(err)
	}
}
