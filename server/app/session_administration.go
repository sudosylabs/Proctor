// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListUserSessionsQuery struct {
	UserID         string
	IncludeRevoked bool
}

type RevokeUserSessionCommand struct {
	UserID    string
	SessionID string
}

type RevokeUserSessionsCommand struct {
	UserID string
}

type sessionAdministrationStore interface {
	Get(context.Context, string) (*model.Session, error)
	ListByUser(context.Context, string) ([]*model.Session, error)
	ListActiveByUser(context.Context, string, int64) ([]*model.Session, error)
	RevokeWithAudit(context.Context, *store.SessionRevocation) (*store.SessionRevocationResult, error)
	RevokeAllForUserWithAudit(context.Context, *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error)
}

type sessionAdministrationAuthorizer interface {
	AuthorizeView(context.Context, Invocation, string) error
	AuthorizeManage(context.Context, Invocation, string) error
}

type sessionAdministrationEffects interface {
	SessionsRevoked(context.Context, string, []*model.Session, []string)
}

type sessionAdministrationService struct {
	sessions      sessionAdministrationStore
	authorization sessionAdministrationAuthorizer
	audit         mutationAuditor
	effects       sessionAdministrationEffects
	now           func() time.Time
}

func newSessionAdministrationService(
	sessions sessionAdministrationStore,
	authorization sessionAdministrationAuthorizer,
	audit mutationAuditor,
	effects sessionAdministrationEffects,
	now func() time.Time,
) *sessionAdministrationService {
	return &sessionAdministrationService{
		sessions: sessions, authorization: authorization, audit: audit, effects: effects, now: now,
	}
}

func (a *App) ListUserSessions(
	ctx context.Context,
	invocation Invocation,
	query ListUserSessionsQuery,
) ([]*model.Session, error) {
	return a.sessionAdministrations.List(ctx, invocation, query)
}

func (s *sessionAdministrationService) List(
	ctx context.Context,
	invocation Invocation,
	query ListUserSessionsQuery,
) ([]*model.Session, error) {
	userID := strings.TrimSpace(query.UserID)
	if !model.IsValidId(userID) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeView(ctx, invocation, userID); err != nil {
		return nil, err
	}
	var (
		sessions []*model.Session
		err      error
	)
	if query.IncludeRevoked {
		sessions, err = s.sessions.ListByUser(ctx, userID)
	} else {
		sessions, err = s.sessions.ListActiveByUser(ctx, userID, s.now().UnixMilli())
	}
	if err != nil {
		return nil, sessionAdministrationError(err)
	}
	if sessions == nil {
		sessions = []*model.Session{}
	}
	return sessions, nil
}

func (a *App) RevokeUserSession(
	ctx context.Context,
	invocation Invocation,
	command RevokeUserSessionCommand,
) error {
	return a.sessionAdministrations.RevokeOne(ctx, invocation, command)
}

func (s *sessionAdministrationService) RevokeOne(
	ctx context.Context,
	invocation Invocation,
	command RevokeUserSessionCommand,
) error {
	userID := strings.TrimSpace(command.UserID)
	sessionID := strings.TrimSpace(command.SessionID)
	if !model.IsValidId(userID) {
		return NewError("request.invalid").WithField("field", "user_id")
	}
	if !model.IsValidId(sessionID) {
		return NewError("request.invalid").WithField("field", "session_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, userID); err != nil {
		return err
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil || session.UserID.String() != userID {
		if err == nil {
			err = store.NewErrNotFound("session", sessionID)
		}
		return sessionAdministrationError(err)
	}
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionSessionManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID},
			Operation:  "revoke_session",
			Value:      map[string]any{"user_id": userID, "session_id": sessionID},
			Prior:      session.Auditable(),
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.SessionRevocationResult, error) {
			return s.sessions.RevokeWithAudit(ctx, &store.SessionRevocation{
				SessionID: sessionID, UserID: userID, RevokedAt: reference.At,
				Reason:       "session revoked by administrator",
				AuditEventID: reference.ID, AuditAt: reference.At,
			})
		},
		sessionAdministrationError,
	)
	if err != nil {
		return err
	}
	s.effects.SessionsRevoked(ctx, userID, []*model.Session{result.Session}, result.TokenHashes)
	return nil
}

func (a *App) RevokeUserSessions(
	ctx context.Context,
	invocation Invocation,
	command RevokeUserSessionsCommand,
) error {
	return a.sessionAdministrations.RevokeAll(ctx, invocation, command)
}

func (s *sessionAdministrationService) RevokeAll(
	ctx context.Context,
	invocation Invocation,
	command RevokeUserSessionsCommand,
) error {
	userID := strings.TrimSpace(command.UserID)
	if !model.IsValidId(userID) {
		return NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, userID); err != nil {
		return err
	}
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionSessionManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID},
			Operation:  "revoke_sessions",
			Value:      map[string]any{"user_id": userID},
		},
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.UserSessionsRevocationResult, error) {
			return s.sessions.RevokeAllForUserWithAudit(ctx, &store.UserSessionsRevocation{
				UserID: userID, RevokedAt: reference.At,
				Reason:       "sessions revoked by administrator",
				AuditEventID: reference.ID, AuditAt: reference.At,
			})
		},
		sessionAdministrationError,
	)
	if err != nil {
		return err
	}
	s.effects.SessionsRevoked(ctx, userID, result.Sessions, result.TokenHashes)
	return nil
}

type sessionAdministrationAuthorization struct {
	authorization *accessControlService
}

func (a sessionAdministrationAuthorization) AuthorizeView(
	ctx context.Context,
	invocation Invocation,
	userID string,
) error {
	return a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		model.ActionSessionView,
		model.Resource{Type: model.ResourceUser, ID: userID},
		invocation.RequestMetadata(),
	)
}

func (a sessionAdministrationAuthorization) AuthorizeManage(
	ctx context.Context,
	invocation Invocation,
	userID string,
) error {
	return a.authorization.authorizeCurrentState(
		ctx,
		invocation.Principal(),
		model.ActionSessionManage,
		model.Resource{Type: model.ResourceUser, ID: userID},
		invocation.RequestMetadata(),
	)
}

type sessionAdministrationRealtimeEffects struct {
	effects authenticationSecurityEffects
}

func (e sessionAdministrationRealtimeEffects) SessionsRevoked(
	ctx context.Context,
	userID string,
	sessions []*model.Session,
	hashes []string,
) {
	e.effects.SessionsRevoked(ctx, userID, sessionIds(sessions), hashes)
}

func sessionAdministrationError(err error) *Error {
	switch {
	case store.IsNotFound(err):
		return NewError("session.not_found").WithField("resource", "session").Wrap(err)
	default:
		var invalid *store.ErrInvalidInput
		if errors.As(err, &invalid) {
			return NewError("request.invalid").WithField("resource", "session").Wrap(err)
		}
		return NewError("administration.unavailable").WithField("resource", "session").Wrap(err)
	}
}
