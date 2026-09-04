// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"strings"
	"testing"
)

func TestReadPrivatePasswordBoundsAndNormalizesInput(t *testing.T) {
	t.Parallel()

	value, err := readPrivatePassword(strings.NewReader("private-value\r\n"), englishCommandText())
	if err != nil || value != "private-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	for _, input := range []string{"", strings.Repeat("x", 4097)} {
		if _, err := readPrivatePassword(strings.NewReader(input), englishCommandText()); err == nil {
			t.Fatalf("readPrivatePassword(%d bytes) error = nil", len(input))
		}
	}
	if _, err := readPrivatePassword(nil, englishCommandText()); err == nil {
		t.Fatal("readPrivatePassword(nil) error = nil")
	}
}
