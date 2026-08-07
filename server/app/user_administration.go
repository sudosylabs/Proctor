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
)

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
