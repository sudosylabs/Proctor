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
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ListSessionsQuery lists the caller's active sessions.
type ListSessionsQuery struct{}

// RevokeSessionCommand revokes one of the caller's sessions by ID.
type RevokeSessionCommand struct {
	SessionID string
}

// RevokeAllSessionsCommand revokes every active session for the caller.
type RevokeAllSessionsCommand struct{}

// RefreshSessionCommand rotates access/refresh credentials for a valid refresh token.
type RefreshSessionCommand struct {
	RefreshToken string
}

// LogoutCommand ends the caller's current session.
type LogoutCommand struct{}

func (a *App) ListSessions(
	ctx context.Context,
	invocation Invocation,
	_ ListSessionsQuery,
) ([]*model.Session, error) {
	principal := invocation.Principal()
	if !principal.IsValid() {
		return nil, invalidTokenAppError()
	}
	sessions, err := a.Store().Session().ListActiveByUser(
		ctx,
		principal.UserId,
		a.authentication.now().UnixMilli(),
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	return sessions, nil
}

func (a *App) RevokeSession(
	ctx context.Context,
	invocation Invocation,
	command RevokeSessionCommand,
) error {
	principal := invocation.Principal()
	if !principal.IsValid() {
		return invalidTokenAppError()
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if !model.IsValidId(sessionID) {
		return NewError("session.id.invalid").WithField("field", "session_id")
	}
	session, err := a.Store().Session().Get(ctx, sessionID)
	if err != nil {
		if store.IsNotFound(err) {
			return NewError("session.not_found")
		}
		return authenticationUnavailable(err)
	}
	if session.UserID.String() != principal.UserId {
		return NewError("session.not_found")
	}

	hashes, err := a.Store().Session().Revoke(
		ctx,
		session.ID.String(),
		principal.UserId,
		a.authentication.now().UnixMilli(),
		"user session revocation",
	)
	if err != nil {
		if store.IsNotFound(err) {
			return NewError("session.not_found")
		}
		return authenticationUnavailable(err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	a.authentication.deleteActivityCache(ctx, session.ID.String())
	a.realtime.PropagateSessionRevocation(
		ctx,
		principal.UserId,
		[]string{session.ID.String()},
		hashes,
	)
	return nil
}

func (a *App) RevokeAllSessions(
	ctx context.Context,
	invocation Invocation,
	_ RevokeAllSessionsCommand,
) error {
	principal := invocation.Principal()
	if !principal.IsValid() {
		return invalidTokenAppError()
	}
	sessions, hashes, err := a.Store().Session().RevokeAllForUser(
		ctx,
		principal.UserId,
		a.authentication.now().UnixMilli(),
		"user revoked all sessions",
	)
	if err != nil {
		return authenticationUnavailable(err)
	}
	a.authentication.deleteAuthenticationCache(ctx, hashes)
	for _, session := range sessions {
		a.authentication.deleteActivityCache(ctx, session.ID.String())
	}
	a.realtime.PropagateSessionRevocation(
		ctx,
		principal.UserId,
		sessionIds(sessions),
		hashes,
	)
	return nil
}

