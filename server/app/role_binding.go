// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
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
	ID                        model.RoleBindingID
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
	Get(context.Context, model.RoleBindingID) (*model.RoleBinding, error)
	ListVisibleByUser(context.Context, model.UserID, store.UserVisibilityScope) ([]*model.RoleBinding, error)
	ListByScope(context.Context, model.RoleScopeType, string) ([]*model.RoleBinding, error)
	Save(context.Context, *store.RoleBindingCreation) (*model.RoleBinding, error)
	End(context.Context, *store.RoleBindingEnd) (*model.RoleBinding, error)
}

type roleBindingRoleStore interface {
	Get(context.Context, model.RoleID) (*model.Role, error)
	GetIncludingArchived(context.Context, model.RoleID) (*model.Role, error)
}

type roleBindingUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

type roleBindingAuthorizer interface {
	AuthorizeRoleBindingList(context.Context, invocation.Invocation, model.Action) (store.UserVisibilityScope, error)
	AuthorizeRoleBindingPreflight(context.Context, invocation.Invocation, model.Action) error
	AuthorizeRoleBindingScope(context.Context, invocation.Invocation, model.Action, model.RoleScopeType, string) (model.Resource, error)
	AuthorizeActionDelegation(context.Context, invocation.Invocation, []string, model.RoleScopeType, string) error
}

type roleBindingEffects interface {
	AuthorizationChangedForUser(context.Context, string)
}

// roleBindingServiceDependencies are the collaborators of roleBindingService;
// newRoleBindingService validates them once and roleBindingService keeps them in deps.
type roleBindingServiceDependencies struct {
	Bindings      roleBindingStore
	Roles         roleBindingRoleStore
	Users         roleBindingUserStore
	Authorization roleBindingAuthorizer
	Capabilities  accessPolicyCapabilitySource
	Audit         mutationAuditor
	Mail          relationshipTransitionMailPreparer
	Effects       roleBindingEffects
	Now           func() time.Time
}

type roleBindingService struct {
	deps roleBindingServiceDependencies
}

func newRoleBindingService(deps roleBindingServiceDependencies) *roleBindingService {
	return &roleBindingService{deps: deps}
}

func (s *roleBindingService) ListRoleBindings(ctx context.Context, invocation invocation.Invocation, query ListRoleBindingsQuery) ([]*model.RoleBinding, error) {
	userID := strings.TrimSpace(query.UserID)
	scopeID := strings.TrimSpace(query.ScopeID)
	switch {
	case userID != "" && query.ScopeType == "" && scopeID == "":
		if !model.UserID(userID).IsValid() {
			return nil, apperror.New("request.invalid").WithField("field", "user_id")
		}
		visibility, err := s.deps.Authorization.AuthorizeRoleBindingList(ctx, invocation, model.ActionRoleBindingView)
		if err != nil {
			return nil, err
		}
		bindings, err := s.deps.Bindings.ListVisibleByUser(ctx, model.UserID(userID), visibility)
		if err != nil {
			return nil, roleBindingStoreErrors.Translate(err)
		}
		if bindings == nil {
			bindings = []*model.RoleBinding{}
		}
		return bindings, nil
	case userID == "" && query.ScopeType.IsValid() && model.IsValidOpaqueID(scopeID):
		if _, err := s.deps.Authorization.AuthorizeRoleBindingScope(ctx, invocation, model.ActionRoleBindingView, query.ScopeType, scopeID); err != nil {
			return nil, err
		}
		bindings, err := s.deps.Bindings.ListByScope(ctx, query.ScopeType, scopeID)
		if err != nil {
			return nil, roleBindingStoreErrors.Translate(err)
		}
		if bindings == nil {
			bindings = []*model.RoleBinding{}
		}
		return bindings, nil
	default:
		return nil, apperror.New("request.invalid").WithField("field", "scope")
	}
}

func (s *roleBindingService) CreateRoleBinding(ctx context.Context, invocation invocation.Invocation, command CreateRoleBindingCommand) (*model.RoleBinding, error) {
	userID, err := model.ParseUserID(strings.TrimSpace(command.UserID))
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	roleID, err := model.ParseRoleID(strings.TrimSpace(command.RoleID))
	if err != nil {
		return nil, apperror.New("request.invalid").WithField("field", "role_id")
	}
	scopeID := strings.TrimSpace(command.ScopeID)
	if !command.ScopeType.IsValid() || !model.IsValidOpaqueID(scopeID) {
		return nil, apperror.New("request.invalid").WithField("field", "role_binding")
	}
	if command.StartAt < 0 || (command.EndAt != 0 && command.EndAt <= command.StartAt) {
		return nil, apperror.New("request.invalid").WithField("field", "role_binding")
	}
	resource, err := s.deps.Authorization.AuthorizeRoleBindingScope(
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
		role, err = s.deps.Roles.GetIncludingArchived(ctx, candidate.RoleID)
	} else {
		role, err = s.deps.Roles.Get(ctx, candidate.RoleID)
	}
	if err != nil {
		return nil, roleBindingStoreErrors.Translate(err)
	}
	if role.Name == model.SystemAdministratorRoleName &&
		candidate.ScopeType != model.RoleScopeInstitution {
		return nil, apperror.New("role_binding.system_admin_scope_invalid")
	}
	delegatedPermissions := role.Permissions
	if role.BuiltIn && role.Name == model.SystemAdministratorRoleName {
		// Downgrade-preserved unknown permissions remain dormant and must not
		// make the protected administrator Role impossible to bind. Every
		// action recognized by this binary is still checked.
		delegatedPermissions = model.AllActions()
	}
	if err = s.deps.Authorization.AuthorizeActionDelegation(
		ctx, invocation, delegatedPermissions, candidate.ScopeType, candidate.ScopeID,
	); err != nil {
		return nil, err
	}
	mailAt := model.TimeUTC(s.deps.Now())
	recipient := &model.User{ID: userID, Revision: 1}
	var notice *store.PreparedMail
	if !command.batchRetainedOutcome {
		recipient, err = s.deps.Users.Get(ctx, userID)
		if err != nil {
			return nil, roleBindingStoreErrors.Translate(err)
		}
		key := model.MailTemplateAuthorizationScopedRoleAssigned
		if candidate.ScopeType == model.RoleScopeInstitution {
			key = model.MailTemplateAuthorizationInstitutionRoleAssigned
		}
		notice, err = s.deps.Mail.PrepareRelationshipTransition(mail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key, ActionAt: mailAt,
		})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
		}
	}
	idempotency, err := idempotency.Prepare(invocation, "role_binding.create", command.IdempotencyKey, struct {
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
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleBindingManage,
			Resource:   resource,
			Operation:  "create_binding",
			Value:      candidate.Auditable(),
		},
		func() time.Time { return mailAt },
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			input := &store.RoleBindingCreation{
				Binding: candidate, ExpectedRoleUpdatedAt: role.UpdatedAt,
				ExpectedRolePermissions:   append([]string(nil), role.Permissions...),
				ExpectedRecipientRevision: recipient.Revision, Notice: notice,
				AuditEventID: reference.ID, AuditAt: reference.MutationAt, Command: idempotency,
			}
			value, storeErr := s.deps.Bindings.Save(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		roleBindingStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	s.deps.Effects.AuthorizationChangedForUser(ctx, saved.UserID.String())
	return saved, nil
}

func (s *roleBindingService) EndRoleBinding(ctx context.Context, invocation invocation.Invocation, command EndRoleBindingCommand) (*model.RoleBinding, error) {
	id := command.ID
	if !id.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "role_binding_id")
	}
	if err := s.deps.Authorization.AuthorizeRoleBindingPreflight(ctx, invocation, model.ActionRoleBindingManage); err != nil {
		return nil, err
	}
	current, err := s.deps.Bindings.Get(ctx, id)
	if err != nil {
		return nil, roleBindingStoreErrors.Translate(err)
	}
	resource, err := s.deps.Authorization.AuthorizeRoleBindingScope(
		ctx, invocation, model.ActionRoleBindingManage, current.ScopeType, current.ScopeID,
	)
	if err != nil {
		return nil, err
	}
	if command.BatchScopeID != "" && (command.BatchScopeType != current.ScopeType || strings.TrimSpace(command.BatchScopeID) != current.ScopeID) {
		return nil, apperror.New("resource.not_found").WithField("resource", "role_binding")
	}
	idempotency, err := idempotency.Prepare(invocation, "role_binding.end", command.IdempotencyKey, struct {
		ID        string              `json:"id"`
		ScopeType model.RoleScopeType `json:"scope_type,omitempty"`
		ScopeID   string              `json:"scope_id,omitempty"`
	}{id.String(), command.BatchScopeType, strings.TrimSpace(command.BatchScopeID)})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	mailAt := model.TimeUTC(s.deps.Now())
	recipient := &model.User{ID: current.UserID, Revision: 1}
	var notice *store.PreparedMail
	if !command.batchRetainedOutcome {
		recipient, err = s.deps.Users.Get(ctx, current.UserID)
		if err != nil {
			return nil, roleBindingStoreErrors.Translate(err)
		}
		key := model.MailTemplateAuthorizationScopedRoleEnded
		if current.ScopeType == model.RoleScopeInstitution {
			key = model.MailTemplateAuthorizationInstitutionRoleEnded
		}
		notice, err = s.deps.Mail.PrepareRelationshipTransition(mail.RelationshipTransitionPreparation{
			Recipient: recipient, OccurrenceID: model.NewMailOccurrenceID(), TemplateKey: key, ActionAt: mailAt,
		})
		if err != nil {
			return nil, apperror.New("mail.unavailable").Wrap(err)
		}
	}
	ended, err := runAuditedMutation(
		ctx,
		s.deps.Audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionRoleBindingManage,
			Resource:   resource,
			Operation:  "end_binding",
			Value:      map[string]any{"role_binding_id": id},
			Prior:      current.Auditable(),
		},
		func() time.Time { return mailAt },
		func(ctx context.Context, reference mutationAttemptReference) (*model.RoleBinding, error) {
			input := &store.RoleBindingEnd{
				ID: id, EndAt: reference.MutationAt,
				ExpectedRecipientRevision: recipient.Revision, Notice: notice,
				Capabilities: accessDeploymentCapabilities(s.deps.Capabilities.Snapshot()),
				AuditEventID: reference.ID, AuditAt: reference.MutationAt, Command: idempotency,
			}
			value, storeErr := s.deps.Bindings.End(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		roleBindingStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	s.deps.Effects.AuthorizationChangedForUser(ctx, ended.UserID.String())
	return ended, nil
}

type roleBindingRealtimeEffects struct {
	effects authorizationInvalidationEffects
}

func (e roleBindingRealtimeEffects) AuthorizationChangedForUser(ctx context.Context, userID string) {
	e.effects.InvalidateAuthorization(ctx, userID)
}

var roleBindingStoreErrors = apperror.AdministrationStoreTable("role_binding", "role_binding.conflict", "role_binding.invalid", map[string]apperror.Code{
	"role_bindings_last_system_admin":              "role_binding.last_system_admin",
	"role_bindings_system_admin_institution_scope": "role_binding.system_admin_scope_invalid",
})
