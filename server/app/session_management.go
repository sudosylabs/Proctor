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
	"errors"
	"strings"
	"time"

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

// DPoPRequestProof carries the HTTP proof inputs that must be verified for a
// Desktop-bound credential. A nil value means the credential was not
// presented with the DPoP authorization scheme.
type DPoPRequestProof struct {
	Proof  string
	Method string
	Path   string
}

// RefreshSessionCommand rotates access/refresh credentials for a valid refresh token.
type RefreshSessionCommand struct {
	RefreshToken string
	DPoP         *DPoPRequestProof
}

// LogoutCommand ends the caller's current session.
type LogoutCommand struct{}

type selfSessionEffects interface {
	SessionsRevoked(context.Context, string, []string, []string)
}

type selfSessionService struct {
	sessions store.SessionStore
	effects  selfSessionEffects
	now      func() time.Time
}

func newSelfSessionService(
	sessions store.SessionStore,
	effects selfSessionEffects,
	now func() time.Time,
) (*selfSessionService, error) {
	if sessions == nil {
		return nil, errors.New("self-session store is required")
	}
	if effects == nil {
		return nil, errors.New("self-session effects are required")
	}
	if now == nil {
		return nil, errors.New("self-session clock is required")
	}
	return &selfSessionService{sessions: sessions, effects: effects, now: now}, nil
}

func (a *App) ListSessions(
	ctx context.Context,
	invocation Invocation,
	_ ListSessionsQuery,
) ([]*model.Session, error) {
	return a.selfSessions.List(ctx, invocation)
}

func (s *selfSessionService) List(
	ctx context.Context,
	invocation Invocation,
) ([]*model.Session, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return nil, invalidTokenAppError()
	}
	sessions, err := s.sessions.ListActiveByUser(
		ctx,
		principal.UserID.String(),
		s.now().UnixMilli(),
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
	return a.selfSessions.RevokeOne(ctx, invocation, command)
}

func (s *selfSessionService) RevokeOne(
	ctx context.Context,
	invocation Invocation,
	command RevokeSessionCommand,
) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if !model.IsValidId(sessionID) {
		return NewError("session.id.invalid").WithField("field", "session_id")
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		if store.IsNotFound(err) {
			return NewError("session.not_found")
		}
		return authenticationUnavailable(err)
	}
	if session.UserID != principal.UserID {
		return NewError("session.not_found")
	}

	hashes, err := s.sessions.Revoke(
		ctx,
		session.ID.String(),
		principal.UserID.String(),
		s.now().UnixMilli(),
		model.SessionRevocationUserSession,
	)
	if err != nil {
		if store.IsNotFound(err) {
			return NewError("session.not_found")
		}
		return authenticationUnavailable(err)
	}
	s.effects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
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
	return a.selfSessions.RevokeAll(ctx, invocation)
}

func (s *selfSessionService) RevokeAll(
	ctx context.Context,
	invocation Invocation,
) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	sessions, hashes, err := s.sessions.RevokeAllForUser(
		ctx,
		principal.UserID.String(),
		s.now().UnixMilli(),
		model.SessionRevocationUserAllSessions,
	)
	if err != nil {
		return authenticationUnavailable(err)
	}
	s.effects.SessionsRevoked(
		ctx,
		principal.UserID.String(),
		sessionIds(sessions),
		hashes,
	)
	return nil
}
