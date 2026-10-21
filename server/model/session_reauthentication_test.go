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

	"github.com/stretchr/testify/require"
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
	require.NoError(t, session.Validate())
	principal := Principal{UserID: session.UserID, SessionID: session.ID, CredentialID: PrincipalCredentialID(NewID()),
		CredentialType: CredentialSessionAccess, ClientType: SessionClientWeb, AuthenticationMethod: "password",
		AuthenticationStrength: AuthenticationSingleFactor, AuthenticatedAt: at, ReauthenticatedAt: session.ReauthenticatedAt}
	require.NoError(t, principal.Validate(), "fresh primary proof did not supply recency without supplying MFA")
	require.True(t, principal.IsRecentlyAuthenticated(session.UpdatedAt, time.Minute), "fresh primary proof did not supply recency without supplying MFA")
	require.False(t, principal.HasStrongAuthentication(), "fresh primary proof did not supply recency without supplying MFA")
	principal.MFACompletedAt = OptionalTimeFrom(at.Add(31 * time.Minute))
	principal.AuthenticationStrength = AuthenticationMultiFactor
	require.True(t, principal.LastAuthenticationAt().Equal(principal.MFACompletedAt.Time), "later MFA proof lost")
	require.False(t, principal.IsRecentlyAuthenticated(at, time.Hour), "future proof granted recency")
	for _, invalid := range []time.Time{at.Add(-time.Microsecond), session.UpdatedAt.Add(time.Microsecond)} {
		session.ReauthenticatedAt = OptionalTimeFrom(invalid)
		require.Error(t, session.Validate(), "invalid refresh accepted: %v", invalid)
	}
}
