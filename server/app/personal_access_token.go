// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/user.go access-token lifecycle.
// Proctor requires finite lifetimes, explicit known-action scopes, recent
// interactive authentication for creation, optional academic-unit ceilings,
// hashed persistence, and durable security auditing.

package app

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	actionPersonalAccessTokenCreate  model.Action = "personal_access_token.create"
	actionPersonalAccessTokenEnable  model.Action = "personal_access_token.enable"
	actionPersonalAccessTokenDisable model.Action = "personal_access_token.disable"
	actionPersonalAccessTokenRevoke  model.Action = "personal_access_token.revoke"
)

// CreatePersonalAccessTokenCommand creates a new PAT for the interactive caller.
type CreatePersonalAccessTokenCommand struct {
	Description    string
	Scopes         []string
	AcademicUnitID string
	ExpiresAt      int64
}

// ListPersonalAccessTokensQuery lists the caller's PATs.
type ListPersonalAccessTokensQuery struct{}

// RevokePersonalAccessTokenCommand revokes one owned PAT.
type RevokePersonalAccessTokenCommand struct {
	TokenID string
}

// SetPersonalAccessTokenDisabledCommand enables or disables an owned PAT.
type SetPersonalAccessTokenDisabledCommand struct {
	TokenID  string
	Disabled bool
}

type personalAccessTokenAuditor interface {
	Begin(
		context.Context, model.Principal, model.Action, model.Resource,
		model.RequestMetadata, map[string]any, map[string]any,
	) (string, error)
	Complete(
		context.Context, string, model.AuditStatus, string, map[string]any,
	) error
}

type personalAccessTokenAdministrationService struct {
	tokens                  store.PersonalAccessTokenStore
	academicUnits           store.AcademicUnitStore
	institutions            store.InstitutionStore
	audit                   personalAccessTokenAuditor
	policy                  PersonalAccessTokenPolicy
	recentAuthenticationTTL time.Duration
	newCredential           func() string
	now                     func() time.Time
}

func newPersonalAccessTokenAdministrationService(
	tokens store.PersonalAccessTokenStore,
	academicUnits store.AcademicUnitStore,
	institutions store.InstitutionStore,
	audit personalAccessTokenAuditor,
	policy PersonalAccessTokenPolicy,
	recentAuthenticationTTL time.Duration,
	newCredential func() string,
	now func() time.Time,
) (*personalAccessTokenAdministrationService, error) {
	if tokens == nil {
		return nil, errors.New("personal access token administration store is required")
	}
	if academicUnits == nil {
		return nil, errors.New("personal access token academic unit store is required")
	}
	if institutions == nil {
		return nil, errors.New("personal access token institution store is required")
	}
	if audit == nil {
		return nil, errors.New("personal access token audit is required")
	}
	if newCredential == nil {
		return nil, errors.New("personal access token credential generator is required")
	}
	if now == nil {
		return nil, errors.New("personal access token clock is required")
	}
	return &personalAccessTokenAdministrationService{
		tokens: tokens, academicUnits: academicUnits, institutions: institutions,
		audit: audit, policy: policy, recentAuthenticationTTL: recentAuthenticationTTL,
		newCredential: newCredential, now: now,
	}, nil
}

func (a *App) CreatePersonalAccessToken(
	ctx context.Context,
	invocation Invocation,
	command CreatePersonalAccessTokenCommand,
) (*model.PersonalAccessTokenCreation, error) {
	return a.personalAccessTokenAdministration.Create(ctx, invocation, command)
}

func (s *personalAccessTokenAdministrationService) Create(
	ctx context.Context,
	invocation Invocation,
	command CreatePersonalAccessTokenCommand,
) (*model.PersonalAccessTokenCreation, error) {
	principal := invocation.Principal()
	at := s.now()
	if err := requireInteractiveSession(
		principal, true, at, s.recentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	now := at.UnixMilli()
	settings := s.policy
	if command.ExpiresAt < now+settings.MinimumLifetime.Milliseconds() ||
		command.ExpiresAt > now+settings.MaximumLifetime.Milliseconds() {
		return nil, invalidPersonalAccessTokenRequest("expires_at")
	}
	normalizedScopes, err := normalizePersonalAccessTokenScopes(command.Scopes)
	if err != nil {
		return nil, err
	}
	if command.AcademicUnitID != "" {
		if !model.IsValidId(command.AcademicUnitID) {
			return nil, invalidPersonalAccessTokenRequest("academic_unit_id")
		}
		if _, err := s.academicUnits.Get(ctx, command.AcademicUnitID); err != nil {
			return nil, personalAccessTokenFailure("academic_unit", err)
		}
	}

	rawCredential := s.newCredential()
	candidate := &model.PersonalAccessToken{
		UserID:         principal.UserID,
		Description:    command.Description,
		TokenHash:      model.HashToken(rawCredential),
		Scopes:         normalizedScopes,
		AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID),
		ExpiresAt:      model.TimeFromMillis(command.ExpiresAt),
	}
	parameters := map[string]any{
		"description": command.Description, "scopes": normalizedScopes,
		"academic_unit_id": command.AcademicUnitID, "expires_at": command.ExpiresAt,
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditID, appErr := s.audit.Begin(
		ctx, principal, actionPersonalAccessTokenCreate, resource, invocation.RequestMetadata(),
		parameters, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := s.tokens.Save(
		ctx,
		candidate,
		settings.MaximumPerUser,
	)
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	if appErr := s.audit.Complete(
		ctx,
		auditID,
		model.AuditStatusSuccess,
		"",
		saved.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return &model.PersonalAccessTokenCreation{
		Token: saved, Credential: rawCredential,
	}, nil
}

func (a *App) ListPersonalAccessTokens(
	ctx context.Context,
	invocation Invocation,
	query ListPersonalAccessTokensQuery,
) ([]*model.PersonalAccessToken, error) {
	return a.personalAccessTokenAdministration.List(ctx, invocation, query)
}

func (s *personalAccessTokenAdministrationService) List(
	ctx context.Context,
	invocation Invocation,
	_ ListPersonalAccessTokensQuery,
) ([]*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.now(), s.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	tokens, err := s.tokens.ListByUser(ctx, principal.UserID.String())
	if err != nil {
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	return tokens, nil
}

func (a *App) RevokePersonalAccessToken(
	ctx context.Context,
	invocation Invocation,
	command RevokePersonalAccessTokenCommand,
) (*model.PersonalAccessToken, error) {
	return a.personalAccessTokenAdministration.Revoke(ctx, invocation, command)
}

func (s *personalAccessTokenAdministrationService) Revoke(
	ctx context.Context,
	invocation Invocation,
	command RevokePersonalAccessTokenCommand,
) (*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.now(), s.recentAuthenticationTTL); err != nil {
		return nil, err
	}
	if !model.IsValidId(command.TokenID) {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := s.tokens.Get(ctx, command.TokenID)
	if err != nil || current.UserID != principal.UserID {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID)
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditID, appErr := s.audit.Begin(
		ctx, principal, actionPersonalAccessTokenRevoke, resource, invocation.RequestMetadata(),
		map[string]any{"personal_access_token_id": command.TokenID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	revoked, err := s.tokens.Revoke(
		ctx,
		command.TokenID,
		principal.UserID.String(),
		s.now().UnixMilli(),
	)
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	if appErr := s.audit.Complete(
		ctx,
		auditID,
		model.AuditStatusSuccess,
		"",
		revoked.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return revoked, nil
}

func (a *App) SetPersonalAccessTokenDisabled(
	ctx context.Context,
	invocation Invocation,
	command SetPersonalAccessTokenDisabledCommand,
) (*model.PersonalAccessToken, error) {
	return a.personalAccessTokenAdministration.SetDisabled(ctx, invocation, command)
}

func (s *personalAccessTokenAdministrationService) SetDisabled(
	ctx context.Context,
	invocation Invocation,
	command SetPersonalAccessTokenDisabledCommand,
) (*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	at := s.now()
	action := actionPersonalAccessTokenDisable
	if !command.Disabled {
		action = actionPersonalAccessTokenEnable
	}
	if err := requireInteractiveSession(
		principal, !command.Disabled, at, s.recentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	if !model.IsValidId(command.TokenID) {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := s.tokens.Get(ctx, command.TokenID)
	if err != nil || current.UserID != principal.UserID {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID)
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditID, appErr := s.audit.Begin(
		ctx, principal, action, resource, invocation.RequestMetadata(),
		map[string]any{
			"personal_access_token_id": command.TokenID,
			"disabled":                 command.Disabled,
		},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	updated, err := s.tokens.SetDisabled(
		ctx,
		command.TokenID,
		principal.UserID.String(),
		command.Disabled,
		at.UnixMilli(),
		s.policy.MaximumPerUser,
	)
	if err != nil {
		return nil, s.failMutation(ctx, auditID, err)
	}
	if appErr := s.audit.Complete(
		ctx,
		auditID,
		model.AuditStatusSuccess,
		"",
		updated.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return updated, nil
}

func (a *App) requireInteractiveSession(principal model.Principal, recent bool) error {
	return requireInteractiveSession(
		principal, recent, time.Now(), a.recentAuthenticationTTL,
	)
}

func requireInteractiveSession(
	principal model.Principal,
	recent bool,
	now time.Time,
	recentAuthenticationTTL time.Duration,
) error {
	if principal.Validate() != nil ||
		principal.CredentialType != model.CredentialSessionAccess {
		return NewError("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(
		now,
		recentAuthenticationTTL,
	) {
		return NewError("authentication.reauthentication_required")
	}
	return nil
}

func normalizePersonalAccessTokenScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > model.PersonalAccessTokenScopeMaxCount {
		return nil, invalidPersonalAccessTokenRequest("scopes")
	}
	result := append([]string(nil), scopes...)
	sort.Strings(result)
	for index, scope := range result {
		if !model.IsGrantableAction(scope) ||
			(index > 0 && result[index-1] == scope) {
			return nil, invalidPersonalAccessTokenRequest("scopes")
		}
	}
	return result, nil
}

func (s *personalAccessTokenAdministrationService) auditResource(
	ctx context.Context,
) (model.Resource, error) {
	institution, err := s.institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, personalAccessTokenFailure("institution", err)
	}
	return model.Resource{
		Type: model.ResourceInstitution,
		ID:   institution.ID.String(),
	}, nil
}

func (s *personalAccessTokenAdministrationService) failMutation(
	ctx context.Context,
	auditID string,
	err error,
) error {
	mapped := personalAccessTokenFailure("personal_access_token", err)
	code := "personal_access_token.unavailable"
	if failure, ok := As(mapped); ok {
		code = failure.Code()
	}
	if auditErr := s.audit.Complete(
		ctx,
		auditID,
		model.AuditStatusFail,
		code,
		nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

type personalAccessTokenAuditAdapter struct {
	audit *auditService
}

func (a personalAccessTokenAuditAdapter) Begin(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
	parameters map[string]any,
	prior map[string]any,
) (string, error) {
	event, err := a.audit.BeginCriticalAction(
		ctx, principal, action, resource, metadata, parameters, prior,
	)
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (a personalAccessTokenAuditAdapter) Complete(
	ctx context.Context,
	auditID string,
	status model.AuditStatus,
	errorCode string,
	result map[string]any,
) error {
	_, err := a.audit.CompleteCriticalAction(
		ctx, auditID, status, errorCode, result,
	)
	return err
}

func invalidPersonalAccessTokenRequest(field string) error {
	return NewError("personal_access_token.invalid").WithField("field", field)
}

func personalAccessTokenFailure(resource string, err error) error {
	code := "personal_access_token.unavailable"
	switch {
	case store.IsNotFound(err):
		code = "resource.not_found"
	case store.IsConflict(err):
		code = "personal_access_token.maximum_reached"
	}
	return NewError(code).WithField("resource", resource).Wrap(err)
}
