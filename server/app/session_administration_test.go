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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
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
	listedAt        time.Time
}

func (s *sessionAdministrationStoreFake) Get(context.Context, model.SessionID) (*model.Session, error) {
	*s.events = append(*s.events, "get-session")
	return s.session, s.getErr
}

func (s *sessionAdministrationStoreFake) ListByUser(context.Context, model.UserID) ([]*model.Session, error) {
	*s.events = append(*s.events, "list-all")
	return s.list, s.listErr
}

func (s *sessionAdministrationStoreFake) ListActiveByUser(_ context.Context, _ model.UserID, at time.Time) ([]*model.Session, error) {
	s.listedAt = at
	*s.events = append(*s.events, "list-active")
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

func (a *sessionAdministrationAuthorizerFake) Authorize(_ context.Context, _ Invocation, action model.Action, _ model.Resource) error {
	event := "authorize-view"
	if action == model.ActionSessionManage {
		event = "authorize-manage"
	}
	*a.events = append(*a.events, event)
	return nil
}

type sessionAdministrationEffectsFake struct {
	events     *[]string
	userID     string
	sessionIDs []string
	hashes     []string
}

func (e *sessionAdministrationEffectsFake) SessionsRevoked(_ context.Context, userID string, sessionIDs, hashes []string) {
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
	service := newSessionAdministrationService(
		persistence,
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return at },
	)
	got, err := service.ListUserSessions(context.Background(), Invocation{}, ListUserSessionsQuery{UserID: model.UserID(userID)})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, session.ID.String(), got[0].ID.String())
	// Contract: authorization precedes the read.
	ordertest.RequireInOrder(t, events, "authorize-view", "list-active")
	require.True(t, persistence.listedAt.Equal(model.TimeUTC(at)), "active Session listing time = %v", persistence.listedAt)
	require.Same(t, time.UTC, persistence.listedAt.Location(), "active Session listing time = %v", persistence.listedAt)
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
	service := newSessionAdministrationService(
		persistence,
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events, beginID: model.NewID()},
		mailer,
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	require.NoError(t, service.RevokeUserSession(context.Background(), Invocation{}, RevokeUserSessionCommand{
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
	// Lookups may move freely.
	ordertest.RequireInOrder(t, events, "authorize-manage", "audit-begin", "store-revoke", "publish-revocation")
}

func TestAdminSessionRevokeFailurePublishesNoEffect(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{
		ID: model.NewSessionID(), UserID: model.UserID(userID),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
	}
	service := newSessionAdministrationService(
		&sessionAdministrationStoreFake{
			events:    &events,
			session:   session,
			revokeErr: store.NewErrConflict("session", "busy", errors.New("busy")),
		},
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events, beginID: model.NewID()},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		time.Now,
	)
	err := service.RevokeUserSession(context.Background(), Invocation{}, RevokeUserSessionCommand{
		UserID: model.UserID(userID), SessionID: session.ID,
	})
	require.True(t, apperror.Is(err, "administration.unavailable"), "error = %v", err)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write. Lookups
	// may move freely.
	ordertest.RequireInOrder(t, events, "authorize-manage", "audit-begin", "store-revoke", "audit-fail")
	require.NotContains(t, events, "publish-revocation", "a failed revocation was published")
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
	service := newSessionAdministrationService(
		persistence,
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events, beginID: model.NewID()},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	require.NoError(t, service.RevokeUserSessions(context.Background(), Invocation{}, RevokeUserSessionsCommand{UserID: model.UserID(userID)}))
	require.Equal(t, model.UserID(userID), persistence.revokeAllInput.UserID)
	require.NotEmpty(t, persistence.revokeAllInput.AuditEventID)
	require.NotNil(t, persistence.revokeAllInput.Occurrence)
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	// Lookups may move freely.
	ordertest.RequireInOrder(t, events, "authorize-manage", "audit-begin", "store-revoke-all", "publish-revocation")
}

func TestAdminSessionIdempotentNoOpPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewUserID().String()
	persistence := &sessionAdministrationStoreFake{events: &events, list: []*model.Session{},
		revokeAllResult: &store.UserSessionsRevocationResult{Sessions: []*model.Session{}, TokenHashes: []string{}}}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newSessionAdministrationService(persistence, &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, mailer,
		&sessionAdministrationEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
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
	service := newSessionAdministrationService(persistence, &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, mailer,
		&sessionAdministrationEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	require.NoError(t, service.RevokeUserSessions(context.Background(), invocation, RevokeUserSessionsCommand{UserID: model.UserID(userID), IdempotencyKey: "row", batchRetainedOutcome: true}))
	require.Empty(t, mailer.requests, "retained mail")
	require.NotContains(t, events, "list-active", "retained mail")
}

func TestAdminSessionCrossUserNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	session := &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID()}
	service := newSessionAdministrationService(
		&sessionAdministrationStoreFake{events: &events, session: session},
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		time.Now,
	)
	err := service.RevokeUserSession(context.Background(), Invocation{}, RevokeUserSessionCommand{
		UserID: model.UserID(userID), SessionID: session.ID,
	})
	require.True(t, apperror.Is(err, "session.not_found"), "error = %v", err)
	require.NotContains(t, events, "audit-begin", "the rejected command was audited")
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
			service := newSessionAdministrationService(persistence,
				&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
				&sessionAdministrationAuthorizerFake{events: &events}, audit,
				&securityNoticeMailerFake{events: &events}, effects, func() time.Time { return now })
			invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
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
			// change publishes. Lookups may move freely.
			want := []string{"authorize-manage", "begin"}
			if outcome == "attempt unavailable" {
				require.Nil(t, persistence.revokeAllInput, "revoked without a durable audit attempt")
			} else {
				want = append(want, "store-revoke-all")
				input := persistence.revokeAllInput
				require.Equal(t, model.AuditEventID(audit.beginID), input.AuditEventID)
				require.Equal(t, now.UnixMilli(), input.AuditAt)
				require.Equal(t, now.UnixMilli(), input.RevokedAt)
				require.NotNil(t, input.Command)
				require.Equal(t, model.SessionRevocationAdministratorAllSessions, input.Reason)
			}
			if outcome != "changed" {
				require.NotContains(t, events, "publish-revocation", "an unchanged or failed revocation was published")
			} else {
				want = append(want, "publish-revocation")
				require.Equal(t, userID, effects.userID)
				require.True(t, slices.Equal(effects.sessionIDs, sessionIDs(persistence.revokeAllResult.Sessions)))
				require.True(t, slices.Equal(effects.hashes, persistence.revokeAllResult.TokenHashes))
			}
			if outcome == "commit unavailable" || outcome == "failure audit unavailable" {
				want = append(want, "fail")
				require.Equal(t, audit.beginID, audit.failID)
				require.Equal(t, "administration.unavailable", audit.failCode)
			}
			ordertest.RequireInOrder(t, events, want...)
			require.Equal(t, model.ActionSessionManage, audit.attempt.Action)
			require.Equal(t, userID, audit.attempt.Resource.ID)
			require.Equal(t, "revoke_sessions", audit.attempt.Operation)
		})
	}
}
