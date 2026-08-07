// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/app/session.go and
// server/channels/app/platform/session.go. Proctor retains application-owned
// session listing and revocation, while adapting cache invalidation to split,
// hashed access/refresh credentials and enforcing self-service ownership at
// the use-case boundary.

package app

import (
	"context"
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (a *App) GetSessions(
	ctx context.Context,
	principal model.Principal,
) ([]*model.Session, *model.AppError) {
	if !principal.IsValid() {
		return nil, invalidTokenError("GetSessions")
	}
	sessions, err := a.Store().Session().ListActiveByUser(
		ctx,
		principal.UserId,
		a.authentication.now().UnixMilli(),
	)
	if err != nil {
		return nil, internalAuthenticationError("GetSessions", err)
	}
	return sessions, nil
}

func (a *App) RevokeSession(
	ctx context.Context,
	principal model.Principal,
	sessionID string,
) *model.AppError {
	if !principal.IsValid() {
		return invalidTokenError("RevokeSession")
	}
	if !model.IsValidId(sessionID) {
		return model.NewAppError(
			"RevokeSession",
			"session.id.invalid",
			nil,
			"",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "session_id"})
	}
	session, err := a.Store().Session().Get(ctx, sessionID)
	if err != nil {
		if store.IsNotFound(err) {
			return sessionNotFoundError("RevokeSession")
		}
		return internalAuthenticationError("RevokeSession.get", err)
	}
	if session.UserId != principal.UserId {
		return sessionNotFoundError("RevokeSession")
	}

	hashes, err := a.Store().Session().Revoke(
		ctx,
		session.Id,
		principal.UserId,
		a.authentication.now().UnixMilli(),
		"user session revocation",
	)
	if err != nil {
		if store.IsNotFound(err) {
			return sessionNotFoundError("RevokeSession")
		}
		return internalAuthenticationError("RevokeSession.revoke", err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	a.authentication.deleteActivityCache(ctx, session.Id)
	a.realtime.PropagateSessionRevocation(
		ctx,
		principal.UserId,
		[]string{session.Id},
		hashes,
	)
	return nil
}

func (a *App) RevokeAllSessions(
	ctx context.Context,
	principal model.Principal,
) *model.AppError {
	if !principal.IsValid() {
		return invalidTokenError("RevokeAllSessions")
	}
	sessions, hashes, err := a.Store().Session().RevokeAllForUser(
		ctx,
		principal.UserId,
		a.authentication.now().UnixMilli(),
		"user revoked all sessions",
	)
	if err != nil {
		return internalAuthenticationError("RevokeAllSessions", err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	for _, session := range sessions {
		a.authentication.deleteActivityCache(ctx, session.Id)
	}
	a.realtime.PropagateSessionRevocation(
		ctx,
		principal.UserId,
		sessionIds(sessions),
		hashes,
	)
	return nil
}

func sessionNotFoundError(where string) *model.AppError {
	return model.NewAppError(
		where,
		"session.not_found",
		nil,
		"",
		http.StatusNotFound,
	)
}
