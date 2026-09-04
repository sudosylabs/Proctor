// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "testing"

func TestNewIdProducesCanonicalUniqueIdentifiers(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := NewId()
		if !IsValidId(id) {
			t.Fatalf("NewId() returned invalid identifier %q", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("NewId() returned duplicate identifier %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestIsValidIdRejectsNonCanonicalIdentifiers(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"short",
		"ybndrfg8ejkmcpqxot1uwisza!",
		"YBNDRFG8EJKMCPQXOT1UWISZA3",
		"00000000000000000000000000",
		"ééééééééééééé",
	} {
		if IsValidId(value) {
			t.Errorf("IsValidId(%q) = true", value)
		}
	}
}

func TestSanitizeUnicodeDropsBidirectionalControls(t *testing.T) {
	t.Parallel()

	const input = "Pro\u202Ector\u2066"
	if got := SanitizeUnicode(input); got != "Proctor" {
		t.Fatalf("SanitizeUnicode() = %q", got)
	}
}
