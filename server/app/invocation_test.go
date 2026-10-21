// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestInvocationCarriesPrincipalAndSafeMetadataImmutably(t *testing.T) {
	t.Parallel()

	principal := model.Principal{
		UserID:           model.NewUserID(),
		SessionID:        model.NewSessionID(),
		CredentialID:     model.PrincipalCredentialID(model.NewID()),
		CredentialType:   model.CredentialSessionAccess,
		CredentialScopes: []string{"scope-a"},
	}
	metadata := model.RequestMetadata{
		RequestID: "req-1",
		IPAddress: "127.0.0.1",
		UserAgent: "proctor-test",
	}

	invocation := app.NewInvocation(principal, metadata)
	require.Equal(t, principal, invocation.Principal())
	got := invocation.RequestMetadata()
	require.Equal(t, metadata, got, "RequestMetadata()")

	// Mutating the source metadata after construction must not affect the
	// invocation; the value is a snapshot.
	metadata.RequestID = "mutated"
	principal.CredentialScopes[0] = "mutated"
	require.Equal(t, "req-1", invocation.RequestMetadata().RequestID, "Invocation retained a live reference to caller metadata")
	gotPrincipal := invocation.Principal()
	gotPrincipal.CredentialScopes[0] = "returned-value-mutated"
	require.Equal(t, "scope-a", invocation.Principal().CredentialScopes[0], "Invocation exposed mutable principal scope storage")
}
