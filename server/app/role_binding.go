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
	UserID                    string
	RoleID                    string
	ScopeType                 model.RoleScopeType
	ScopeID                   string
	StartAt                   int64
	EndAt                     int64
	IdempotencyKey            string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type EndRoleBindingCommand struct {
	ID                        string
	IdempotencyKey            string
	BatchScopeType            model.RoleScopeType
	BatchScopeID              string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type roleBindingStore interface {
	Get(context.Context, string) (*model.RoleBinding, error)
	ListByUser(context.Context, string) ([]*model.RoleBinding, error)
	ListVisibleByUser(context.Context, string, store.UserVisibilityScope) ([]*model.RoleBinding, error)
	ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error)
	SaveWithAudit(context.Context, *store.RoleBindingCreation) (*model.RoleBinding, error)
	EndWithAudit(context.Context, *store.RoleBindingEnd) (*model.RoleBinding, error)
}

type roleBindingRoleStore interface {
	Get(context.Context, string) (*model.Role, error)
	GetIncludingArchived(context.Context, string) (*model.Role, error)
}

type roleBindingAuthorizer interface {
	AuthorizeRoleBindingInstitution(context.Context, Invocation, model.Action) (model.Resource, error)
	AuthorizeRoleBindingList(context.Context, Invocation, model.Action) (store.UserVisibilityScope, error)
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
	capabilities  accessPolicyCapabilitySource
	audit         mutationAuditor
	effects       roleBindingEffects
	now           func() time.Time
}

func newRoleBindingService(
	bindings roleBindingStore,
	roles roleBindingRoleStore,
	authorization roleBindingAuthorizer,
	capabilities accessPolicyCapabilitySource,
	audit mutationAuditor,
	effects roleBindingEffects,
	now func() time.Time,
) *roleBindingService {
	return &roleBindingService{
		bindings: bindings, roles: roles, authorization: authorization,
		capabilities: capabilities, audit: audit, effects: effects, now: now,
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
		visibility, err := s.authorization.AuthorizeRoleBindingList(ctx, invocation, model.ActionRoleBindingView)
		if err != nil {
			return nil, err
		}
		bindings, err := s.bindings.ListVisibleByUser(ctx, userID, visibility)
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
	var role *model.Role
	if command.batchRetainedOutcome {
		role, err = s.roles.GetIncludingArchived(ctx, candidate.RoleID.String())
	} else {
		role, err = s.roles.Get(ctx, candidate.RoleID.String())
	}
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
	idempotency, err := newCommandIdempotency(invocation, "role_binding.create.v1", command.IdempotencyKey, struct {
		UserID    string              `json:"user_id"`
		RoleID    string              `json:"role_id"`
		ScopeType model.RoleScopeType `json:"scope_type"`
		ScopeID   string              `json:"scope_id"`
		StartAt   int64               `json:"start_at"`
		EndAt     int64               `json:"end_at"`
	}{userID.String(), roleID.String(), command.ScopeType, scopeID, command.StartAt, command.EndAt})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	if command.batchAuthorization != nil {
		command.batchAuthorization.DelegatedActions = make([]model.Action, 0, len(delegatedPermissions))
		for _, permission := range delegatedPermissions {
			command.batchAuthorization.DelegatedActions = append(command.batchAuthorization.DelegatedActions, model.Action(permission))
		}
	}
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
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
			input := &store.RoleBindingCreation{
				Binding: candidate, ExpectedRoleUpdatedAt: role.UpdatedAt,
				ExpectedRolePermissions: append([]string(nil), role.Permissions...),
				AuditEventID:            reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.bindings.SaveWithAudit(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
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
	if command.BatchScopeID != "" && (command.BatchScopeType != current.ScopeType || strings.TrimSpace(command.BatchScopeID) != current.ScopeID) {
		return nil, NewError("resource.not_found").WithField("resource", "role_binding")
	}
	idempotency, err := newCommandIdempotency(invocation, "role_binding.end.v1", command.IdempotencyKey, struct {
		ID        string              `json:"id"`
		ScopeType model.RoleScopeType `json:"scope_type,omitempty"`
		ScopeID   string              `json:"scope_id,omitempty"`
	}{id, command.BatchScopeType, strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
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
			input := &store.RoleBindingEnd{
				ID: id, EndAt: reference.MutationAtMillis,
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.bindings.EndWithAudit(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
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
	if mapped := idempotencyError(err); mapped != nil {
		return mapped
	}
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
