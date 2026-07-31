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
	"net/http"
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

func (a *App) CreatePersonalAccessToken(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	description string,
	scopes []string,
	academicUnitID string,
	expiresAt int64,
) (*model.PersonalAccessTokenCreation, *model.AppError) {
	if appErr := a.requireInteractiveSession(
		principal,
		true,
		"CreatePersonalAccessToken",
	); appErr != nil {
		return nil, appErr
	}
	now := time.Now().UnixMilli()
	settings := a.Config().Authentication.PersonalAccessTokens
	if expiresAt < now+settings.MinimumLifetime.Milliseconds() ||
		expiresAt > now+settings.MaximumLifetime.Milliseconds() {
		return nil, invalidPersonalAccessTokenRequest(
			"CreatePersonalAccessToken",
			"expires_at",
		)
	}
	normalizedScopes, appErr := normalizePersonalAccessTokenScopes(scopes)
	if appErr != nil {
		return nil, appErr
	}
	if academicUnitID != "" {
		if !model.IsValidId(academicUnitID) {
			return nil, invalidPersonalAccessTokenRequest(
				"CreatePersonalAccessToken",
				"academic_unit_id",
			)
		}
		if _, err := a.Store().AcademicUnit().Get(ctx, academicUnitID); err != nil {
			return nil, personalAccessTokenError(
				"CreatePersonalAccessToken.academic_unit",
				"academic_unit",
				err,
			)
		}
	}

	rawCredential := model.NewCredentialToken()
	candidate := &model.PersonalAccessToken{
		UserId: principal.UserId, Description: description,
		TokenHash: model.HashToken(rawCredential), Scopes: normalizedScopes,
		AcademicUnitId: academicUnitID, ExpiresAt: expiresAt,
	}
	// PreSave is performed on a clone by the store. Build a safe preview for
	// the attempt audit without ever including the raw credential or hash.
	parameters := map[string]any{
		"description": description, "scopes": normalizedScopes,
		"academic_unit_id": academicUnitID, "expires_at": expiresAt,
	}
	resource, appErr := a.personalAccessTokenAuditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionPersonalAccessTokenCreate, resource, metadata,
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
		return nil, a.failPersonalAccessTokenMutation(
			ctx,
			attempt.Id,
			"CreatePersonalAccessToken.save",
			err,
		)
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
	principal model.Principal,
) ([]*model.PersonalAccessToken, *model.AppError) {
	if appErr := a.requireInteractiveSession(
		principal,
		false,
		"ListPersonalAccessTokens",
	); appErr != nil {
		return nil, appErr
	}
	tokens, err := a.Store().PersonalAccessToken().ListByUser(ctx, principal.UserId)
	if err != nil {
		return nil, personalAccessTokenError(
			"ListPersonalAccessTokens",
			"personal_access_token",
			err,
		)
	}
	return tokens, nil
}

func (a *App) RevokePersonalAccessToken(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	tokenID string,
) (*model.PersonalAccessToken, *model.AppError) {
	if appErr := a.requireInteractiveSession(
		principal,
		false,
		"RevokePersonalAccessToken",
	); appErr != nil {
		return nil, appErr
	}
	if !model.IsValidId(tokenID) {
		return nil, invalidPersonalAccessTokenRequest(
			"RevokePersonalAccessToken",
			"personal_access_token_id",
		)
	}
	current, err := a.Store().PersonalAccessToken().Get(ctx, tokenID)
	if err != nil || current.UserId != principal.UserId {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", tokenID)
		}
		return nil, personalAccessTokenError(
			"RevokePersonalAccessToken.get",
			"personal_access_token",
			err,
		)
	}
	resource, appErr := a.personalAccessTokenAuditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, actionPersonalAccessTokenRevoke, resource, metadata,
		map[string]any{"personal_access_token_id": tokenID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	revoked, err := a.Store().PersonalAccessToken().Revoke(
		ctx,
		tokenID,
		principal.UserId,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return nil, a.failPersonalAccessTokenMutation(
			ctx,
			attempt.Id,
			"RevokePersonalAccessToken.revoke",
			err,
		)
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
	principal model.Principal,
	metadata model.RequestMetadata,
	tokenID string,
	disabled bool,
) (*model.PersonalAccessToken, *model.AppError) {
	where := "DisablePersonalAccessToken"
	action := actionPersonalAccessTokenDisable
	if !disabled {
		where = "EnablePersonalAccessToken"
		action = actionPersonalAccessTokenEnable
	}
	if appErr := a.requireInteractiveSession(
		principal,
		!disabled,
		where,
	); appErr != nil {
		return nil, appErr
	}
	if !model.IsValidId(tokenID) {
		return nil, invalidPersonalAccessTokenRequest(
			where,
			"personal_access_token_id",
		)
	}
	current, err := a.Store().PersonalAccessToken().Get(ctx, tokenID)
	if err != nil || current.UserId != principal.UserId {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", tokenID)
		}
		return nil, personalAccessTokenError(
			where+".get",
			"personal_access_token",
			err,
		)
	}
	resource, appErr := a.personalAccessTokenAuditResource(ctx)
	if appErr != nil {
		return nil, appErr
	}
	attempt, appErr := a.audit.BeginCriticalAction(
		ctx, principal, action, resource, metadata,
		map[string]any{
			"personal_access_token_id": tokenID,
			"disabled":                 disabled,
		},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	updated, err := a.Store().PersonalAccessToken().SetDisabled(
		ctx,
		tokenID,
		principal.UserId,
		disabled,
		time.Now().UnixMilli(),
		a.Config().Authentication.PersonalAccessTokens.MaximumPerUser,
	)
	if err != nil {
		return nil, a.failPersonalAccessTokenMutation(
			ctx,
			attempt.Id,
			where+".set_disabled",
			err,
		)
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

func (a *App) requireInteractiveSession(
	principal model.Principal,
	recent bool,
	where string,
) *model.AppError {
	if !principal.IsValid() ||
		principal.CredentialType != model.CredentialSessionAccess {
		return model.NewAppError(
			where,
			"authentication.session_required",
			nil,
			"",
			http.StatusUnauthorized,
		)
	}
	if recent && !principal.IsRecentlyAuthenticated(
		time.Now(),
		a.Config().Authentication.RecentAuthenticationTTL.Duration,
	) {
		return model.NewAppError(
			where,
			"authentication.reauthentication_required",
			nil,
			"",
			http.StatusForbidden,
		)
	}
	return nil
}

func normalizePersonalAccessTokenScopes(
	scopes []string,
) ([]string, *model.AppError) {
	if len(scopes) == 0 || len(scopes) > model.PersonalAccessTokenScopeMaxCount {
		return nil, invalidPersonalAccessTokenRequest(
			"CreatePersonalAccessToken",
			"scopes",
		)
	}
	result := append([]string(nil), scopes...)
	sort.Strings(result)
	for index, scope := range result {
		if !model.IsKnownAction(scope) ||
			(index > 0 && result[index-1] == scope) {
			return nil, invalidPersonalAccessTokenRequest(
				"CreatePersonalAccessToken",
				"scopes",
			)
		}
	}
	return result, nil
}

func (a *App) personalAccessTokenAuditResource(
	ctx context.Context,
) (model.Resource, *model.AppError) {
	institution, err := a.Store().Institution().GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, personalAccessTokenError(
			"PersonalAccessToken.audit_resource",
			"institution",
			err,
		)
	}
	return model.Resource{
		Type: model.ResourceInstitution,
		Id:   institution.Id,
	}, nil
}

func (a *App) failPersonalAccessTokenMutation(
	ctx context.Context,
	auditID string,
	where string,
	err error,
) *model.AppError {
	mapped := personalAccessTokenError(where, "personal_access_token", err)
	if _, auditErr := a.audit.CompleteCriticalAction(
		ctx,
		auditID,
		model.AuditStatusFail,
		mapped.ErrorCode(),
		nil,
	); auditErr != nil {
		return auditErr
	}
	return mapped
}

func invalidPersonalAccessTokenRequest(
	where string,
	field string,
) *model.AppError {
	return model.NewAppError(
		where,
		"personal_access_token.invalid",
		nil,
		"",
		http.StatusBadRequest,
	).WithSafeFields(map[string]string{"field": field})
}

func personalAccessTokenError(
	where string,
	resource string,
	err error,
) *model.AppError {
	var appErr *model.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	status, code := http.StatusInternalServerError, "personal_access_token.unavailable"
	switch {
	case store.IsNotFound(err):
		status, code = http.StatusNotFound, "resource.not_found"
	case store.IsConflict(err):
		status, code = http.StatusConflict, "personal_access_token.maximum_reached"
	}
	return model.NewAppError(
		where,
		code,
		nil,
		"",
		status,
	).WithSafeFields(map[string]string{"resource": resource}).Wrap(err)
}
