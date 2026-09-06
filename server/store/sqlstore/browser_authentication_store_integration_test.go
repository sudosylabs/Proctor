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
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "desktop-clock", Email: "desktop-clock@example.edu"})
	password := passwordProofForSQLTest(t, ctx, persistence, user.ID)
	identity, err := persistence.ExternalIdentity().Save(ctx, &model.ExternalIdentity{
		UserID: user.ID, Provider: "campus-oidc", Subject: "desktop-clock",
	})
	if err != nil {
		t.Fatal(err)
	}
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
			if err != nil {
				t.Fatal(err)
			}
			binding, err := bindDesktopAuthorizationForSQLTest(ctx, persistence, handle, proof, state)
			if err != nil {
				t.Fatal(err)
			}
			var before time.Time
			if err = persistence.GetMaster().Get(ctx, &before, `SELECT clock_timestamp()`); err != nil {
				t.Fatal(err)
			}
			input := &store.DesktopAuthorizationAuthentication{
				BindingHash: model.HashToken(binding), UserID: user.ID, AuthenticationMethod: "password",
				PasswordProof: password, AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: before.Add(test.authOffset).UnixMilli(), Capabilities: capabilities,
			}
			if test.external {
				input.AuthenticationMethod, input.AuthenticationProviderID, input.ExternalIdentityID = "oidc", "campus-oidc", identity.ID
				input.PasswordProof = store.PasswordCredentialProof{}
			}
			if test.mfa {
				input.AuthenticationStrength = model.AuthenticationMultiFactor
				input.MFACompletedAt = before.Add(test.mfaOffset).UnixMilli()
			}
			result, err := persistence.BrowserAuthentication().AuthenticateDesktopAuthorization(ctx, input)
			if err != nil || result == nil || result.Denied {
				t.Fatalf("AuthenticateDesktopAuthorization() = %#v, %v", result, err)
			}
			var after time.Time
			if err = persistence.GetMaster().Get(ctx, &after, `SELECT clock_timestamp()`); err != nil {
				t.Fatal(err)
			}
			var row browserAuthenticationRow
			if err = persistence.GetMaster().Get(ctx, &row, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, created.ID.String()); err != nil {
				t.Fatal(err)
			}
			authenticated, err := row.model()
			if err != nil {
				t.Fatalf("AuthenticateDesktopAuthorization committed invalid state: %v", err)
			}
			if authenticated.UpdatedAt.Before(before) || authenticated.UpdatedAt.After(after) || !authenticated.ExpiresAt.Equal(created.ExpiresAt) {
				t.Fatal("authentication moved the database transition time or transaction deadline")
			}
			wantAuth := authenticated.UpdatedAt
			wantMFA := model.OptionalTime{}
			if test.external && model.TimeFromMillis(input.AuthenticatedAt).Before(wantAuth) {
				wantAuth = model.TimeFromMillis(input.AuthenticatedAt)
			}
			if test.mfa {
				wantMFA = model.OptionalTimeFrom(authenticated.UpdatedAt)
				if test.external && model.TimeFromMillis(input.MFACompletedAt).Before(wantMFA.Time) {
					wantMFA = model.OptionalTimeFromMillis(input.MFACompletedAt)
				}
			}
			if !authenticated.AuthenticatedAt.Time.Equal(wantAuth) || authenticated.MFACompletedAt != wantMFA {
				t.Fatalf("authentication provenance = %s, %v; want %s, %v", authenticated.AuthenticatedAt.Time,
					authenticated.MFACompletedAt, wantAuth, wantMFA)
			}
			audit := saveDesktopAuthorizationAuditForSQLTest(t, ctx, persistence, institution.ID, user.ID, "clock-issue")
			issued, err := persistence.BrowserAuthentication().IssueCode(ctx, &store.DesktopAuthorizationCodeIssue{
				BindingHash: model.HashToken(binding), StateHash: model.HashToken(state), CodeHash: model.HashToken(model.NewCredentialToken()),
				ExpectedUserID: user.ID, CodeLifetime: 45 * time.Second, Capabilities: capabilities,
				AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
			})
			if err != nil || issued == nil {
				t.Fatalf("IssueCode() = %#v, %v", issued, err)
			}
			if err = persistence.GetMaster().Get(ctx, &row, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions WHERE id=?`, created.ID.String()); err != nil {
				t.Fatal(err)
			}
			codeIssued, err := row.model()
			if err != nil {
				t.Fatal(err)
			}
			if !codeIssued.AuthenticatedAt.Time.Equal(wantAuth) || codeIssued.MFACompletedAt != wantMFA ||
				!codeIssued.ExpiresAt.Equal(created.ExpiresAt) || issued.CodeExpiresAt.After(created.ExpiresAt) ||
				!issued.CodeExpiresAt.Equal(codeIssued.UpdatedAt.Add(45*time.Second)) {
				t.Fatal("code issuance changed authentication provenance or extended its database-clock deadline")
			}
		})
	}
}
