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
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSelfSessionServiceUsesCallerOwnershipAndPostCommitEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	principal := selfSessionPrincipal(now)
	session := &model.Session{ID: principal.SessionID, UserID: principal.UserID}
	events := []string{}
	persistence := &selfSessionStoreFake{
		events:  &events,
		session: session,
		hashes:  []string{"access-hash"},
	}
	effects := &selfSessionEffectsFake{events: &events}
	service, err := newSelfSessionService(persistence, &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}, effects, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	invocation := NewInvocation(principal, model.RequestMetadata{})
	if err = service.RevokeOne(
		context.Background(),
		invocation,
		RevokeSessionCommand{SessionID: session.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if want := []string{"get", "begin", "revoke", "effects"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if persistence.revokedUserID != principal.UserID.String() ||
		persistence.revokedAt != now.UnixMilli() {
		t.Fatalf("revocation = user %q at %d", persistence.revokedUserID, persistence.revokedAt)
	}
	if effects.userID != principal.UserID.String() ||
		!reflect.DeepEqual(effects.sessionIDs, []string{session.ID.String()}) ||
		!reflect.DeepEqual(effects.hashes, []string{"access-hash"}) {
		t.Fatalf("effects = %#v", effects)
	}
}

func TestSelfSessionServiceHidesAnotherUsersSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	principal := selfSessionPrincipal(now)
	persistence := &selfSessionStoreFake{
		events:  &[]string{},
		session: &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID()},
	}
	service, err := newSelfSessionService(
		persistence,
		&mutationAttemptAuditorFake{events: &[]string{}, beginID: model.NewId()},
		&selfSessionEffectsFake{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}

	err = service.RevokeOne(
		context.Background(),
		NewInvocation(principal, model.RequestMetadata{}),
		RevokeSessionCommand{SessionID: persistence.session.ID.String()},
	)
	if !Is(err, "session.not_found") {
		t.Fatalf("error = %v, want session.not_found", err)
	}
	if persistence.revokeCalls != 0 {
		t.Fatalf("revoke calls = %d, want 0", persistence.revokeCalls)
	}
}

func TestSelfSessionListUsesNativeDecisionTime(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 12, 9, 30, 0, 123_456_789, time.FixedZone("offset", 7200))
	principal := selfSessionPrincipal(model.TimeUTC(at))
	persistence := &selfSessionStoreFake{session: &model.Session{ID: principal.SessionID, UserID: principal.UserID}}
	service, err := newSelfSessionService(persistence, &mutationAttemptAuditorFake{events: &[]string{}, beginID: model.NewId()}, &selfSessionEffectsFake{}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), NewInvocation(principal, model.RequestMetadata{})); err != nil {
		t.Fatal(err)
	}
	if !persistence.listedAt.Equal(model.TimeUTC(at)) || persistence.listedAt.Location() != time.UTC {
		t.Fatalf("active Session listing time = %v, want %v", persistence.listedAt, model.TimeUTC(at))
	}
}

func TestSelfSessionServiceRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newSelfSessionService(&selfSessionStoreFake{}, nil, &selfSessionEffectsFake{}, time.Now); err == nil {
		t.Fatal("nil self-session audit was accepted")
	}
	if _, err := newSelfSessionService(nil, &mutationAttemptAuditorFake{}, &selfSessionEffectsFake{}, time.Now); err == nil {
		t.Fatal("nil self-session store was accepted")
	}
	if _, err := newSelfSessionService(&selfSessionStoreFake{}, &mutationAttemptAuditorFake{}, nil, time.Now); err == nil {
		t.Fatal("nil self-session effects were accepted")
	}
	if _, err := newSelfSessionService(&selfSessionStoreFake{}, &mutationAttemptAuditorFake{}, &selfSessionEffectsFake{}, nil); err == nil {
		t.Fatal("nil self-session clock was accepted")
	}
}

func TestSelfSessionRevocationsRequireAuditBeforeMutationAndEffects(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"revoke_own_session", "revoke_own_sessions", "logout"} {
		for _, outcome := range []string{"success", "attempt unavailable", "completion unavailable"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
				principal := selfSessionPrincipal(now)
				events := []string{}
				persistence := &selfSessionStoreFake{events: &events,
					session: &model.Session{ID: principal.SessionID, UserID: principal.UserID}, hashes: []string{"access-hash"}}
				audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}
				effects := &selfSessionEffectsFake{events: &events}
				if outcome == "attempt unavailable" {
					audit.beginErr = NewError("audit.unavailable")
				}
				if outcome == "completion unavailable" {
					persistence.revokeErr = errors.New("audit completion unavailable")
				}
				self, err := newSelfSessionService(persistence, audit, effects, func() time.Time { return now })
				if err != nil {
					t.Fatal(err)
				}
				invocation := NewInvocation(principal, model.RequestMetadata{})
				switch operation {
				case "revoke_own_session":
					err = self.RevokeOne(context.Background(), invocation, RevokeSessionCommand{SessionID: principal.SessionID.String()})
				case "revoke_own_sessions":
					err = self.RevokeAll(context.Background(), invocation)
				case "logout":
					authentication := &authenticationService{sessions: persistence, audit: audit, securityEffects: effects, now: self.now}
					err = authentication.logout(context.Background(), invocation)
				}
				if outcome != "success" {
					if err == nil || persistence.session.RevokedAt.Valid || effects.userID != "" {
						t.Fatalf("failed audit allowed revocation/effect: error=%v session=%#v effects=%#v", err, persistence.session, effects)
					}
					if outcome == "attempt unavailable" && persistence.revokeCalls != 0 {
						t.Fatal("Store mutation ran before durable attempt")
					}
					if outcome == "completion unavailable" && audit.failID != audit.beginID {
						t.Fatal("mutation rollback was not recorded against its audit attempt")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !persistence.session.RevokedAt.Valid || effects.userID != principal.UserID.String() {
					t.Fatalf("revocation/effects = %#v / %#v", persistence.session, effects)
				}
				want := []string{"begin", "revoke", "effects"}
				if operation == "revoke_own_session" {
					want = append([]string{"get"}, want...)
				}
				if !reflect.DeepEqual(events, want) {
					t.Fatalf("events = %v, want %v", events, want)
				}
				if audit.attempt.Operation != operation || audit.attempt.Resource.ID != principal.UserID.String() || audit.attempt.Action != model.ActionSessionManage {
					t.Fatalf("audit context = %#v", audit.attempt)
				}
				if input := persistence.revocation; input != nil && (input.AuditEventID != audit.beginID || input.AuditAt != now.UnixMilli() || input.Occurrence != nil || input.Delivery != nil || input.DeliveryJob != nil) {
					t.Fatalf("single revocation audit/notice = %#v", input)
				}
				if input := persistence.allRevocation; input != nil && (input.AuditEventID != audit.beginID || input.AuditAt != now.UnixMilli() || input.Occurrence != nil || input.Delivery != nil || input.DeliveryJob != nil) {
					t.Fatalf("all revocation audit/notice = %#v", input)
				}
			})
		}
	}
}

func TestSelfSessionRevocationRepeatOutcomes(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"revoke_own_session", "revoke_own_sessions", "logout"} {
		t.Run(operation, func(t *testing.T) {
			now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
			principal := selfSessionPrincipal(now)
			events := []string{}
			persistence := &selfSessionStoreFake{events: &events, session: &model.Session{
				ID: principal.SessionID, UserID: principal.UserID, RevokedAt: model.OptionalTimeFrom(now.Add(-time.Minute)),
			}}
			audit := &mutationAttemptAuditorFake{events: &events, beginID: model.NewId()}
			effects := &selfSessionEffectsFake{events: &events}
			self, err := newSelfSessionService(persistence, audit, effects, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			invocation := NewInvocation(principal, model.RequestMetadata{})
			switch operation {
			case "revoke_own_session":
				err = self.RevokeOne(context.Background(), invocation, RevokeSessionCommand{SessionID: principal.SessionID.String()})
				if !Is(err, "session.not_found") || audit.failCode != "session.not_found" {
					t.Fatalf("repeat = %v audit=%#v", err, audit)
				}
			case "revoke_own_sessions":
				err = self.RevokeAll(context.Background(), invocation)
				if err != nil || !persistence.allRevocation.NoOp {
					t.Fatalf("repeat = %v", err)
				}
			case "logout":
				authentication := &authenticationService{sessions: persistence, audit: audit, securityEffects: effects, now: self.now}
				err = authentication.logout(context.Background(), invocation)
				if err != nil {
					t.Fatal(err)
				}
			}
			if persistence.revokeCalls != 1 || effects.userID != "" {
				t.Fatalf("repeat bypassed audit or repeated effects: events=%v", events)
			}
		})
	}
}

func selfSessionPrincipal(at time.Time) model.Principal {
	return model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: at,
	}
}

type selfSessionStoreFake struct {
	store.SessionStore
	events        *[]string
	session       *model.Session
	hashes        []string
	revokedUserID string
	revokedAt     int64
	revokeCalls   int
	listedAt      time.Time
	revokeErr     error
	revocation    *store.SessionRevocation
	allRevocation *store.UserSessionsRevocation
}

func (s *selfSessionStoreFake) Get(context.Context, string) (*model.Session, error) {
	*s.events = append(*s.events, "get")
	if s.session == nil {
		return nil, store.NewErrNotFound("session", "")
	}
	return s.session, nil
}

func (s *selfSessionStoreFake) ListActiveByUser(_ context.Context, _ string, at time.Time) ([]*model.Session, error) {
	s.listedAt = at
	return []*model.Session{s.session}, nil
}

func (s *selfSessionStoreFake) RevokeWithAudit(_ context.Context, input *store.SessionRevocation) (*store.SessionRevocationResult, error) {
	*s.events = append(*s.events, "revoke")
	s.revokeCalls++
	s.revocation = input
	s.revokedUserID, s.revokedAt = input.UserID, input.RevokedAt
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	if s.session == nil || s.session.RevokedAt.Valid || s.session.UserID.String() != input.UserID {
		if input.Reason == model.SessionRevocationUserLogout {
			return &store.SessionRevocationResult{}, nil
		}
		return nil, store.NewErrNotFound("session", input.SessionID)
	}
	s.session.RevokedAt = model.OptionalTimeFromMillis(input.RevokedAt)
	return &store.SessionRevocationResult{Session: s.session, TokenHashes: append([]string(nil), s.hashes...)}, nil
}

func (s *selfSessionStoreFake) RevokeAllForUserWithAudit(_ context.Context, input *store.UserSessionsRevocation) (*store.UserSessionsRevocationResult, error) {
	*s.events = append(*s.events, "revoke")
	s.revokeCalls++
	s.allRevocation = input
	if s.revokeErr != nil {
		return nil, s.revokeErr
	}
	if s.session == nil || s.session.RevokedAt.Valid {
		input.NoOp = true
		return &store.UserSessionsRevocationResult{}, nil
	}
	s.session.RevokedAt = model.OptionalTimeFromMillis(input.RevokedAt)
	return &store.UserSessionsRevocationResult{Sessions: []*model.Session{s.session}, TokenHashes: append([]string(nil), s.hashes...)}, nil
}

type selfSessionEffectsFake struct {
	events     *[]string
	userID     string
	sessionIDs []string
	hashes     []string
}

func (e *selfSessionEffectsFake) AuthenticationCacheInvalidated(context.Context, string, []string) {}

func (e *selfSessionEffectsFake) SessionsRevoked(_ context.Context, userID string, sessionIDs, hashes []string) {
	if e.events != nil {
		*e.events = append(*e.events, "effects")
	}
	e.userID = userID
	e.sessionIDs = append([]string(nil), sessionIDs...)
	e.hashes = append([]string(nil), hashes...)
}
