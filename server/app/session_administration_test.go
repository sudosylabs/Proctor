// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type sessionAdministrationStoreFake struct {
	events          *[]string
	session         *model.Session
	list            []*model.Session
	revokeInput     *store.SessionRevocation
	revokeAllInput  *store.UserSessionsRevocation
	revokeResult    *store.SessionRevocationResult
	revokeAllResult *store.UserSessionsRevocationResult
	getErr          error
	listErr         error
	revokeErr       error
	revokeAllErr    error
	replayed        bool
	noOp            bool
	listedPage      *store.SessionPageQuery
}

func (s *sessionAdministrationStoreFake) Get(context.Context, model.SessionID) (*model.Session, error) {
	*s.events = append(*s.events, "get-session")
	return s.session, s.getErr
}

func (s *sessionAdministrationStoreFake) ListByUser(_ context.Context, page *store.SessionPageQuery) ([]*model.Session, error) {
	s.listedPage = page
	*s.events = append(*s.events, "list")
	return s.list, s.listErr
}

func (s *sessionAdministrationStoreFake) Revoke(_ context.Context, input *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	*s.events = append(*s.events, "store-revoke")
	s.revokeInput = input
	return s.revokeResult, s.revokeErr
}

func (s *sessionAdministrationStoreFake) RevokeAllForUser(_ context.Context, input *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error) {
	*s.events = append(*s.events, "store-revoke-all")
	s.revokeAllInput = input
	input.Replayed, input.NoOp = s.replayed, s.noOp
	return s.revokeAllResult, s.revokeAllErr
}

type sessionAdministrationAuthorizerFake struct{ events *[]string }

func (a *sessionAdministrationAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, _ model.Resource) error {
	event := "authorize-view"
	if action == model.ActionSessionManage {
		event = "authorize-manage"
	}
	*a.events = append(*a.events, event)
	return nil
}

type sessionAdministrationEffectsFake struct {
	events     *[]string
	userID     model.UserID
	sessionIDs []model.SessionID
	hashes     []string
}

func (e *sessionAdministrationEffectsFake) SessionsRevoked(_ context.Context, userID model.UserID, sessionIDs []model.SessionID, hashes []string) {
	*e.events = append(*e.events, "publish-revocation")
	e.userID, e.sessionIDs, e.hashes = userID, sessionIDs, hashes
}

type sessionAdministrationUserStoreFake struct {
	events *[]string
	user   *model.User
	err    error
}

func (s *sessionAdministrationUserStoreFake) Get(context.Context, model.UserID) (*model.User, error) {
	*s.events = append(*s.events, "get-user")
	return s.user, s.err
}

func sessionAdministrationTestUser(id string) *model.User {
	return &model.User{ID: model.UserID(id), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Revision: 1,
		Username: "student", Email: "student@example.edu", DisplayName: "Student", Locale: "en", Timezone: "UTC"}
}

func TestAdminSessionListAuthorizesThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{ID: model.NewSessionID(), UserID: model.UserID(userID)}
	at := time.Date(2026, 8, 12, 9, 30, 0, 123_456_789, time.FixedZone("offset", 7200))
	persistence := &sessionAdministrationStoreFake{events: &events, list: []*model.Session{session}}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events}, Mail: &securityNoticeMailerFake{events: &events}, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: func() time.Time { return at }})
	got, err := service.ListUserSessions(context.Background(), invocation.Invocation{}, ListUserSessionsQuery{UserID: model.UserID(userID), Limit: 50})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, session.ID.String(), got[0].ID.String())
	// Contract: authorization precedes the read.
	require.Equal(t, []string{"authorize-view", "list"}, events)
	require.Equal(t, 50, persistence.listedPage.Limit)
	require.True(t, persistence.listedPage.ActiveAt.Equal(model.TimeUTC(at)), "active Session listing time = %v", persistence.listedPage.ActiveAt)
	require.Same(t, time.UTC, persistence.listedPage.ActiveAt.Location(), "active Session listing time = %v", persistence.listedPage.ActiveAt)
}

func TestAdminSessionHistoryIsPaged(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewUserID()
	persistence := &sessionAdministrationStoreFake{events: &events}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID.String())}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events}, Mail: &securityNoticeMailerFake{events: &events}, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: time.Now})
	before := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	beforeID := model.NewSessionID()
	_, err := service.ListUserSessions(context.Background(), invocation.Invocation{}, ListUserSessionsQuery{
		UserID: userID, IncludeRevoked: true, Limit: 2, BeforeCreatedAt: before, BeforeID: beforeID,
	})
	require.NoError(t, err)
	require.Equal(t, &store.SessionPageQuery{UserID: userID, Limit: 2, BeforeCreatedAt: before, BeforeID: beforeID}, persistence.listedPage,
		"history includes revoked Sessions and continues after the cursor")

	for _, query := range []ListUserSessionsQuery{
		{UserID: userID, Limit: 0},
		{UserID: userID, Limit: 201},
		{UserID: userID, Limit: 10, BeforeID: beforeID},
	} {
		_, err = service.ListUserSessions(context.Background(), invocation.Invocation{}, query)
		require.True(t, apperror.Is(err, "request.invalid"), "query %#v: err = %v", query, err)
	}
}

func TestAdminSessionRevokeCommitsBeforePublishing(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{
		ID: model.NewSessionID(), UserID: model.UserID(userID),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
	}
	revoked := *session
	revoked.RevokedAt = model.OptionalTimeFromMillis(500)
	persistence := &sessionAdministrationStoreFake{
		events:  &events,
		session: session,
		revokeResult: &store.SessionRevocationResult{
			Session: &revoked, TokenHashes: []string{"hash"},
		},
	}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	require.NoError(t, service.RevokeUserSession(context.Background(), invocation.Invocation{}, RevokeUserSessionCommand{
		UserID: model.UserID(userID), SessionID: session.ID,
	}))
	require.Equal(t, session.ID, persistence.revokeInput.SessionID)
	require.NotEmpty(t, persistence.revokeInput.AuditEventID)
	require.Len(t, mailer.requests, 1, "mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	require.Equal(t, model.MailTemplateIdentitySessionsRevokedByAdmin, mailer.requests[0].TemplateKey, "mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	require.NotNil(t, persistence.revokeInput.Occurrence, "mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	require.NotNil(t, persistence.revokeInput.Delivery, "mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	require.NotNil(t, persistence.revokeInput.DeliveryJob, "mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-manage", "get-session", "get-user", "prepare-mail", "audit-begin", "store-revoke", "publish-revocation"}, events)
}

func TestAdminSessionRevokeFailurePublishesNoEffect(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{
		ID: model.NewSessionID(), UserID: model.UserID(userID),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
	}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: &sessionAdministrationStoreFake{
		events:    &events,
		session:   session,
		revokeErr: store.NewErrConflict("session", "busy", errors.New("busy")),
	}, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: &securityNoticeMailerFake{events: &events}, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: time.Now})
	err := service.RevokeUserSession(context.Background(), invocation.Invocation{}, RevokeUserSessionCommand{
		UserID: model.UserID(userID), SessionID: session.ID,
	})
	require.True(t, apperror.Is(err, "administration.unavailable"), "error = %v", err)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"authorize-manage", "get-session", "get-user", "prepare-mail", "audit-begin", "store-revoke", "audit-fail"}, events)
}

func TestAdminSessionRevokeAllCommitsBeforePublishing(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{ID: model.NewSessionID(), UserID: model.UserID(userID)}
	persistence := &sessionAdministrationStoreFake{
		events: &events,
		revokeAllResult: &store.UserSessionsRevocationResult{
			Sessions: []*model.Session{session}, TokenHashes: []string{"hash"},
		},
	}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: &securityNoticeMailerFake{events: &events}, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: func() time.Time { return time.UnixMilli(500) }})
	require.NoError(t, service.RevokeUserSessions(context.Background(), invocation.Invocation{}, RevokeUserSessionsCommand{UserID: model.UserID(userID)}))
	require.Equal(t, model.UserID(userID), persistence.revokeAllInput.UserID)
	require.NotEmpty(t, persistence.revokeAllInput.AuditEventID)
	require.NotNil(t, persistence.revokeAllInput.Occurrence)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"authorize-manage", "get-user", "prepare-mail", "audit-begin", "store-revoke-all", "publish-revocation"}, events)
}

func TestAdminSessionIdempotentNoOpPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewUserID().String()
	persistence := &sessionAdministrationStoreFake{events: &events, list: []*model.Session{},
		revokeAllResult: &store.UserSessionsRevocationResult{Sessions: []*model.Session{}, TokenHashes: []string{}}}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: time.Now})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	require.NoError(t, service.RevokeUserSessions(context.Background(), invocation, RevokeUserSessionsCommand{UserID: model.UserID(userID), IdempotencyKey: "row"}))
	require.Len(t, mailer.requests, 1, "idempotent no-op mail")
	require.NotNil(t, persistence.revokeAllInput, "idempotent no-op mail")
	require.NotNil(t, persistence.revokeAllInput.Occurrence, "idempotent no-op mail")
}

func TestAdminSessionRetainedOutcomeBypassesMailAfterNewSession(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewUserID().String()
	persistence := &sessionAdministrationStoreFake{events: &events,
		list:            []*model.Session{{ID: model.NewSessionID(), UserID: model.UserID(userID)}},
		revokeAllResult: &store.UserSessionsRevocationResult{Sessions: []*model.Session{}, TokenHashes: []string{}}}
	mailer := &securityNoticeMailerFake{events: &events, err: errors.New("mail unavailable")}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mailer, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: time.Now})
	invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	require.NoError(t, service.RevokeUserSessions(context.Background(), invocation, RevokeUserSessionsCommand{UserID: model.UserID(userID), IdempotencyKey: "row", batchRetainedOutcome: true}))
	require.Empty(t, mailer.requests, "retained mail")
	require.Equal(t, []string{"authorize-manage", "get-user", "audit-begin", "store-revoke-all"}, events)
}

func TestAdminSessionCrossUserNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID()}
	service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: &sessionAdministrationStoreFake{events: &events, session: session}, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events}, Mail: &securityNoticeMailerFake{events: &events}, Effects: &sessionAdministrationEffectsFake{events: &events}, Now: time.Now})
	err := service.RevokeUserSession(context.Background(), invocation.Invocation{}, RevokeUserSessionCommand{
		UserID: model.UserID(userID), SessionID: session.ID,
	})
	require.True(t, apperror.Is(err, "session.not_found"), "error = %v", err)
	require.Equal(t, []string{"authorize-manage", "get-session"}, events)
}

func TestAdminSessionRevocationOutcomesPreserveAuditAndBatchResult(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"changed", "empty", "replay", "attempt unavailable", "commit unavailable", "failure audit unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			now := time.UnixMilli(500)
			userID := model.NewUserID().String()
			events := []string{}
			audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewID()}
			persistence := &sessionAdministrationStoreFake{events: &events,
				revokeAllResult: &store.UserSessionsRevocationResult{Sessions: []*model.Session{
					{ID: model.NewSessionID(), UserID: model.UserID(userID)},
					{ID: model.NewSessionID(), UserID: model.UserID(userID)},
				}, TokenHashes: []string{"first-hash", "second-hash"}}}
			unchanged := false
			switch outcome {
			case "empty", "replay":
				persistence.revokeAllResult = &store.UserSessionsRevocationResult{}
				persistence.noOp, persistence.replayed = outcome == "empty", outcome == "replay"
			case "attempt unavailable":
				audit.beginErr = apperror.New("audit.unavailable")
				unchanged = true // An unattempted mutation does not replace the caller's value.
			case "commit unavailable", "failure audit unavailable":
				persistence.revokeAllErr = errors.New("atomic revocation failed")
				if outcome == "failure audit unavailable" {
					audit.failErr = apperror.New("audit.unavailable")
				}
			}
			effects := &sessionAdministrationEffectsFake{events: &events}
			service := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: persistence, Users: &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)}, Authorization: &sessionAdministrationAuthorizerFake{events: &events}, Audit: audit, Mail: &securityNoticeMailerFake{events: &events}, Effects: effects, Now: func() time.Time { return now }})
			invocation := invocation.New(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
			err := service.RevokeUserSessions(context.Background(), invocation, RevokeUserSessionsCommand{
				UserID: model.UserID(userID), IdempotencyKey: "repeat-command", batchReplayed: &unchanged,
			})
			wantCode := ""
			switch outcome {
			case "attempt unavailable":
				wantCode = "audit.unavailable"
			case "commit unavailable", "failure audit unavailable":
				// A failed failure-audit completion joins, never replaces, the commit failure.
				wantCode = "administration.unavailable"
			}
			require.False(t, wantCode == "" && err != nil, "error = %v, want %q", err, wantCode)
			require.False(t, wantCode != "" && !apperror.Is(err, wantCode), "error = %v, want %q", err, wantCode)
			require.Equal(t, outcome == "empty" || outcome == "replay" || outcome == "attempt unavailable", unchanged, "unchanged = %t for %s", unchanged, outcome)
			// Contract (guardrails 8 and 14): authorization precedes the audit, the
			// audit begins before the durable revocation, and only a committed
			// change publishes. The exact sequence also proves that no other write,
			// effect, or audit completion ran.
			want := []string{"authorize-manage", "get-user", "prepare-mail", "begin"}
			if outcome == "attempt unavailable" {
				require.Nil(t, persistence.revokeAllInput, "revoked without a durable audit attempt")
			} else {
				want = append(want, "store-revoke-all")
				input := persistence.revokeAllInput
				require.Equal(t, model.AuditEventID(audit.beginID), input.AuditEventID)
				require.True(t, model.TimeUTC(now).Equal(input.AuditAt))
				require.True(t, model.TimeUTC(now).Equal(input.RevokedAt))
				require.NotNil(t, input.Command)
				require.Equal(t, model.SessionRevocationAdministratorAllSessions, input.Reason)
			}
			if outcome == "changed" {
				want = append(want, "publish-revocation")
				require.Equal(t, model.UserID(userID), effects.userID)
				require.True(t, slices.Equal(effects.sessionIDs, sessionIDs(persistence.revokeAllResult.Sessions)))
				require.True(t, slices.Equal(effects.hashes, persistence.revokeAllResult.TokenHashes))
			}
			if outcome == "commit unavailable" || outcome == "failure audit unavailable" {
				want = append(want, "fail")
				require.Equal(t, audit.beginID, audit.failID)
				require.Equal(t, "administration.unavailable", audit.failCode)
			}
			require.Equal(t, want, events)
			require.Equal(t, model.ActionSessionManage, audit.attempt.Action)
			require.Equal(t, userID, audit.attempt.Resource.ID)
			require.Equal(t, "revoke_sessions", audit.attempt.Operation)
		})
	}
}
