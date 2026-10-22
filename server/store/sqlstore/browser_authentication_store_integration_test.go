//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestDesktopAuthorizationAuthenticationClock(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	seedTestAuthenticationPolicy(t, persistence, map[string]model.ProviderAdmissionMode{
		"campus-oidc": model.ProviderAdmissionLinkedOnly,
	})
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "desktop-clock", DisplayName: "Desktop Clock"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-clock", Email: "desktop-clock@example.edu"})
	password := passwordProofForSQLTest(t, ctx, persistence, user.ID)
	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: user.ID, Provider: "campus-oidc", Subject: "desktop-clock",
	})
	require.NoError(t, err)
	capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-oidc": {}}}
	for _, test := range []struct {
		name       string
		external   bool
		authOffset time.Duration
		mfaOffset  time.Duration
		mfa        bool
	}{
		{name: "fresh password with future node clock", authOffset: 2 * time.Hour},
		{name: "fresh password MFA with past node clock", authOffset: -2 * time.Hour, mfaOffset: -time.Hour, mfa: true},
		{name: "fresh password MFA with future node clock", authOffset: time.Hour, mfaOffset: 2 * time.Hour, mfa: true},
		{name: "old external authentication", external: true, authOffset: -time.Hour},
		{name: "old external MFA", external: true, authOffset: -time.Hour, mfaOffset: -30 * time.Minute, mfa: true},
		{name: "future external MFA", external: true, authOffset: time.Minute, mfaOffset: 2 * time.Minute, mfa: true},
		{name: "old external authentication with future MFA", external: true, authOffset: -time.Hour, mfaOffset: time.Minute, mfa: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transaction, handle, proof, state, _ := desktopAuthorizationTransactionForSQLTest(model.NowUTC(), institution.ID)
			created, err := persistence.BrowserAuthentication().CreateDesktopAuthorization(ctx, transaction)
			require.NoError(t, err)
			binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
			require.NoError(t, err)
			var before time.Time
			err = persistence.GetMaster().Get(ctx, &before, `SELECT clock_timestamp()`)
			require.NoError(t, err)
			input := &store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(binding), UserID: user.ID, AuthenticationMethod: "password",
				PasswordProof: password, AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: before.Add(test.authOffset), Capabilities: capabilities,
			}
			if test.external {
				input.AuthenticationMethod, input.AuthenticationProviderID, input.ExternalIdentityID = "oidc", "campus-oidc", identity.ID
				input.PasswordProof = store.PasswordCredentialProof{}
			}
			if test.mfa {
				input.AuthenticationStrength = model.AuthenticationMultiFactor
				input.MFACompletedAt = model.OptionalTimeFrom(before.Add(test.mfaOffset))
			}
			result, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, input)
			require.NoError(t, err, "AuthenticateDesktopAuthorization()")
			require.NotNil(t, result, "AuthenticateDesktopAuthorization()")
			require.False(t, result.Denied, "AuthenticateDesktopAuthorization()")
			var after time.Time
			err = persistence.GetMaster().Get(ctx, &after, `SELECT clock_timestamp()`)
			require.NoError(t, err)
			var row browserAuthenticationRow
			err = persistence.GetMaster().Get(ctx, &row, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, created.ID.String())
			require.NoError(t, err)
			authenticated, err := row.model()
			require.NoError(t, err)
			require.False(t, authenticated.UpdatedAt.Before(before), "authentication moved the database transition time or transaction deadline")
			require.False(t, authenticated.UpdatedAt.After(after), "authentication moved the database transition time or transaction deadline")
			require.True(t, authenticated.ExpiresAt.Equal(created.ExpiresAt), "authentication moved the database transition time or transaction deadline")
			wantAuth := authenticated.UpdatedAt
			wantMFA := model.OptionalTime{}
			if test.external && model.TimeUTC(input.AuthenticatedAt).Before(wantAuth) {
				wantAuth = model.TimeUTC(input.AuthenticatedAt)
			}
			if test.mfa {
				wantMFA = model.OptionalTimeFrom(authenticated.UpdatedAt)
				if test.external && input.MFACompletedAt.Time.Before(wantMFA.Time) {
					wantMFA = input.MFACompletedAt
				}
			}
			require.True(t, authenticated.AuthenticatedAt.Time.Equal(wantAuth), "authentication provenance = %s", authenticated.AuthenticatedAt.Time)
			require.Equal(t, wantMFA, authenticated.MFACompletedAt, "authentication provenance = %s", authenticated.AuthenticatedAt.Time)
			audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "clock-issue")
			issued, err := persistence.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
				BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(model.NewCredentialToken()),
				ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second, Capabilities: capabilities,
				AuditEventID: audit.ID, AuditAt: model.NowUTC(),
			})
			require.NoError(t, err, "IssueCode()")
			require.NotNil(t, issued, "IssueCode()")
			err = persistence.GetMaster().Get(ctx, &row, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, created.ID.String())
			require.NoError(t, err)
			codeIssued, err := row.model()
			require.NoError(t, err)
			require.True(t, codeIssued.AuthenticatedAt.Time.Equal(wantAuth), "code issuance changed authentication provenance or extended its database-clock deadline")
			require.Equal(t, wantMFA, codeIssued.MFACompletedAt, "code issuance changed authentication provenance or extended its database-clock deadline")
			require.True(t, codeIssued.ExpiresAt.Equal(created.ExpiresAt), "code issuance changed authentication provenance or extended its database-clock deadline")
			require.False(t, issued.CodeExpiresAt.After(created.ExpiresAt), "code issuance changed authentication provenance or extended its database-clock deadline")
			require.True(t, issued.CodeExpiresAt.Equal(codeIssued.UpdatedAt.Add(45*time.Second)), "code issuance changed authentication provenance or extended its database-clock deadline")
		})
	}
}
