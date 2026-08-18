// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"reflect"
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
}

func (s *sessionAdministrationStoreFake) Get(context.Context, string) (*model.Session, error) {
	*s.events = append(*s.events, "get-session")
	return s.session, s.getErr
}

func (s *sessionAdministrationStoreFake) ListByUser(context.Context, string) ([]*model.Session, error) {
	*s.events = append(*s.events, "list-all")
	return s.list, s.listErr
}

func (s *sessionAdministrationStoreFake) ListActiveByUser(context.Context, string, int64) ([]*model.Session, error) {
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

type sessionAdministrationEffectsFake struct{ events *[]string }

func (e *sessionAdministrationEffectsFake) SessionsRevoked(context.Context, string, []*model.Session, []string) {
	*e.events = append(*e.events, "publish-revocation")
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
	service := newSessionAdministrationService(
		&sessionAdministrationStoreFake{events: &events, list: []*model.Session{session}},
		&sessionAdministrationUserStoreFake{events: &events, user: sessionAdministrationTestUser(userID)},
		&sessionAdministrationAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events},
		&securityNoticeMailerFake{events: &events},
		&sessionAdministrationEffectsFake{events: &events},
		func() time.Time { return time.UnixMilli(500) },
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
