// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/app/user.go. Proctor keeps account
// administration separate from authentication and from academic membership.

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (a *App) ListUsers(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	options store.UserListOptions,
) ([]*model.User, *model.AppError) {
	if _, appErr := a.authorizePrincipalToSystem(
		ctx, principal, model.ActionInstitutionManage, metadata,
	); appErr != nil {
		return nil, appErr
	}
	if options.Limit == 0 {
		options.Limit = defaultAdministrationListLimit
	}
	users, err := a.Store().User().List(ctx, options)
	if err != nil {
		return nil, administrationError("ListUsers", "user", err)
	}
	return users, nil
}

func (a *App) PatchUser(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
	patch *model.UserPatch,
) (*model.User, *model.AppError) {
	resource, appErr := a.authorizePrincipalToUser(
		ctx, principal, userID, model.ActionUserManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	if patch == nil {
		return nil, invalidAdministrationRequest("PatchUser", "patch")
	}
	current, err := a.Store().User().Get(ctx, userID)
	if err != nil {
		return nil, administrationError("PatchUser.get", "user", err)
	}
	candidate := *current
	candidate.Patch(patch)
	return updateAcademicEntity(
		a, ctx, principal, metadata, model.ActionUserManage, resource,
		"PatchUser", "user", candidate.Auditable(), current.Auditable(),
		func() (*model.User, error) { return a.Store().User().Update(ctx, &candidate) },
	)
}

func (a *App) SetUserDisabled(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
	disabled bool,
) (*model.User, *model.AppError) {
	resource, appErr := a.authorizePrincipalToUser(
		ctx, principal, userID, model.ActionUserManage, metadata,
	)
	if appErr != nil {
		return nil, appErr
	}
	if principal.UserId == userID && disabled {
		return nil, invalidAdministrationRequest("SetUserDisabled", "user_id")
	}
	current, err := a.Store().User().Get(ctx, userID)
	if err != nil {
		return nil, administrationError("SetUserDisabled.get", "user", err)
	}
	now := time.Now().UnixMilli()
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, model.ActionUserManage, resource, metadata,
		"set_disabled",
		map[string]any{"user_id": userID, "disabled": disabled},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	if disabled {
		updated, sessions, hashes, err := a.Store().User().DisableAndRevokeSessions(
			ctx, userID, now, "account disabled by administrator",
		)
		if err != nil {
			return nil, a.failAdministrationMutation(
				ctx, attempt.Id, "SetUserDisabled", "user", err,
			)
		}
		a.authentication.deleteAuthenticationCache(ctx, hashes)
		for _, session := range sessions {
			a.authentication.deleteActivityCache(ctx, session.Id)
		}
		a.realtime.PropagateSessionRevocation(
			ctx,
			userID,
			sessionIds(sessions),
			hashes,
		)
		if appErr := a.completeAdministrationMutation(ctx, attempt.Id, updated); appErr != nil {
			return nil, appErr
		}
		return updated, nil
	}
	updated, err := a.Store().User().SetDisabled(ctx, userID, 0, now)
	if err != nil {
		return nil, a.failAdministrationMutation(
			ctx, attempt.Id, "SetUserDisabled", "user", err,
		)
	}
	if appErr := a.completeAdministrationMutation(ctx, attempt.Id, updated); appErr != nil {
		return nil, appErr
	}
	return updated, nil
}

func (a *App) RevokeUserSessions(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	userID string,
) *model.AppError {
	resource, appErr := a.authorizePrincipalToUser(
		ctx, principal, userID, model.ActionSessionManage, metadata,
	)
	if appErr != nil {
		return appErr
	}
	attempt, appErr := a.beginAdministrationMutation(
		ctx, principal, model.ActionSessionManage, resource, metadata,
		"revoke_sessions", map[string]any{"user_id": userID}, nil,
	)
	if appErr != nil {
		return appErr
	}
	now := time.Now().UnixMilli()
	sessions, hashes, err := a.Store().Session().RevokeAllForUser(
		ctx, userID, now, "sessions revoked by administrator",
	)
	if err != nil {
		return a.failAdministrationMutation(
			ctx, attempt.Id, "RevokeUserSessions", "session", err,
		)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	for _, session := range sessions {
		a.authentication.deleteActivityCache(ctx, session.Id)
	}
	a.realtime.PropagateSessionRevocation(
		ctx,
		userID,
		sessionIds(sessions),
		hashes,
	)
	_, appErr = a.audit.CompleteCriticalAction(
		ctx,
		attempt.Id,
		model.AuditStatusSuccess,
		"",
		map[string]any{"revoked_session_count": len(sessions)},
	)
	return appErr
}
