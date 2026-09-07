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
	"reflect"
	"slices"
	"testing"
	"time"

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
	listedAt        time.Time
}

func (s *sessionAdministrationStoreFake) Get(context.Context, string) (*model.Session, error) {
	*s.events = append(*s.events, "get-session")
	return s.session, s.getErr
}

func (s *sessionAdministrationStoreFake) ListByUser(context.Context, string) ([]*model.Session, error) {
	*s.events = append(*s.events, "list-all")
	return s.list, s.listErr
}

func (s *sessionAdministrationStoreFake) ListActiveByUser(_ context.Context, _ string, at time.Time) ([]*model.Session, error) {
	s.listedAt = at
	*s.events = append(*s.events, "list-active")
	return s.list, s.listErr
}

func (s *sessionAdministrationStoreFake) RevokeWithAudit(_ context.Context, input *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	*s.events = append(*s.events, "store-revoke")
	s.revokeInput = input
	return s.revokeResult, s.revokeErr
}

func (s *sessionAdministrationStoreFake) RevokeAllForUserWithAudit(_ context.Context, input *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error) {
	*s.events = append(*s.events, "store-revoke-all")
	s.revokeAllInput = input
	input.Replayed, input.NoOp = s.replayed, s.noOp
	return s.revokeAllResult, s.revokeAllErr
}

type sessionAdministrationAuthorizerFake struct{ events *[]string }

func (a *sessionAdministrationAuthorizerFake) AuthorizeView(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-view")
	return nil
}

func (a *sessionAdministrationAuthorizerFake) AuthorizeManage(context.Context, Invocation, string) error {
	*a.events = append(*a.events, "authorize-manage")
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

func (s *sessionAdministrationUserStoreFake) Get(context.Context, string) (*model.User, error) {
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
	userID := model.NewId()
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
	got, err := service.List(context.Background(), Invocation{}, ListUserSessionsQuery{UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID.String() != session.ID.String() {
		t.Fatalf("list = %#v", got)
	}
	want := []string{"authorize-view", "list-active"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if !persistence.listedAt.Equal(model.TimeUTC(at)) || persistence.listedAt.Location() != time.UTC {
		t.Fatalf("active Session listing time = %v, want %v", persistence.listedAt, model.TimeUTC(at))
	}
}

func TestAdminSessionRevokeCommitsBeforePublishing(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
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
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		mailer,
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	if err := service.RevokeOne(context.Background(), Invocation{}, RevokeUserSessionCommand{
		UserID: userID, SessionID: session.ID.String(),
	}); err != nil {
		t.Fatal(err)
	}
	if persistence.revokeInput.SessionID != session.ID.String() || persistence.revokeInput.AuditEventID == "" {
		t.Fatalf("input = %#v", persistence.revokeInput)
	}
	if len(mailer.requests) != 1 || mailer.requests[0].TemplateKey != model.MailTemplateIdentitySessionsRevokedByAdmin ||
		persistence.revokeInput.Occurrence == nil || persistence.revokeInput.Delivery == nil || persistence.revokeInput.DeliveryJob == nil {
		t.Fatalf("mail request/input = %#v / %#v", mailer.requests, persistence.revokeInput)
	}
	want := []string{"authorize-manage", "get-session", "get-user", "prepare-mail", "audit-begin", "store-revoke", "publish-revocation"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAdminSessionRevokeFailurePublishesNoEffect(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
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
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		time.Now,
	)
	err := service.RevokeOne(context.Background(), Invocation{}, RevokeUserSessionCommand{
		UserID: userID, SessionID: session.ID.String(),
	})
	if !Is(err, "administration.unavailable") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-session", "get-user", "prepare-mail", "audit-begin", "store-revoke", "audit-fail"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAdminSessionRevokeAllCommitsBeforePublishing(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
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
		&institutionAuditorFake{events: &events, beginID: model.NewId()},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
	)
	if err := service.RevokeAll(context.Background(), Invocation{}, RevokeUserSessionsCommand{UserID: userID}); err != nil {
		t.Fatal(err)
	}
	if persistence.revokeAllInput.UserID != userID || persistence.revokeAllInput.AuditEventID == "" || persistence.revokeAllInput.Occurrence == nil {
		t.Fatalf("input = %#v", persistence.revokeAllInput)
	}
	want := []string{"authorize-manage", "get-user", "prepare-mail", "audit-begin", "store-revoke-all", "publish-revocation"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAdminSessionIdempotentNoOpPreparesNoticeForAuthoritativeRace(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewUserID().String()
	persistence := &sessionAdministrationStoreFake{events: &events, list: []*model.Session{},
		revokeAllResult: &store.UserSessionsRevocationResult{Sessions: []*model.Session{}, TokenHashes: []string{}}}
	mailer := &securityNoticeMailerFake{events: &events}
	service := newSessionAdministrationService(persistence, &sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer,
		&sessionAdministrationEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	if err := service.RevokeAll(context.Background(), invocation, RevokeUserSessionsCommand{UserID: userID, IdempotencyKey: "row"}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.requests) != 1 || persistence.revokeAllInput == nil || persistence.revokeAllInput.Occurrence == nil {
		t.Fatalf("idempotent no-op mail=%#v input=%#v", mailer.requests, persistence.revokeAllInput)
	}
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
		&sessionAdministrationAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, mailer,
		&sessionAdministrationEffectsFake{events: &events}, time.Now)
	invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
	if err := service.RevokeAll(context.Background(), invocation, RevokeUserSessionsCommand{UserID: userID, IdempotencyKey: "row", batchRetainedOutcome: true}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.requests) != 0 || slices.Contains(events, "list-active") {
		t.Fatalf("retained mail=%#v events=%v", mailer.requests, events)
	}
}

func TestAdminSessionCrossUserNotFound(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
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
	err := service.RevokeOne(context.Background(), Invocation{}, RevokeUserSessionCommand{
		UserID: userID, SessionID: session.ID.String(),
	})
	if !Is(err, "session.not_found") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-manage", "get-session"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAdminSessionRevocationOutcomesPreserveAuditAndBatchResult(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"changed", "empty", "replay", "attempt unavailable", "commit unavailable", "failure audit unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			now := time.UnixMilli(500)
			userID := model.NewUserID().String()
			events := []string{}
			audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}
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
				audit.beginErr = NewError("audit.unavailable")
				unchanged = true // An unattempted mutation does not replace the caller's value.
			case "commit unavailable", "failure audit unavailable":
				persistence.revokeAllErr = errors.New("atomic revocation failed")
				if outcome == "failure audit unavailable" {
					audit.failErr = NewError("audit.unavailable")
				}
			}
			effects := &sessionAdministrationEffectsFake{events: &events}
			service := newSessionAdministrationService(persistence,
				&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
				&sessionAdministrationAuthorizerFake{events: &events}, audit,
				&securityNoticeMailerFake{events: &events}, effects, func() time.Time { return now })
			invocation := NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{})
			err := service.RevokeAll(context.Background(), invocation, RevokeUserSessionsCommand{
				UserID: userID, IdempotencyKey: "repeat-command", batchReplayed: &unchanged,
			})
			wantCode := ""
			switch outcome {
			case "attempt unavailable", "failure audit unavailable":
				wantCode = "audit.unavailable"
			case "commit unavailable":
				wantCode = "administration.unavailable"
			}
			if (wantCode == "" && err != nil) || (wantCode != "" && !Is(err, wantCode)) {
				t.Fatalf("error = %v, want %q", err, wantCode)
			}
			if unchanged != (outcome == "empty" || outcome == "replay" || outcome == "attempt unavailable") {
				t.Fatalf("unchanged = %t for %s", unchanged, outcome)
			}
			want := []string{"authorize-manage", "get-user", "prepare-mail", "begin"}
			if outcome != "attempt unavailable" {
				want = append(want, "store-revoke-all")
				input := persistence.revokeAllInput
				if input.AuditEventID != audit.beginID || input.AuditAt != now.UnixMilli() ||
					input.RevokedAt != now.UnixMilli() || input.Command == nil ||
					input.Reason != model.SessionRevocationAdministratorAllSessions {
					t.Fatalf("atomic input = %#v", input)
				}
			}
			if outcome == "changed" {
				want = append(want, "publish-revocation")
				if effects.userID != userID || !slices.Equal(effects.sessionIDs, sessionIds(persistence.revokeAllResult.Sessions)) ||
					!slices.Equal(effects.hashes, persistence.revokeAllResult.TokenHashes) {
					t.Fatalf("affected Sessions = %#v", effects)
				}
			}
			if outcome == "commit unavailable" || outcome == "failure audit unavailable" {
				want = append(want, "fail")
				if audit.failID != audit.beginID || audit.failCode != "administration.unavailable" {
					t.Fatalf("failed audit = %#v", audit)
				}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %v, want %v", events, want)
			}
			if audit.attempt.Action != model.ActionSessionManage || audit.attempt.Resource.ID != userID ||
				audit.attempt.Operation != "revoke_sessions" {
				t.Fatalf("audit intent = %#v", audit.attempt)
			}
		})
	}
}
