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

func testPersonalAccessTokenNativeExpiry(t *testing.T, ss store.Store) {
	ctx := context.Background()
	saveInstitution(t, ctx, ss)
	user, _ := saveLocalUser(t, ctx, ss)
	raw := model.NewCredentialToken()
	candidate := newPersonalAccessToken(user.ID.String(), raw)
	deadline := model.NowUTC().Truncate(time.Millisecond).Add(time.Hour + 700*time.Microsecond)
	candidate.ExpiresAt = deadline
	saved, err := createPersonalAccessTokenWithNotice(t, ctx, ss, candidate, 10)
	requireNoError(t, err)
	if !saved.ExpiresAt.Equal(deadline) {
		t.Fatalf("token deadline = %s, want %s", saved.ExpiresAt, deadline)
	}
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
				if !store.IsNotFound(err) {
					t.Fatalf("expired Resolve() = %v, want not found", err)
				}
				return
			}
			requireNoError(t, err)
			if !resolved.Token.LastUsedAt.Valid || !resolved.Token.LastUsedAt.Time.Equal(deadline.Add(test.lastOffset)) {
				t.Fatalf("last use = %v, want %s", resolved.Token.LastUsedAt, deadline.Add(test.lastOffset))
			}
		})
	}
}

func testMFANativeExpiry(t *testing.T, ss store.Store) {
	t.Run("invalid decision instants", func(t *testing.T) {
		ctx := context.Background()
		for _, at := range []time.Time{{}, time.Unix(-1, 0), time.Unix(0, 0), time.Unix(0, 1)} {
			userID, sessionID := model.NewUserID(), model.NewSessionID()
			_, err := ss.MFA().Activate(ctx, &store.MFAActivationMutation{
				CredentialID: model.NewMFACredentialID().String(), UserID: userID.String(),
				SessionID: sessionID.String(), At: at, TimeStep: 1,
				RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
				AuditEventID:  model.NewAuditEventID().String(), AuditAt: model.GetMillis(),
			})
			var invalid *store.ErrInvalidInput
			if !errors.As(err, &invalid) || invalid.Entity != "mfa_credential" || invalid.Field != "activate" {
				t.Fatalf("Activate(%s) = %v, want invalid decision instant", at, err)
			}
			_, err = ss.MFA().UpgradeSession(ctx, sessionID.String(), userID.String(), at)
			if !errors.As(err, &invalid) || invalid.Entity != "session" || invalid.Field != "upgrade_mfa" {
				t.Fatalf("UpgradeSession(%s) = %v, want invalid decision instant", at, err)
			}
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
				session, savedCredentials, err := ss.Session().Save(ctx, testSessionCreation(t, ctx, ss, candidate, credentials, 10))
				requireNoError(t, err)
				at := deadline.Add(point.offset).In(time.FixedZone("test", 2*60*60))
				if operation == "Session upgrade" {
					_, err = ss.MFA().UpgradeSession(ctx, session.ID.String(), user.ID.String(), at)
				} else {
					pending, saveErr := ss.MFA().SavePending(ctx, &model.MFACredential{
						UserID: user.ID, State: model.MFAStatePending,
						EncryptedSecret: "encrypted-secret", EncryptionKeyID: "0123456789abcdef0123456789abcdef",
						CreatedAt: model.NowUTC(), PendingExpiresAt: model.OptionalTimeFrom(deadline),
					})
					requireNoError(t, saveErr)
					audit, notice := mfaSecurityNoticeFixture(t, ctx, ss, user, model.MailTemplateIdentityMFAEnabled, at.UnixMilli())
					_, err = ss.MFA().Activate(ctx, &store.MFAActivationMutation{
						Principal: mfaSessionPrincipal(session, savedCredentials[0]), RecentAuthenticationTTL: time.Hour,
						CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: time.Now().Unix() / 30,
						RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken(model.NewCredentialToken())}},
						SessionID:     session.ID.String(), At: at, AuditEventID: audit.ID.String(), AuditAt: at.UnixMilli(), Notice: notice,
					})
					if !point.valid {
						current, getErr := ss.MFA().GetByUser(ctx, user.ID.String())
						requireNoError(t, getErr)
						if current.State != model.MFAStatePending {
							t.Fatal("expired activation changed the pending credential")
						}
					}
				}
				if !point.valid {
					if !store.IsNotFound(err) {
						t.Fatalf("expired MFA operation = %v, want not found", err)
					}
				} else {
					requireNoError(t, err)
				}
				current, getErr := ss.Session().Get(ctx, session.ID.String())
				requireNoError(t, getErr)
				if point.valid {
					if current.AuthenticationStrength != model.AuthenticationMultiFactor || (operation == "Session upgrade" && !current.MFACompletedAt.Time.Equal(model.TimeUTC(at))) || (operation == "activation" && (current.MFACompletedAt.Time.Before(session.CreatedAt) || current.MFACompletedAt.Time.After(model.NowUTC()))) {
						t.Fatal("MFA upgrade lost its native decision instant")
					}
				} else if current.AuthenticationStrength != session.AuthenticationStrength || current.MFACompletedAt.Valid {
					t.Fatal("expired MFA operation changed Session assurance")
				}
			})
		}
	}
}
