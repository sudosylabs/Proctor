// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// cacheLegacyAuthentication represents a positive snapshot left in a shared
// cache by an earlier process. It must never authorize a current request.
func cacheLegacyAuthentication(t *testing.T, cache authenticationCache, credential *model.SessionCredential,
	session *model.Session, user *model.User,
) {
	t.Helper()
	value := struct {
		Credential *model.SessionCredential `json:"credential"`
		Session    *model.Session           `json:"session"`
		User       *model.User              `json:"user"`
	}{Credential: credential, Session: session, User: user}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetAlways(context.Background(), authenticationCachePrefix+credential.TokenHash, data, time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAuthenticationUsesCurrentStoreStateWithWarmCache(t *testing.T) {
	t.Parallel()

	for _, clientType := range []model.SessionClientType{model.SessionClientWeb, model.SessionClientCLI} {
		t.Run(string(clientType), func(t *testing.T) {
			for _, test := range []struct {
				name     string
				change   func(*authenticationStoreFake, *model.User, *model.SessionCredential, *model.Session, time.Time)
				wantCode string
			}{
				{name: "revoked credential", wantCode: "authentication.invalid_token", change: func(_ *authenticationStoreFake, _ *model.User, credential *model.SessionCredential, _ *model.Session, at time.Time) {
					credential.RevokedAt = model.OptionalTimeFrom(at)
				}},
				{name: "rotated credential", wantCode: "authentication.invalid_token", change: func(persistence *authenticationStoreFake, _ *model.User, credential *model.SessionCredential, _ *model.Session, _ time.Time) {
					delete(persistence.accessByHash, credential.TokenHash)
				}},
				{name: "revoked Session", wantCode: "authentication.invalid_token", change: func(_ *authenticationStoreFake, _ *model.User, _ *model.SessionCredential, session *model.Session, at time.Time) {
					session.RevokedAt = model.OptionalTimeFrom(at)
					session.RevocationReason = model.SessionRevocationUserLogout
				}},
				{name: "disabled User", wantCode: "authentication.invalid_token", change: func(persistence *authenticationStoreFake, user *model.User, _ *model.SessionCredential, _ *model.Session, at time.Time) {
					persistence.users[user.ID.String()].DisabledAt = model.OptionalTimeFrom(at)
				}},
				{name: "archived User", wantCode: "authentication.invalid_token", change: func(persistence *authenticationStoreFake, user *model.User, _ *model.SessionCredential, _ *model.Session, at time.Time) {
					persistence.users[user.ID.String()].ArchivedAt = model.OptionalTimeFrom(at)
				}},
				{name: "missing User", wantCode: "authentication.invalid_token", change: func(persistence *authenticationStoreFake, user *model.User, _ *model.SessionCredential, _ *model.Session, _ time.Time) {
					delete(persistence.users, user.ID.String())
				}},
				{name: "User Store unavailable", wantCode: "authentication.internal", change: func(persistence *authenticationStoreFake, _ *model.User, _ *model.SessionCredential, _ *model.Session, _ time.Time) {
					persistence.userGetErr = errors.New("database unavailable")
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					persistence := newAuthenticationStoreFake()
					cache := newAuthenticationCacheFake()
					service := newTestAuthenticationServiceWithCache(t, persistence, cache)
					at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
					service.now = func() time.Time { return at }
					user, rawAccess := seedAuthenticatedSession(t, service)
					hash := model.HashToken(rawAccess)
					credential := persistence.accessByHash[hash]
					session := persistence.sessionByCredential[hash]
					session.ClientType = clientType
					cacheLegacyAuthentication(t, cache, credential, session, user)
					if _, err := service.authenticateAccess(context.Background(), rawAccess); err != nil {
						t.Fatalf("initial authentication: %v", err)
					}

					test.change(persistence, user, credential, session, at)
					principal, err := service.authenticateAccess(context.Background(), rawAccess)
					if principal != nil || !Is(err, test.wantCode) {
						t.Fatalf("authentication after durable change = %#v, %v; want %s", principal, err, test.wantCode)
					}
				})
			}
		})
	}
}

func TestSessionAuthenticationFailsClosedWhenCredentialStoreUnavailableWithWarmCache(t *testing.T) {
	t.Parallel()
	persistence := newAuthenticationStoreFake()
	cache := newAuthenticationCacheFake()
	service := newTestAuthenticationServiceWithCache(t, persistence, cache)
	user, rawAccess := seedAuthenticatedSession(t, service)
	hash := model.HashToken(rawAccess)
	cacheLegacyAuthentication(t, cache, persistence.accessByHash[hash], persistence.sessionByCredential[hash], user)
	service.sessionCredentials = unavailableAuthenticationCredentialStore{SessionCredentialStore: persistence.SessionCredential()}
	principal, err := service.authenticateAccess(context.Background(), rawAccess)
	if principal != nil || !Is(err, "authentication.internal") {
		t.Fatalf("authentication with unavailable Store = %#v, %v", principal, err)
	}
}

type unavailableAuthenticationCredentialStore struct{ store.SessionCredentialStore }

func (unavailableAuthenticationCredentialStore) GetSessionByTokenHash(context.Context, string,
	model.SessionCredentialKind,
) (*model.SessionCredential, *model.Session, error) {
	return nil, nil, errors.New("database unavailable")
}

func TestSessionRevocationRejectsAfterLocalCacheDeleteFailure(t *testing.T) {
	t.Parallel()

	persistence := newAuthenticationStoreFake()
	cache := &failingAuthenticationCacheDelete{authenticationCacheFake: newAuthenticationCacheFake()}
	service := newTestAuthenticationServiceWithCache(t, persistence, cache)
	service.securityEffects = newTestRealtimeService(t, cache)
	user, rawAccess := seedAuthenticatedSession(t, service)
	hash := model.HashToken(rawAccess)
	cacheLegacyAuthentication(t, cache, persistence.accessByHash[hash], persistence.sessionByCredential[hash], user)
	principal, err := service.authenticateAccess(context.Background(), rawAccess)
	if err != nil {
		t.Fatal(err)
	}
	cache.fail = true
	if err := service.logout(context.Background(), NewInvocation(*principal, model.RequestMetadata{})); err != nil {
		t.Fatalf("logout after cache failure: %v", err)
	}
	if cache.failures == 0 {
		t.Fatal("logout did not attempt local cache invalidation")
	}
	if _, err := cache.Get(context.Background(), authenticationCachePrefix+hash); err != nil {
		t.Fatalf("failed delete must retain the positive cache: %v", err)
	}
	if principal, err := service.authenticateAccess(context.Background(), rawAccess); principal != nil || !Is(err, "authentication.invalid_token") {
		t.Fatalf("authentication after committed logout = %#v, %v", principal, err)
	}
}

type failingAuthenticationCacheDelete struct {
	*authenticationCacheFake
	fail     bool
	failures int
}

func (c *failingAuthenticationCacheDelete) Delete(ctx context.Context, key string) error {
	if c.fail {
		c.failures++
		return errors.New("cache delete unavailable")
	}
	return c.authenticationCacheFake.Delete(ctx, key)
}

func TestSessionAuthenticationPreservesDebouncedActivityAndAbsoluteLimit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		absoluteTTL time.Duration
	}{
		{name: "idle extension", absoluteTTL: 24 * time.Hour},
		{name: "absolute limit", absoluteTTL: 90 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			persistence := newAuthenticationStoreFake()
			cache := newAuthenticationCacheFake()
			service := newTestAuthenticationServiceWithCache(t, persistence, cache)
			at := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
			now := at
			service.now = func() time.Time { return now }
			service.sessionPolicy.AbsoluteTTL = test.absoluteTTL
			_, rawAccess := seedAuthenticatedSession(t, service)
			session := persistence.sessionByCredential[model.HashToken(rawAccess)]
			initialIdleExpiry := session.IdleExpiresAt

			now = at.Add(service.sessionPolicy.ActivityUpdateInterval - time.Millisecond)
			if _, err := service.authenticateAccess(context.Background(), rawAccess); err != nil {
				t.Fatal(err)
			}
			if !session.LastActivityAt.Equal(at) || !session.IdleExpiresAt.Equal(initialIdleExpiry) {
				t.Fatal("activity changed before the debounce interval")
			}

			now = at.Add(service.sessionPolicy.ActivityUpdateInterval)
			if _, err := service.authenticateAccess(context.Background(), rawAccess); err != nil {
				t.Fatal(err)
			}
			wantIdleExpiry := now.Add(service.sessionPolicy.IdleTTL)
			if session.ExpiresAt.Before(wantIdleExpiry) {
				wantIdleExpiry = session.ExpiresAt
			}
			if !session.LastActivityAt.Equal(now) || !session.IdleExpiresAt.Equal(wantIdleExpiry) {
				t.Fatalf("activity update = %v/%v, want %v/%v", session.LastActivityAt, session.IdleExpiresAt, now, wantIdleExpiry)
			}
			updatedAt := now
			now = now.Add(time.Second)
			if _, err := service.authenticateAccess(context.Background(), rawAccess); err != nil {
				t.Fatal(err)
			}
			if !session.LastActivityAt.Equal(updatedAt) || !session.IdleExpiresAt.Equal(wantIdleExpiry) {
				t.Fatal("another request repeated the debounced activity write")
			}
		})
	}
}
