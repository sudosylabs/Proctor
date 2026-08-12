// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
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
	service, err := newSelfSessionService(persistence, effects, func() time.Time { return now })
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
	if want := []string{"get", "revoke", "effects"}; !reflect.DeepEqual(events, want) {
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

func TestSelfSessionServiceRequiresFocusedDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newSelfSessionService(nil, &selfSessionEffectsFake{}, time.Now); err == nil {
		t.Fatal("nil self-session store was accepted")
	}
	if _, err := newSelfSessionService(&selfSessionStoreFake{}, nil, time.Now); err == nil {
		t.Fatal("nil self-session effects were accepted")
	}
	if _, err := newSelfSessionService(&selfSessionStoreFake{}, &selfSessionEffectsFake{}, nil); err == nil {
		t.Fatal("nil self-session clock was accepted")
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
}

func (s *selfSessionStoreFake) Get(context.Context, string) (*model.Session, error) {
	*s.events = append(*s.events, "get")
	if s.session == nil {
		return nil, store.NewErrNotFound("session", "")
	}
	return s.session, nil
}

func (s *selfSessionStoreFake) ListActiveByUser(context.Context, string, int64) ([]*model.Session, error) {
	return []*model.Session{s.session}, nil
}

func (s *selfSessionStoreFake) Revoke(_ context.Context, _ string, userID string, at int64, _ string) ([]string, error) {
	*s.events = append(*s.events, "revoke")
	s.revokeCalls++
	s.revokedUserID, s.revokedAt = userID, at
	return append([]string(nil), s.hashes...), nil
}

func (s *selfSessionStoreFake) RevokeAllForUser(context.Context, string, int64, string) ([]*model.Session, []string, error) {
	return []*model.Session{s.session}, append([]string(nil), s.hashes...), nil
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
