// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
						active, err := ss.Session().ListActiveByUser(context.Background(), user.ID, test.at)
						require.NoError(t, err)
						require.False(t, test.expired && len(active) != 0, "active Sessions at %v = %#v", test.at, active)
						require.False(t, !test.expired && (len(active) != 1 || active[0].ID != session.ID), "active Sessions at %v = %#v", test.at, active)
					})
				}
			})
			for _, test := range sessionDecisionCases(deadline) {
				t.Run("EnforceExpiry/"+test.name, func(t *testing.T) {
					user, session, raw := saveSessionTimeFixture(t, ss, expiry, deadline)
					result, err := ss.Session().EnforceExpiry(context.Background(), &store.SessionExpiryEnforcement{ID: session.ID, UserID: user.ID, At: test.at})
					require.NoError(t, err)
					require.NotNil(t, result, "EnforceExpiry(%v)", test.at)
					require.NotNil(t, result.Session, "EnforceExpiry(%v)", test.at)
					require.Equal(t, test.expired, result.Expired, "EnforceExpiry(%v)", test.at)
					if !test.expired {
						require.False(t, result.Session.RevokedAt.Valid, "expiry enforcement changed an active Session")
						require.Empty(t, result.TokenHashes, "expiry enforcement changed an active Session")
						return
					}
					at := model.TimeUTC(test.at)
					require.True(t, result.Session.RevokedAt.Time.Equal(at))
					require.Len(t, result.TokenHashes, 2)
					credential, saved, err := ss.SessionCredential().GetSessionByTokenHash(
						context.Background(), model.HashToken(raw.access), model.SessionCredentialAccess,
					)
					require.NoError(t, err)
					require.True(t, credential.RevokedAt.Time.Equal(at), "persisted expiry time = %v/%v", credential.RevokedAt, saved.RevokedAt)
					require.True(t, saved.RevokedAt.Time.Equal(at), "persisted expiry time = %v/%v", credential.RevokedAt, saved.RevokedAt)
				})
				t.Run("UpdateActivity/"+test.name, func(t *testing.T) {
					_, session, _ := saveSessionTimeFixture(t, ss, expiry, deadline)
					idle := test.at.Add(time.Hour + 500*time.Microsecond)
					err := ss.Session().UpdateActivity(context.Background(), &store.SessionActivityUpdate{ID: session.ID, LastActivityAt: test.at, IdleExpiresAt: idle})
					if test.expired {
						require.True(t, store.IsNotFound(err), "UpdateActivity(expired)")
					} else {
						require.NoError(t, err)
					}
					saved, err := ss.Session().Get(context.Background(), session.ID)
					require.NoError(t, err)
					if test.expired {
						require.True(t, saved.LastActivityAt.Equal(session.LastActivityAt), "activity update revived an expired Session")
						require.True(t, saved.IdleExpiresAt.Equal(session.IdleExpiresAt), "activity update revived an expired Session")
						return
					}
					wantIdle := model.TimeUTC(idle)
					if session.ExpiresAt.Before(wantIdle) {
						wantIdle = session.ExpiresAt
					}
					require.True(t, saved.LastActivityAt.Equal(model.TimeUTC(test.at)), "activity precision")
					require.True(t, saved.IdleExpiresAt.Equal(wantIdle), "activity precision")
				})
			}
		})
	}
	for _, expiry := range []string{"refresh", "idle", "absolute"} {
		t.Run("RotateRefresh/"+expiry, func(t *testing.T) {
			for _, test := range sessionDecisionCases(deadline) {
				t.Run(test.name, func(t *testing.T) {
					_, session, raw := saveSessionTimeFixture(t, ss, expiry, deadline)
					result, err := ss.SessionCredential().RotateRefresh(context.Background(), &store.SessionRefreshRotation{TokenHash: model.HashToken(raw.refresh), Access: &model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: test.at.Add(15 * time.Minute)}, Refresh: &model.SessionCredential{TokenHash: model.HashToken(model.NewCredentialToken()), ExpiresAt: test.at.Add(time.Hour)}, Now: test.at, IdleExpiresAt: test.at.Add(time.Hour + 500*time.Microsecond)})
					if test.expired && expiry == "refresh" {
						var conflict *store.ErrConflict
						require.ErrorAs(t, err, &conflict, "RotateRefresh(expired credential)")
						require.Nil(t, result, "RotateRefresh(expired credential)")
						return
					}
					require.NoError(t, err)
					at := model.TimeUTC(test.at)
					require.NotNil(t, result, "RotateRefresh(%v)", test.at)
					require.NotNil(t, result.Session, "RotateRefresh(%v)", test.at)
					require.Equal(t, test.expired, result.Expired, "RotateRefresh(%v)", test.at)
					require.False(t, result.ReplayDetected, "RotateRefresh(%v)", test.at)
					if test.expired {
						require.True(t, result.Session.RevokedAt.Time.Equal(at))
						require.Nil(t, result.AccessCredential)
						require.Nil(t, result.RefreshCredential)
					} else {
						require.True(t, result.AccessCredential.CreatedAt.Equal(at), "refresh access credential lost native time")
						require.True(t, result.RefreshCredential.CreatedAt.Equal(at), "refresh credential lost native time")
						require.True(t, result.Session.LastActivityAt.Equal(at), "refresh Session activity lost native time")
						require.False(t, result.Session.IdleExpiresAt.After(session.ExpiresAt), "refresh idle expiry passed the absolute cap")
					}
					old, saved, err := ss.SessionCredential().GetSessionByTokenHash(context.Background(), model.HashToken(raw.access), model.SessionCredentialAccess)
					require.NoError(t, err)
					require.True(t, old.RevokedAt.Time.Equal(at), "refresh revocation precision = %v/%v", old.RevokedAt, saved.RevokedAt)
					require.False(t, test.expired && !saved.RevokedAt.Time.Equal(at), "refresh revocation precision = %v/%v", old.RevokedAt, saved.RevokedAt)
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
	saved, _, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, session, credentials, 10))
	require.NoError(t, err)
	return user, saved, raw
}
