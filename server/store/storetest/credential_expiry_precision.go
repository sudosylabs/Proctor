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

func testPersonalAccessTokenNativeExpiry(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	raw := model.NewCredentialToken()
	candidate := newPersonalAccessToken(user.ID.String(), raw)
	deadline := model.NowUTC().Truncate(time.Millisecond).Add(time.Hour + 700*time.Microsecond)
	candidate.ExpiresAt = deadline
	saved, err := createPersonalAccessTokenWithNotice(t, ctx, ss, candidate, 10)
	require.NoError(t, err)
	require.True(t, saved.ExpiresAt.Equal(deadline), "token deadline = %s", saved.ExpiresAt)
	for _, test := range []struct {
		name       string
		offset     time.Duration
		lastOffset time.Duration
		valid      bool
	}{
		{name: "first use", offset: -400 * time.Microsecond, lastOffset: -400 * time.Microsecond, valid: true},
		{name: "debounced", offset: -201 * time.Microsecond, lastOffset: -400 * time.Microsecond, valid: true},
		{name: "update threshold", offset: -200 * time.Microsecond, lastOffset: -200 * time.Microsecond, valid: true},
		{name: "nanosecond normalization", offset: -time.Nanosecond, lastOffset: -200 * time.Microsecond, valid: true},
		{name: "at deadline"},
		{name: "after deadline", offset: time.Microsecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			at := deadline.Add(test.offset).In(time.FixedZone("test", 2*60*60))
			resolved, err := ss.PersonalAccessToken().Resolve(ctx, model.HashToken(raw), at, 200*time.Microsecond)
			if !test.valid {
				require.True(t, store.IsNotFound(err), "expired Resolve()")
				return
			}
			require.NoError(t, err)
			require.True(t, resolved.Token.LastUsedAt.Valid, "last use = %v", resolved.Token.LastUsedAt)
			require.True(t, resolved.Token.LastUsedAt.Time.Equal(deadline.Add(test.lastOffset)), "last use = %v", resolved.Token.LastUsedAt)
		})
	}
}

func testMFANativeExpiry(t *testing.T, ss store.Store) {
	t.Run("invalid decision instants", func(t *testing.T) {
		ctx := context.Background()
		for _, at := range []time.Time{{}, time.Unix(-1, 0), time.Unix(0, 0), time.Unix(0, 1)} {
			userID, sessionID := model.NewUserID(), model.NewSessionID()
			_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
				CredentialID: model.NewMFACredentialID(), UserID: userID,
				SessionID: sessionID, At: at, TimeStep: 1,
				RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
				AuditEventID:  model.NewAuditEventID(), AuditAt: model.NowUTC(),
			})
			var invalid *store.ErrInvalidInput
			require.ErrorAs(t, err, &invalid, "Activate(%s)", at)
			require.Equal(t, "mfa_credential", invalid.Entity, "Activate(%s)", at)
			require.Equal(t, "activate", invalid.Field, "Activate(%s)", at)
			_, err = ss.MFA().UpgradeSession(ctx, sessionID, userID, at)
			require.ErrorAs(t, err, &invalid, "UpgradeSession(%s)", at)
			require.Equal(t, "session", invalid.Entity, "UpgradeSession(%s)", at)
			require.Equal(t, "upgrade_mfa", invalid.Field, "UpgradeSession(%s)", at)
		}
	})
	for _, operation := range []string{"activation", "Session upgrade"} {
		for _, point := range []struct {
			name   string
			offset time.Duration
			valid  bool
		}{
			{name: "before", offset: -time.Microsecond, valid: true},
			{name: "nanosecond normalization", offset: -time.Nanosecond, valid: true},
			{name: "at deadline"},
			{name: "after", offset: time.Microsecond},
		} {
			t.Run(operation+"/"+point.name, func(t *testing.T) {
				ctx := context.Background()
				user := saveUser(t, ctx, ss)
				deadline := model.NowUTC().Truncate(time.Millisecond).Add(time.Minute + 700*time.Microsecond)
				candidate, credentials, _ := newSession(user.ID.String())
				if operation == "Session upgrade" {
					candidate.IdleExpiresAt = deadline
				}
				session, savedCredentials, err := SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, candidate, credentials, 10))
				require.NoError(t, err)
				at := deadline.Add(point.offset).In(time.FixedZone("test", 2*60*60))
				if operation == "Session upgrade" {
					_, err = ss.MFA().UpgradeSession(ctx, session.ID, user.ID, at)
				} else {
					pending, saveErr := ss.MFA().SeedPending(ctx, &model.MFACredential{
						UserID: user.ID, State: model.MFAStatePending,
						EncryptedSecret: "encrypted-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef",
						CreatedAt: model.NowUTC(), PendingExpiresAt: model.OptionalTimeFrom(deadline),
					})
					require.NoError(t, saveErr)
					audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at)
					_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
						Principal: mfaSessionPrincipal(session, savedCredentials[0]), RecentAuthenticationTTL: time.Hour,
						CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
						RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
						SessionID:     session.ID, At: at, AuditEventID: audit.ID, AuditAt: at, Notice: notice,
					})
					if !point.valid {
						current, getErr := ss.MFA().GetByUser(ctx, user.ID)
						require.NoError(t, getErr)
						require.Equal(t, model.MFAStatePending, current.State, "expired activation changed the pending credential")
					}
				}
				if !point.valid {
					require.True(t, store.IsNotFound(err), "expired MFA operation")
				} else {
					require.NoError(t, err)
				}
				current, getErr := ss.Session().Get(ctx, session.ID)
				require.NoError(t, getErr)
				if point.valid {
					require.Equal(t, model.AuthenticationMultiFactor, current.AuthenticationStrength, "MFA upgrade lost its native decision instant")
					require.False(t, operation == "Session upgrade" && !current.MFACompletedAt.Time.Equal(model.TimeUTC(at)), "MFA upgrade lost its native decision instant")
					require.False(t, operation == "activation" && (current.MFACompletedAt.Time.Before(session.CreatedAt) || current.MFACompletedAt.Time.After(model.NowUTC())), "MFA upgrade lost its native decision instant")
				} else {
					require.Equal(t, session.AuthenticationStrength, current.AuthenticationStrength, "expired MFA operation changed Session assurance")
					require.False(t, current.MFACompletedAt.Valid, "expired MFA operation recorded MFA completion")
				}
			})
		}
	}
}
