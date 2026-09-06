// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRunWritesDeterministicRepresentativePreview(t *testing.T) {
	t.Parallel()

	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	for _, output := range []string{first, second} {
		var stderr bytes.Buffer
		if err := run([]string{"-output", output, "-catalogs", "../../i18n", "-templates", "../../templates"}, &stderr); err != nil {
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
	wantImage, err := os.ReadFile("../../templates/proctor-lockup-25d-v1.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{first, second} {
		image, err := os.ReadFile(filepath.Join(output, "proctor-lockup-25d-v1.png"))
		if err != nil || !bytes.Equal(image, wantImage) {
			t.Fatalf("preview image differs from released mail asset: %v", err)
		}
	}
	keys := model.AllMailTemplateKeys()
	if len(keys) != 44 {
		t.Fatalf("preview catalog keys = %d, want 44", len(keys))
	}
	for _, catalogKey := range keys {
		key := string(catalogKey)
		body, err := os.ReadFile(filepath.Join(first, key+".html"))
		if err != nil {
			t.Fatalf("%s HTML preview: %v", key, err)
		}
		if !strings.Contains(string(body), `src="proctor-lockup-25d-v1.png"`) || strings.Contains(string(body), `src="cid:`) {
			t.Fatalf("%s preview image does not resolve locally", key)
		}
		if _, err := os.Stat(filepath.Join(first, key+".txt")); err != nil {
			t.Fatalf("%s text preview: %v", key, err)
		}
		if bytes.Count(firstIndex, []byte(key+".html")) != 1 || bytes.Count(firstIndex, []byte(key+".txt")) != 1 {
			t.Fatalf("%s preview links are incomplete or duplicated", key)
		}
	}
}
