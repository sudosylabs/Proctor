// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIDProducesCanonicalUniqueIdentifiers(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := NewID()
		require.True(t, IsValidOpaqueID(id), "NewID() returned invalid identifier")
		_, exists := seen[id]
		require.False(t, exists, "NewID() returned duplicate identifier %q", id)
		seen[id] = struct{}{}
	}
}

func TestIsValidOpaqueIDRejectsNonCanonicalIdentifiers(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"short",
		"ybndrfg8ejkmcpqxot1uwisza!",
		"YBNDRFG8EJKMCPQXOT1UWISZA3",
		"00000000000000000000000000",
		"ééééééééééééé",
	} {
		assert.False(t, IsValidOpaqueID(value), "IsValidOpaqueID")
	}
}

func TestSanitizeUnicodeDropsBidirectionalControls(t *testing.T) {
	t.Parallel()

	const input = "Pro\u202Ector\u2066"
	require.Equal(t, "Proctor", SanitizeUnicode(input))
}
