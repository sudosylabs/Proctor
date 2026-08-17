// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunWritesDeterministicRepresentativePreview(t *testing.T) {
	t.Parallel()

	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, output := range []string{first, second} {
		var stderr bytes.Buffer
		if err := run([]string{"-output", output}, &stderr); err != nil {
			t.Fatalf("run(%q): %v (%s)", output, err, stderr.String())
		}
	}

	firstIndex, err := os.ReadFile(filepath.Join(first, "index.html"))
	if err != nil {
		t.Fatalf("read first index: %v", err)
	}
	secondIndex, err := os.ReadFile(filepath.Join(second, "index.html"))
	if err != nil {
		t.Fatalf("read second index: %v", err)
	}
	if !bytes.Equal(firstIndex, secondIndex) {
		t.Fatal("preview index is not deterministic")
	}
	if bytes.Contains(firstIndex, []byte("@")) {
		t.Fatal("preview index appears to contain a production-like email address")
	}
	if _, err := os.Stat(filepath.Join(first, "identity.verify_email.html")); err != nil {
		t.Fatalf("representative HTML preview: %v", err)
	}
	if _, err := os.Stat(filepath.Join(first, "identity.verify_email.txt")); err != nil {
		t.Fatalf("representative text preview: %v", err)
	}
}
