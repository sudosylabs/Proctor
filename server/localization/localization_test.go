// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package localization

import (
	"testing"
	"testing/fstest"
)

func TestLocalizerFallsBackPerMessageAndInterpolates(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"en.json": {Data: []byte(`[
  {"id":"greeting","translation":"Hello {{.Name}}"},
  {"id":"only.english","translation":"English only"}
]`)},
		"fr.json": {Data: []byte(`[
  {"id":"greeting","translation":"Bonjour {{.Name}}"}
]`)},
	}
	localizer, err := New(files, "en")
	if err != nil {
		t.Fatal(err)
	}
	translated, err := localizer.Resolve("fr-CA", "greeting", struct{ Name string }{"Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if translated.Locale != "fr" || translated.Text != "Bonjour Ada" {
		t.Fatalf("translation = %#v", translated)
	}
	fallback, err := localizer.Resolve("fr", "only.english", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Locale != "en" || fallback.Text != "English only" {
		t.Fatalf("fallback = %#v", fallback)
	}
}

func TestLocalizerRejectsInvalidCatalogContracts(t *testing.T) {
	t.Parallel()
	tests := map[string]fstest.MapFS{
		"nested shape": {"en.json": {Data: []byte(`{"greeting":"hello"}`)}},
		"unsorted": {"en.json": {Data: []byte(`[
  {"id":"z","translation":"last"},
  {"id":"a","translation":"first"}
]`)}},
		"unknown translated id": {
			"en.json": {Data: []byte(`[{"id":"known","translation":"Known"}]`)},
			"fr.json": {Data: []byte(`[{"id":"unknown","translation":"Inconnu"}]`)},
		},
		"placeholder mismatch": {
			"en.json": {Data: []byte(`[{"id":"hello","translation":"Hello {{.Name}}"}]`)},
			"fr.json": {Data: []byte(`[{"id":"hello","translation":"Bonjour {{.User}}"}]`)},
		},
	}
	for name, files := range tests {
		name, files := name, files
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(files, "en"); err == nil {
				t.Fatal("invalid catalog was accepted")
			}
		})
	}
}
