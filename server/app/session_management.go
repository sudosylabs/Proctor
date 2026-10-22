// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

type selfSessionService struct {
	sessions store.SessionStore
	audit    mutationAuditor
	effects  sessionRevocationEffects
	now      func() time.Time
}

func newSelfSessionService(
	sessions store.SessionStore,
	audit mutationAuditor,
	effects sessionRevocationEffects,
	now func() time.Time,
) (*selfSessionService, error) {
	if sessions == nil {
		return nil, errors.New("self-session store is required")
	}
	if audit == nil {
		return nil, errors.New("self-session audit is required")
	}
	if effects == nil {
		return nil, errors.New("self-session effects are required")
	}
	if now == nil {
		return nil, errors.New("self-session clock is required")
	}
	return &selfSessionService{sessions: sessions, audit: audit, effects: effects, now: now}, nil
}

func (s *selfSessionService) ListSessions(
	ctx context.Context,
	invocation invocation.Invocation,
	_ ListSessionsQuery,
) ([]*model.Session, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return nil, invalidTokenAppError()
	}
	sessions, err := s.sessions.ListActiveByUser(
		ctx,
		principal.UserID,
		model.TimeUTC(s.now()),
	)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	return sessions, nil
}

func (s *selfSessionService) RevokeSession(
	ctx context.Context,
	invocation invocation.Invocation,
	command RevokeSessionCommand,
) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if !model.SessionID(sessionID).IsValid() {
		return apperror.New("session.id.invalid").WithField("field", "session_id")
	}
	session, err := s.sessions.Get(ctx, model.SessionID(sessionID))
	if err != nil {
		if store.IsNotFound(err) {
			return apperror.New("session.not_found")
		}
		return authenticationUnavailable(err)
	}
	if session.UserID != principal.UserID {
		return apperror.New("session.not_found")
	}

	revocations := sessionRevocationCoordinator{sessions: s.sessions, audit: s.audit, effects: s.effects, now: s.now}
	return revocations.revokeOne(ctx,
		mutationAttempt{
			Invocation: invocation, Action: model.ActionSessionManage,
			Resource:  model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
			Operation: "revoke_own_session",
			Value:     map[string]any{"session_id": session.ID.String()},
		},
		&store.SessionRevocation{
			SessionID: session.ID, UserID: principal.UserID,
			Reason: model.SessionRevocationUserSession,
		},
		selfSessionRevocationStoreErrors.Translate,
	)
}

func (s *selfSessionService) RevokeAllSessions(
	ctx context.Context,
	invocation invocation.Invocation,
	_ RevokeAllSessionsCommand,
) error {
	principal := invocation.Principal()
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	revocations := sessionRevocationCoordinator{sessions: s.sessions, audit: s.audit, effects: s.effects, now: s.now}
	return revocations.revokeAll(ctx,
		mutationAttempt{
			Invocation: invocation, Action: model.ActionSessionManage,
			Resource:  model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
			Operation: "revoke_own_sessions",
		},
		&store.UserSessionsRevocation{
			UserID: principal.UserID, Reason: model.SessionRevocationUserAllSessions,
		},
		authenticationUnavailable, nil,
	)
}

var selfSessionRevocationStoreErrors = apperror.StoreTable{
	NotFound:    "session.not_found",
	Unavailable: "authentication.internal",
}
