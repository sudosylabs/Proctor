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
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ListUserSessionsQuery pages one User's Sessions newest first. Limit is
// between 1 and 200; a continuation names the last Session of the previous
// page by its creation instant and ID.
type ListUserSessionsQuery struct {
	UserID          model.UserID
	IncludeRevoked  bool
	Limit           int
	BeforeCreatedAt time.Time
	BeforeID        model.SessionID
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
	ListByUser(context.Context, *store.SessionPageQuery) ([]*model.Session, error)
	Revoke(context.Context, *store.SessionRevocation) (*store.SessionRevocationResult, error)
	RevokeAllForUser(context.Context, *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error)
}

type sessionAdministrationUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

// sessionAdministrationServiceDependencies are the collaborators of sessionAdministrationService;
// newSessionAdministrationService validates them once and sessionAdministrationService keeps them in deps.
type sessionAdministrationServiceDependencies struct {
	Sessions      sessionAdministrationStore
	Users         sessionAdministrationUserStore
	Authorization resourceAuthorizer
	Audit         mutationAuditor
	Mail          sessionAdministrationMailPreparer
	Effects       sessionRevocationEffects
	Now           func() time.Time
}

type sessionAdministrationService struct {
	deps sessionAdministrationServiceDependencies
}

func newSessionAdministrationService(deps sessionAdministrationServiceDependencies) *sessionAdministrationService {
	return &sessionAdministrationService{deps: deps}
}

func (s *sessionAdministrationService) ListUserSessions(
	ctx context.Context,
	invocation invocation.Invocation,
	query ListUserSessionsQuery,
) (Page[*model.Session], error) {
	userID := query.UserID
	if !userID.IsValid() {
		return Page[*model.Session]{}, apperror.New("request.invalid").WithField("field", "user_id")
	}
	if query.Limit < 1 || query.Limit > MaximumPageLimit {
		return Page[*model.Session]{}, apperror.Invalid("request.invalid", "limit")
	}
	if query.BeforeCreatedAt.IsZero() != query.BeforeID.IsZero() ||
		(!query.BeforeID.IsZero() && !query.BeforeID.IsValid()) {
		return Page[*model.Session]{}, apperror.Invalid("request.invalid", "cursor")
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionSessionView, model.Resource{Type: model.ResourceUser, ID: userID.String()}); err != nil {
		return Page[*model.Session]{}, err
	}
	page := &store.SessionPageQuery{
		UserID: userID, Limit: query.Limit + 1,
		BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID,
	}
	if !query.IncludeRevoked {
		page.ActiveAt = model.TimeUTC(s.deps.Now())
	}
	sessions, err := s.deps.Sessions.ListByUser(ctx, page)
	if err != nil {
		return Page[*model.Session]{}, sessionAdministrationStoreErrors.Translate(err)
	}
	return newPage(sessions, query.Limit), nil
}

func (s *sessionAdministrationService) RevokeUserSession(
	ctx context.Context,
	invocation invocation.Invocation,
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
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionSessionManage, model.Resource{Type: model.ResourceUser, ID: userID.String()}); err != nil {
		return err
	}
	session, err := s.deps.Sessions.Get(ctx, sessionID)
	if err != nil || session.UserID != userID {
		if err == nil {
			err = store.NewErrNotFound("session", sessionID.String())
		}
		return sessionAdministrationStoreErrors.Translate(err)
	}
	user, err := s.deps.Users.Get(ctx, userID)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	now := model.TimeUTC(s.deps.Now())
	prepared, err := s.prepareRevocationNotice(user, now)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	revocations := sessionRevocationCoordinator{
		sessions: s.deps.Sessions, audit: s.deps.Audit, effects: s.deps.Effects, now: func() time.Time { return now },
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

func (s *sessionAdministrationService) RevokeUserSessions(
	ctx context.Context,
	invocation invocation.Invocation,
	command RevokeUserSessionsCommand,
) error {
	userID := command.UserID
	if !userID.IsValid() {
		return apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.deps.Authorization.Authorize(ctx, invocation, model.ActionSessionManage, model.Resource{Type: model.ResourceUser, ID: userID.String()}); err != nil {
		return err
	}
	user, err := s.deps.Users.Get(ctx, userID)
	if err != nil {
		return sessionAdministrationStoreErrors.Translate(err)
	}
	now := model.TimeUTC(s.deps.Now())
	idempotency, err := idempotency.Prepare(invocation, "user_sessions.revoke", command.IdempotencyKey, struct {
		UserID string `json:"user_id"`
	}{userID.String()})
	if err != nil {
		return err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	prepared := &store.PreparedMail{}
	if !command.batchRetainedOutcome {
		recipient := *user
		recipient.DisabledAt = model.OptionalTime{}
		prepared, err = s.prepareRevocationNotice(&recipient, now)
		if err != nil {
			return sessionAdministrationStoreErrors.Translate(err)
		}
	}
	revocations := sessionRevocationCoordinator{
		sessions: s.deps.Sessions, audit: s.deps.Audit, effects: s.deps.Effects, now: func() time.Time { return now },
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

func (s *sessionAdministrationService) prepareRevocationNotice(user *model.User, at time.Time) (*store.PreparedMail, error) {
	return s.deps.Mail.PrepareSessionsRevokedByAdministrator(mail.NoticePreparation{Recipient: user, At: at})
}

var sessionAdministrationStoreErrors = apperror.StoreTable{
	NotFound:    "session.not_found",
	Invalid:     "request.invalid",
	Unavailable: "administration.unavailable",
	Fields:      map[string]string{"resource": "session"},
}
