// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"
)

func TestSessionReauthenticationPreservesAssuranceHistory(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 7, 10, 0, 0, 123456000, time.UTC)
	session := &Session{UserID: NewUserID(), ClientType: SessionClientWeb, AuthenticationMethod: "password",
		AuthenticationStrength: AuthenticationSingleFactor, AuthenticatedAt: at,
		LastActivityAt: at, IdleExpiresAt: at.Add(time.Hour), ExpiresAt: at.Add(24 * time.Hour)}
	session.PrepareCreate(NewSessionID(), at)
	session.UpdatedAt = at.Add(30 * time.Minute)
	session.ReauthenticatedAt = OptionalTimeFrom(session.UpdatedAt)
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	principal := Principal{UserID: session.UserID, SessionID: session.ID, CredentialID: PrincipalCredentialID(NewId()),
		CredentialType: CredentialSessionAccess, ClientType: SessionClientWeb, AuthenticationMethod: "password",
		AuthenticationStrength: AuthenticationSingleFactor, AuthenticatedAt: at, ReauthenticatedAt: session.ReauthenticatedAt}
	if principal.Validate() != nil || !principal.IsRecentlyAuthenticated(session.UpdatedAt, time.Minute) || principal.HasStrongAuthentication() {
		t.Fatal("fresh primary proof did not supply recency without supplying MFA")
	}
	principal.MFACompletedAt = OptionalTimeFrom(at.Add(31 * time.Minute))
	principal.AuthenticationStrength = AuthenticationMultiFactor
	if !principal.LastAuthenticationAt().Equal(principal.MFACompletedAt.Time) {
		t.Fatal("later MFA proof lost")
	}
	if principal.IsRecentlyAuthenticated(at, time.Hour) {
		t.Fatal("future proof granted recency")
	}
	for _, invalid := range []time.Time{at.Add(-time.Microsecond), session.UpdatedAt.Add(time.Microsecond)} {
		session.ReauthenticatedAt = OptionalTimeFrom(invalid)
		if session.Validate() == nil {
			t.Fatalf("invalid refresh accepted: %v", invalid)
		}
	}
}
