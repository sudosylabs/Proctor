// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSessionAuthenticationUsesMicrosecondExpiryBoundaries(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, 8, 12, 12, 0, 0, 123_700_000, time.UTC)
	for _, expiry := range []string{"access credential", "idle Session"} {
		t.Run(expiry, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				offset  time.Duration
				expired bool
			}{
				{name: "before", offset: -time.Microsecond},
				{name: "normalized before", offset: -time.Nanosecond},
				{name: "at", expired: true},
				{name: "after", offset: time.Microsecond, expired: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					service, persistence, raw, session, credential := sessionTimeFixture(t, deadline)
					service.now = func() time.Time { return deadline.Add(test.offset) }
					switch expiry {
					case "access credential":
						credential.ExpiresAt = deadline
					case "idle Session":
						session.IdleExpiresAt = deadline
					}
					principal, err := service.authenticateAccess(context.Background(), raw)
					if test.expired {
						if principal != nil || !Is(err, "authentication.invalid_token") {
							t.Fatalf("authentication at %v = %#v, %v", service.now(), principal, err)
						}
						if expiry != "access credential" && !persistence.sessions[session.ID.String()].RevokedAt.Time.Equal(model.TimeUTC(service.now())) {
							t.Fatal("Session expiry revocation lost the decision's microsecond precision")
						}
					} else if err != nil || principal == nil {
						t.Fatalf("authentication before %v = %#v, %v", deadline, principal, err)
					}
				})
			}
		})
	}
}

func TestSessionPrincipalRevalidationUsesMicrosecondExpiryBoundaries(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, 8, 12, 12, 0, 0, 123_700_000, time.UTC)
	for _, expiry := range []string{"idle", "absolute"} {
		t.Run(expiry, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				offset  time.Duration
				expired bool
			}{
				{name: "before", offset: -time.Microsecond},
				{name: "normalized before", offset: -time.Nanosecond},
				{name: "at", expired: true},
				{name: "after", offset: time.Microsecond, expired: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					service, _, raw, session, credential := sessionTimeFixture(t, deadline)
					session.IdleExpiresAt = deadline
					if expiry == "absolute" {
						session.ExpiresAt, credential.ExpiresAt = deadline, deadline
					}
					service.now = func() time.Time { return deadline.Add(-time.Microsecond) }
					principal, err := service.authenticateAccess(context.Background(), raw)
					if err != nil {
						t.Fatalf("establish Principal before expiry: %v", err)
					}
					service.now = func() time.Time { return deadline.Add(test.offset) }
					err = service.ValidatePrincipal(context.Background(), *principal)
					if test.expired {
						if !Is(err, "authentication.invalid_token") || !session.RevokedAt.Time.Equal(model.TimeUTC(service.now())) {
							t.Fatalf("Principal validation at %v = %v; revoked at %v", service.now(), err, session.RevokedAt)
						}
					} else if err != nil || session.RevokedAt.Valid {
						t.Fatalf("Principal validation before expiry = %v; revoked at %v", err, session.RevokedAt)
					}
				})
			}
		})
	}
}

func TestSessionCreationKeepsMicrosecondDeadlinesAndAbsoluteCap(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		absolute time.Duration
	}{
		{name: "individual deadlines", absolute: 48*time.Hour + 700*time.Microsecond},
		{name: "absolute cap", absolute: 30*time.Minute + 700*time.Microsecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			service := newTestAuthenticationService(t, persistence)
			clock := time.Date(2026, 8, 12, 12, 0, 0, 123_456_789, time.FixedZone("offset", 7200))
			service.now = func() time.Time { return clock }
			service.sessionPolicy.AccessTTL = time.Hour + 500*time.Microsecond
			service.sessionPolicy.RefreshTTL = 24*time.Hour + 600*time.Microsecond
			service.sessionPolicy.IdleTTL = 2*time.Hour + 800*time.Microsecond
			service.sessionPolicy.AbsoluteTTL = test.absolute
			now := model.TimeUTC(clock)
			user := &model.User{ID: model.NewUserID()}
			password := &model.PasswordCredential{ID: model.NewPasswordCredentialID(), UserID: user.ID, Revision: 1}
			persistence.passwords[user.ID.String()] = password
			session, tokens, err := service.createSession(context.Background(), sessionIssuance{
				User: user, ClientType: model.SessionClientCLI, AuthenticationMethod: "password",
				AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: now.UnixMilli(),
				PasswordProof: store.PasswordCredentialProof{ID: password.ID, Revision: password.Revision},
			})
			if err != nil {
				t.Fatal(err)
			}
			absolute := now.Add(test.absolute)
			if !session.LastActivityAt.Equal(now) || !session.ExpiresAt.Equal(absolute) {
				t.Fatalf("Session creation times = %v/%v, want %v/%v", session.LastActivityAt, session.ExpiresAt, now, absolute)
			}
			wantAccess, wantIdle := now.Add(service.sessionPolicy.AccessTTL), now.Add(service.sessionPolicy.IdleTTL)
			wantRefresh := now.Add(service.sessionPolicy.RefreshTTL)
			if absolute.Before(wantAccess) {
				wantAccess = absolute
			}
			if absolute.Before(wantIdle) {
				wantIdle = absolute
			}
			if absolute.Before(wantRefresh) {
				wantRefresh = absolute
			}
			if !tokens.AccessExpiresAt.Equal(wantAccess) || !tokens.RefreshExpiresAt.Equal(wantRefresh) || !session.IdleExpiresAt.Equal(wantIdle) {
				t.Fatalf("Session deadlines = %v/%v/%v, want %v/%v/%v", tokens.AccessExpiresAt, tokens.RefreshExpiresAt, session.IdleExpiresAt, wantAccess, wantRefresh, wantIdle)
			}
		})
	}
}

func TestSessionRefreshUsesOneNativeDecisionInstant(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 8, 12, 12, 0, 0, 123_456_789, time.FixedZone("offset", 7200))
	persistence := newAuthenticationStoreFake()
	user := &model.User{ID: model.NewUserID()}
	persistence.users[user.ID.String()] = user
	persistence.rotation = &store.SessionRotation{Session: &model.Session{
		ID: model.NewSessionID(), UserID: user.ID, ClientType: model.SessionClientWeb,
	}}
	service := newTestAuthenticationService(t, persistence)
	service.sessionPolicy.AccessTTL += 500 * time.Microsecond
	service.sessionPolicy.RefreshTTL += 600 * time.Microsecond
	service.sessionPolicy.IdleTTL += 700 * time.Microsecond
	clockReads := 0
	service.now = func() time.Time { clockReads++; return clock.Add(time.Duration(clockReads-1) * time.Hour) }
	_, tokens, err := service.refresh(context.Background(), RefreshSessionCommand{RefreshToken: model.NewCredentialToken()})
	if err != nil {
		t.Fatal(err)
	}
	now := model.TimeUTC(clock)
	if clockReads != 1 || !persistence.rotatedAt.Equal(now) || persistence.rotatedAt.Location() != time.UTC ||
		!persistence.rotatedIdleExpiry.Equal(now.Add(service.sessionPolicy.IdleTTL)) ||
		!tokens.AccessExpiresAt.Equal(now.Add(service.sessionPolicy.AccessTTL)) ||
		!tokens.RefreshExpiresAt.Equal(now.Add(service.sessionPolicy.RefreshTTL)) {
		t.Fatalf("refresh lost a single native instant: reads=%d, at=%v, idle=%v, tokens=%#v", clockReads, persistence.rotatedAt, persistence.rotatedIdleExpiry, tokens)
	}
}

func TestSessionActivityUsesNativeDebounceBoundary(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, 8, 12, 12, 0, 0, 123_700_000, time.UTC)
	for _, test := range []struct {
		name   string
		offset time.Duration
		update bool
	}{
		{name: "before", offset: -time.Microsecond},
		{name: "normalized before", offset: -time.Nanosecond},
		{name: "at", update: true},
		{name: "after", offset: time.Microsecond, update: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, raw, session, _ := sessionTimeFixture(t, deadline)
			service.sessionPolicy.ActivityUpdateInterval = time.Minute + 500*time.Microsecond
			service.sessionPolicy.IdleTTL = time.Hour + 600*time.Microsecond
			initialActivity := deadline.Add(-service.sessionPolicy.ActivityUpdateInterval)
			session.LastActivityAt = initialActivity
			initialIdle := session.IdleExpiresAt
			service.now = func() time.Time { return deadline.Add(test.offset) }
			if _, err := service.authenticateAccess(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			if !test.update {
				if !session.LastActivityAt.Equal(initialActivity) || !session.IdleExpiresAt.Equal(initialIdle) {
					t.Fatal("Session activity advanced before the complete debounce interval")
				}
				return
			}
			now := model.TimeUTC(service.now())
			if !session.LastActivityAt.Equal(now) || !session.IdleExpiresAt.Equal(now.Add(service.sessionPolicy.IdleTTL)) {
				t.Fatal("Session activity or idle extension lost microsecond precision")
			}
		})
	}
}

func sessionTimeFixture(t *testing.T, deadline time.Time) (*authenticationService, *authenticationStoreFake,
	string, *model.Session, *model.SessionCredential,
) {
	t.Helper()
	persistence := newAuthenticationStoreFake()
	user := &model.User{ID: model.NewUserID()}
	raw := model.NewCredentialToken()
	session := &model.Session{
		ID: model.NewSessionID(), UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: deadline.Add(-time.Hour), LastActivityAt: deadline.Add(-30 * time.Second),
		IdleExpiresAt: deadline.Add(90 * time.Minute), ExpiresAt: deadline.Add(2 * time.Hour),
	}
	credential := &model.SessionCredential{ID: model.NewSessionCredentialID(), SessionID: session.ID,
		Kind: model.SessionCredentialAccess, TokenHash: model.HashToken(raw), ExpiresAt: deadline.Add(time.Hour)}
	persistence.users[user.ID.String()] = user
	persistence.sessions[session.ID.String()] = session
	persistence.accessByHash[credential.TokenHash] = credential
	persistence.sessionByCredential[credential.TokenHash] = session
	return newTestAuthenticationService(t, persistence), persistence, raw, session, credential
}
