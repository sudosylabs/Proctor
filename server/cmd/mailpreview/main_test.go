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
	for _, key := range []string{"identity.verify_email", "identity.password_reset", "identity.password_changed"} {
		if _, err := os.Stat(filepath.Join(first, key+".html")); err != nil {
			t.Fatalf("%s HTML preview: %v", key, err)
		}
		if _, err := os.Stat(filepath.Join(first, key+".txt")); err != nil {
			t.Fatalf("%s text preview: %v", key, err)
		}
	}
}
