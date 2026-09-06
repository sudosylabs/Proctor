// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testSessionNativeTimeBoundaries(t *testing.T, ss store.Store) {
	// The deadline deliberately lies inside a millisecond, and far enough in
	// the future that real persistence creation clocks cannot cross it.
	deadline := model.NowUTC().Add(time.Hour).Truncate(time.Second).Add(123_700 * time.Microsecond)
	for _, expiry := range []string{"idle", "absolute"} {
		t.Run(expiry, func(t *testing.T) {
			t.Run("ListActive", func(t *testing.T) {
				user, session, _ := saveSessionTimeFixture(t, ss, expiry, deadline)
				for _, test := range sessionDecisionCases(deadline) {
					t.Run(test.name, func(t *testing.T) {
						active, err := ss.Session().ListActiveByUser(context.Background(), user.ID.String(), test.at)
						requireNoError(t, err)
						if test.expired && len(active) != 0 || !test.expired && (len(active) != 1 || active[0].ID != session.ID) {
							t.Fatalf("active Sessions at %v = %#v", test.at, active)
						}
					})
				}
			})
			for _, test := range sessionDecisionCases(deadline) {
				t.Run("EnforceExpiry/"+test.name, func(t *testing.T) {
					user, session, raw := saveSessionTimeFixture(t, ss, expiry, deadline)
					result, err := ss.Session().EnforceExpiry(context.Background(), session.ID.String(), user.ID.String(), test.at)
					requireNoError(t, err)
					if result == nil || result.Session == nil || result.Expired != test.expired {
						t.Fatalf("EnforceExpiry(%v) = %#v", test.at, result)
					}
					if !test.expired {
						if result.Session.RevokedAt.Valid || len(result.TokenHashes) != 0 {
							t.Fatal("expiry enforcement changed an active Session")
						}
						return
					}
					at := model.TimeUTC(test.at)
					if !result.Session.RevokedAt.Time.Equal(at) || len(result.TokenHashes) != 2 {
						t.Fatalf("expiry result lost decision precision: %#v", result)
					}
					credential, saved, err := ss.SessionCredential().GetSessionByTokenHash(
						context.Background(), model.HashToken(raw.access), model.SessionCredentialAccess,
					)
					requireNoError(t, err)
					if !credential.RevokedAt.Time.Equal(at) || !saved.RevokedAt.Time.Equal(at) {
						t.Fatalf("persisted expiry time = %v/%v, want %v", credential.RevokedAt, saved.RevokedAt, at)
					}
				})
				t.Run("UpdateActivity/"+test.name, func(t *testing.T) {
					_, session, _ := saveSessionTimeFixture(t, ss, expiry, deadline)
					idle := test.at.Add(time.Hour + 500*time.Microsecond)
					err := ss.Session().UpdateActivity(context.Background(), session.ID.String(), test.at, idle)
					if test.expired {
						if !store.IsNotFound(err) {
							t.Fatalf("UpdateActivity(expired) = %v", err)
						}
					} else {
						requireNoError(t, err)
					}
					saved, err := ss.Session().Get(context.Background(), session.ID.String())
					requireNoError(t, err)
					if test.expired {
						if !saved.LastActivityAt.Equal(session.LastActivityAt) || !saved.IdleExpiresAt.Equal(session.IdleExpiresAt) {
							t.Fatal("activity update revived an expired Session")
						}
						return
					}
					wantIdle := model.TimeUTC(idle)
					if session.ExpiresAt.Before(wantIdle) {
						wantIdle = session.ExpiresAt
					}
					if !saved.LastActivityAt.Equal(model.TimeUTC(test.at)) || !saved.IdleExpiresAt.Equal(wantIdle) {
						t.Fatalf("activity precision = %v/%v, want %v/%v", saved.LastActivityAt, saved.IdleExpiresAt, model.TimeUTC(test.at), wantIdle)
					}
				})
			}
		})
	}
	for _, expiry := range []string{"refresh", "idle", "absolute"} {
		t.Run("RotateRefresh/"+expiry, func(t *testing.T) {
			for _, test := range sessionDecisionCases(deadline) {
				t.Run(test.name, func(t *testing.T) {
					_, session, raw := saveSessionTimeFixture(t, ss, expiry, deadline)
					result, err := ss.SessionCredential().RotateRefresh(context.Background(), model.HashToken(raw.refresh),
						&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: test.at.Add(15 * time.Minute)},
						&model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: test.at.Add(time.Hour)},
						test.at, test.at.Add(time.Hour+500*time.Microsecond))
					if test.expired && expiry == "refresh" {
						var conflict *store.ErrConflict
						if !errors.As(err, &conflict) || result != nil {
							t.Fatalf("RotateRefresh(expired credential) = %#v, %v", result, err)
						}
						return
					}
					requireNoError(t, err)
					at := model.TimeUTC(test.at)
					if result == nil || result.Session == nil || result.Expired != test.expired || result.ReplayDetected {
						t.Fatalf("RotateRefresh(%v) = %#v", test.at, result)
					}
					if test.expired {
						if !result.Session.RevokedAt.Time.Equal(at) || result.AccessCredential != nil || result.RefreshCredential != nil {
							t.Fatalf("refresh expiry result = %#v", result)
						}
					} else if !result.AccessCredential.CreatedAt.Equal(at) || !result.RefreshCredential.CreatedAt.Equal(at) ||
						!result.Session.LastActivityAt.Equal(at) || result.Session.IdleExpiresAt.After(session.ExpiresAt) {
						t.Fatalf("refresh result lost native time or absolute cap: %#v", result)
					}
					old, saved, err := ss.SessionCredential().GetSessionByTokenHash(context.Background(), model.HashToken(raw.access), model.SessionCredentialAccess)
					requireNoError(t, err)
					if !old.RevokedAt.Time.Equal(at) || test.expired && !saved.RevokedAt.Time.Equal(at) {
						t.Fatalf("refresh revocation precision = %v/%v, want %v", old.RevokedAt, saved.RevokedAt, at)
					}
				})
			}
		})
	}
}

type sessionDecisionCase struct {
	name    string
	at      time.Time
	expired bool
}

func sessionDecisionCases(deadline time.Time) []sessionDecisionCase {
	return []sessionDecisionCase{
		{name: "before", at: deadline.Add(-time.Microsecond)},
		{name: "normalized before", at: deadline.Add(-time.Nanosecond)},
		{name: "at", at: deadline, expired: true},
		{name: "after", at: deadline.Add(time.Microsecond), expired: true},
		{name: "normalized at", at: deadline.Add(999 * time.Nanosecond).In(time.FixedZone("offset", 7200)), expired: true},
	}
}

func saveSessionTimeFixture(t *testing.T, ss store.Store, expiry string, deadline time.Time) (*model.User, *model.Session, rawSessionCredentials) {
	t.Helper()
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	session, credentials, raw := newSession(user.ID.String())
	session.ExpiresAt = deadline.Add(2 * time.Hour)
	session.IdleExpiresAt = deadline.Add(time.Hour)
	credentials[0].ExpiresAt = deadline
	credentials[1].ExpiresAt = session.ExpiresAt
	switch expiry {
	case "refresh":
		credentials[1].ExpiresAt = deadline
	case "idle":
		session.IdleExpiresAt = deadline
	case "absolute":
		session.IdleExpiresAt, session.ExpiresAt = deadline, deadline
		credentials[1].ExpiresAt = deadline
	}
	saved, _, err := ss.Session().Save(ctx, testSessionCreation(t, ctx, ss, session, credentials, 10))
	requireNoError(t, err)
	return user, saved, raw
}
