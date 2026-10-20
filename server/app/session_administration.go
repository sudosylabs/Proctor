// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ListUserSessionsQuery struct {
	UserID         model.UserID
	IncludeRevoked bool
}

type RevokeUserSessionCommand struct {
	UserID    model.UserID
	SessionID model.SessionID
}

type RevokeUserSessionsCommand struct {
	UserID                    model.UserID
	IdempotencyKey            string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type sessionAdministrationStore interface {
	Get(context.Context, model.SessionID) (*model.Session, error)
	ListByUser(context.Context, model.UserID) ([]*model.Session, error)
	ListActiveByUser(context.Context, model.UserID, time.Time) ([]*model.Session, error)
	RevokeWithAudit(context.Context, *store.SessionRevocation) (*store.SessionRevocationResult, error)
	RevokeAllForUserWithAudit(context.Context, *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error)
}

type sessionAdministrationAuthorizer interface {
	AuthorizeView(context.Context, Invocation, string) error
	AuthorizeManage(context.Context, Invocation, string) error
}

type sessionAdministrationUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

type sessionAdministrationService struct {
	sessions      sessionAdministrationStore
	users         sessionAdministrationUserStore
	authorization sessionAdministrationAuthorizer
	audit         mutationAuditor
	mail          sessionAdministrationMailPreparer
	effects       sessionRevocationEffects
	now           func() time.Time
}

func newSessionAdministrationService(
	sessions sessionAdministrationStore,
	users sessionAdministrationUserStore,
	authorization sessionAdministrationAuthorizer,
	audit mutationAuditor,
	mail sessionAdministrationMailPreparer,
	effects sessionRevocationEffects,
	now func() time.Time,
) *sessionAdministrationService {
	return &sessionAdministrationService{
		sessions: sessions, users: users, authorization: authorization, audit: audit, mail: mail, effects: effects, now: now,
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
	userID := query.UserID
	if !userID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeView(ctx, invocation, userID.String()); err != nil {
		return nil, err
	}
	var (
		sessions []*model.Session
		err      error
	)
	if query.IncludeRevoked {
		sessions, err = s.sessions.ListByUser(ctx, userID)
	} else {
		sessions, err = s.sessions.ListActiveByUser(ctx, userID, model.TimeUTC(s.now()))
	}
	if err != nil {
		return nil, sessionAdministrationStoreErrors.Translate(err)
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
	userID := command.UserID
	sessionID := command.SessionID
	if !userID.IsValid() {
		return apperror.New("request.invalid").WithField("field", "user_id")
	}
	if !sessionID.IsValid() {
		return apperror.New("request.invalid").WithField("field", "session_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, userID.String()); err != nil {
		return err
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil || session.UserID != userID {
		if err == nil {
			err = store.NewErrNotFound("session", sessionID.String())
		}
		return sessionAdministrationStoreErrors.Translate(err)
	}
	user, err := s.users.Get(ctx, userID)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	now := model.TimeFromMillis(s.now().UnixMilli())
	prepared, err := s.prepareRevocationNotice(user, now)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	revocations := sessionRevocationCoordinator{
		sessions: s.sessions, audit: s.audit, effects: s.effects, now: func() time.Time { return now },
	}
	return revocations.revokeOne(ctx,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionSessionManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID.String()},
			Operation:  "revoke_session",
			Value:      map[string]any{"user_id": userID, "session_id": sessionID},
			Prior:      session.Auditable(),
		},
		&store.SessionRevocation{
			SessionID: sessionID, UserID: userID,
			Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
			Reason: model.SessionRevocationAdministratorSession,
		},
		sessionAdministrationStoreErrors.Translate,
	)
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
	userID := command.UserID
	if !userID.IsValid() {
		return apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, userID.String()); err != nil {
		return err
	}
	user, err := s.users.Get(ctx, userID)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	now := model.TimeFromMillis(s.now().UnixMilli())
	idempotency, err := applicationidempotency.Prepare(invocation, "user_sessions.revoke.v1", command.IdempotencyKey, struct {
		UserID string `json:"user_id"`
	}{userID.String()})
	if err != nil {
		return err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	prepared := &preparedDirectMail{}
	if !command.batchRetainedOutcome {
		recipient := *user
		recipient.DisabledAt = model.OptionalTime{}
		prepared, err = s.prepareRevocationNotice(&recipient, now)
		if err != nil {
			return sessionAdministrationStoreErrors.Translate(err)
		}
	}
	revocations := sessionRevocationCoordinator{
		sessions: s.sessions, audit: s.audit, effects: s.effects, now: func() time.Time { return now },
	}
	return revocations.revokeAll(ctx,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionSessionManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID.String()},
			Operation:  "revoke_sessions",
			Value:      map[string]any{"user_id": userID},
		},
		&store.UserSessionsRevocation{
			UserID:     userID,
			Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
			Reason: model.SessionRevocationAdministratorAllSessions, Command: idempotency,
		},
		sessionAdministrationStoreErrors.Translate, command.batchReplayed,
	)
}

func (s *sessionAdministrationService) prepareRevocationNotice(user *model.User, at time.Time) (*preparedDirectMail, error) {
	return s.mail.PrepareSessionsRevokedByAdministrator(appmail.NoticePreparation{Recipient: user, At: at})
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

var sessionAdministrationStoreErrors = apperror.StoreTable{
	NotFound:    "session.not_found",
	Invalid:     "request.invalid",
	Unavailable: "administration.unavailable",
	Fields:      map[string]string{"resource": "session"},
}
