// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"strings"
	"testing"
)

func TestReadPrivatePasswordBoundsAndNormalizesInput(t *testing.T) {
	t.Parallel()

	value, err := readPrivatePassword(strings.NewReader("private-value\r\n"))
	if err != nil || value != "private-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	for _, input := range []string{"", strings.Repeat("x", 4097)} {
		if _, err := readPrivatePassword(strings.NewReader(input)); err == nil {
			t.Fatalf("readPrivatePassword(%d bytes) error = nil", len(input))
		}
	}
	if _, err := readPrivatePassword(nil); err == nil {
		t.Fatal("readPrivatePassword(nil) error = nil")
	}
}
