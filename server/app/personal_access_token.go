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

func (a *App) CreatePersonalAccessToken(
	ctx context.Context,
	invocation Invocation,
	command CreatePersonalAccessTokenCommand,
) (*model.PersonalAccessTokenCreation, error) {
	principal := invocation.Principal()
	if err := a.requireInteractiveSession(principal, true); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	settings := a.personalAccessTokens
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
		if _, err := a.Store().AcademicUnit().Get(ctx, command.AcademicUnitID); err != nil {
			return nil, personalAccessTokenFailure("academic_unit", err)
		}
	}

	rawCredential := model.NewCredentialToken()
	candidate := &model.PersonalAccessToken{
		UserId: principal.UserId, Description: command.Description,
		TokenHash: model.HashToken(rawCredential), Scopes: normalizedScopes,
		AcademicUnitId: command.AcademicUnitID, ExpiresAt: command.ExpiresAt,
	}
	parameters := map[string]any{
		"description": command.Description, "scopes": normalizedScopes,
		"academic_unit_id": command.AcademicUnitID, "expires_at": command.ExpiresAt,
	}
	resource, err := a.personalAccessTokenAuditResource(ctx)
	if err != nil {
		return nil, err
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionPersonalAccessTokenCreate, resource, invocation.RequestMetadata(),
		parameters, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	saved, err := a.Store().PersonalAccessToken().Save(
		ctx,
		candidate,
		settings.MaximumPerUser,
	)
	if err != nil {
		return nil, a.failPersonalAccessTokenMutation(ctx, attempt.Id, err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx,
		attempt.Id,
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
	_ ListPersonalAccessTokensQuery,
) ([]*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	if err := a.requireInteractiveSession(principal, false); err != nil {
		return nil, err
	}
	tokens, err := a.Store().PersonalAccessToken().ListByUser(ctx, principal.UserId)
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
	principal := invocation.Principal()
	if err := a.requireInteractiveSession(principal, false); err != nil {
		return nil, err
	}
	if !model.IsValidId(command.TokenID) {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := a.Store().PersonalAccessToken().Get(ctx, command.TokenID)
	if err != nil || current.UserId != principal.UserId {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID)
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	resource, err := a.personalAccessTokenAuditResource(ctx)
	if err != nil {
		return nil, err
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionPersonalAccessTokenRevoke, resource, invocation.RequestMetadata(),
		map[string]any{"personal_access_token_id": command.TokenID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	revoked, err := a.Store().PersonalAccessToken().Revoke(
		ctx,
		command.TokenID,
		principal.UserId,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return nil, a.failPersonalAccessTokenMutation(ctx, attempt.Id, err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx,
		attempt.Id,
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
	principal := invocation.Principal()
	action := actionPersonalAccessTokenDisable
	if !command.Disabled {
		action = actionPersonalAccessTokenEnable
	}
	if err := a.requireInteractiveSession(principal, !command.Disabled); err != nil {
		return nil, err
	}
	if !model.IsValidId(command.TokenID) {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := a.Store().PersonalAccessToken().Get(ctx, command.TokenID)
	if err != nil || current.UserId != principal.UserId {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID)
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	resource, err := a.personalAccessTokenAuditResource(ctx)
	if err != nil {
		return nil, err
	}
	attempt, appErr := a.audit.BeginCriticalAction(
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
	updated, err := a.Store().PersonalAccessToken().SetDisabled(
		ctx,
		command.TokenID,
		principal.UserId,
		command.Disabled,
		time.Now().UnixMilli(),
		a.personalAccessTokens.MaximumPerUser,
	)
	if err != nil {
		return nil, a.failPersonalAccessTokenMutation(ctx, attempt.Id, err)
	}
	if _, appErr := a.audit.CompleteCriticalAction(
		ctx,
		attempt.Id,
		model.AuditStatusSuccess,
		"",
		updated.Auditable(),
	); appErr != nil {
		return nil, appErr
	}
	return updated, nil
}

func (a *App) requireInteractiveSession(principal model.Principal, recent bool) error {
	if !principal.IsValid() ||
		principal.CredentialType != model.CredentialSessionAccess {
		return NewError("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(
		time.Now(),
		a.recentAuthenticationTTL,
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
		if !model.IsKnownAction(scope) ||
			(index > 0 && result[index-1] == scope) {
			return nil, invalidPersonalAccessTokenRequest("scopes")
		}
	}
	return result, nil
}

func (a *App) personalAccessTokenAuditResource(ctx context.Context) (model.Resource, error) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, personalAccessTokenFailure("institution", err)
	}
	return model.Resource{
		Type: model.ResourceInstitution,
		Id:   institution.ID.String(),
	}, nil
}

func (a *App) failPersonalAccessTokenMutation(ctx context.Context, auditID string, err error) error {
	mapped := personalAccessTokenFailure("personal_access_token", err)
	code := "personal_access_token.unavailable"
	if failure, ok := As(mapped); ok {
		code = failure.Code()
	}
	if _, auditErr := a.audit.CompleteCriticalAction(
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
