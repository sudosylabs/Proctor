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
	AuthorizeManage(context.Context, Invocation) (model.Resource, error)
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
	if _, err := s.authorization.AuthorizeManage(ctx, invocation); err != nil {
		return nil, err
	}
	userID := strings.TrimSpace(query.UserID)
	scopeID := strings.TrimSpace(query.ScopeID)
	switch {
	case userID != "" && query.ScopeType == "" && scopeID == "":
		if !model.IsValidId(userID) {
			return nil, NewError("request.invalid").WithField("field", "user_id")
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
	resource, err := s.authorization.AuthorizeManage(ctx, invocation)
	if err != nil {
		return nil, err
	}
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
	saved, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "create_binding",
			Value:      candidate.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			return s.bindings.SaveWithAudit(ctx, &store.RoleBindingCreation{
				Binding: candidate, AuditEventID: reference.ID, AuditAt: reference.At,
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
	resource, err := s.authorization.AuthorizeManage(ctx, invocation)
	if err != nil {
		return nil, err
	}
	current, err := s.bindings.Get(ctx, id)
	if err != nil {
		return nil, roleBindingError(err)
	}
	ended, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleManage,
			Resource:   resource,
			Operation:  "end_binding",
			Value:      map[string]any{"role_binding_id": id},
			Prior:      current.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			return s.bindings.EndWithAudit(ctx, &store.RoleBindingEnd{
				ID: id, EndAt: reference.At,
				AuditEventID: reference.ID, AuditAt: reference.At,
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

func roleBindingError(err error) *Error {
	var appFailure *Error
	if errors.As(err, &appFailure) {
		return appFailure
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
