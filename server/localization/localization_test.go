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
	locales := localizer.SupportedLocales()
	if localizer.DefaultLocale() != "en" || len(locales) != 2 || locales[0] != "en" || locales[1] != "fr" {
		t.Fatalf("locale metadata = default %q supported %#v", localizer.DefaultLocale(), locales)
	}
	locales[0] = "changed"
	if localizer.SupportedLocales()[0] != "en" {
		t.Fatal("supported locales exposed mutable localizer state")
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
	if _, err := localizer.Resolve("en", "greeting", struct{}{}); err == nil {
		t.Fatal("missing interpolation data was accepted")
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

func TestLocalizerValidatesConsumerDefinitions(t *testing.T) {
	t.Parallel()
	localizer, err := New(fstest.MapFS{
		"en.json": {Data: []byte(`[
  {"id":"greeting","translation":"Hello {{.Name}}"},
  {"id":"plain","translation":"Plain"}
]`)},
		"fr.json": {Data: []byte(`[{"id":"plain","translation":"Simple"}]`)},
	}, "en")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []Definition{
		{ID: "plain", Origin: "test"},
		{ID: "greeting", Origin: "test", Variables: []string{"Name"}},
	}
	if err := localizer.ValidateDefinitions(definitions); err != nil {
		t.Fatal(err)
	}
	missing, err := localizer.MissingDefinitions("fr", definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].ID != "greeting" {
		t.Fatalf("missing definitions = %#v", missing)
	}
}

func TestLocalizerRejectsDefinitionDrift(t *testing.T) {
	t.Parallel()
	localizer, err := New(fstest.MapFS{"en.json": {Data: []byte(`[
  {"id":"greeting","translation":"Hello {{.Name}}"},
  {"id":"orphan","translation":"Orphan"}
]`)}}, "en")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]Definition{
		"orphan": {{ID: "greeting", Origin: "test", Variables: []string{"Name"}}},
		"variable mismatch": {
			{ID: "greeting", Origin: "test", Variables: []string{"User"}},
			{ID: "orphan", Origin: "test"},
		},
		"duplicate owner": {
			{ID: "greeting", Origin: "one", Variables: []string{"Name"}},
			{ID: "greeting", Origin: "two", Variables: []string{"Name"}},
		},
	}
	for name, definitions := range tests {
		name, definitions := name, definitions
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := localizer.ValidateDefinitions(definitions); err == nil {
				t.Fatal("definition drift was accepted")
			}
		})
	}
}

func TestLocalizerUsesCLDRPluralForms(t *testing.T) {
	t.Parallel()
	localizer, err := New(fstest.MapFS{
		"en.json": {Data: []byte(`[
  {"id":"items","translation":{"one":"{{.PluralCount}} item","other":"{{.PluralCount}} items"}}
]`)},
	}, "en")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []Definition{{
		ID: "items", Origin: "test", Variables: []string{"PluralCount"}, PluralVariable: "PluralCount",
	}}
	if err := localizer.ValidateDefinitions(definitions); err != nil {
		t.Fatal(err)
	}
	for count, want := range map[int]string{1: "1 item", 2: "2 items"} {
		got, err := localizer.TranslateRequest("en", Request{ID: "items", PluralCount: count})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("count %d = %q, want %q", count, got, want)
		}
	}
	if _, err := localizer.Resolve("en", "items", nil); err == nil {
		t.Fatal("plural message resolved without a count")
	}
}

func TestPreferredLocaleHonorsSpecificQualityAndExclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested string
		supported []string
		want      string
	}{
		{
			name:      "specific quality overrides broader range",
			requested: "en-US;q=0.1, en;q=1, fr;q=0.5",
			supported: []string{"en-US", "fr"},
			want:      "fr",
		},
		{
			name:      "explicit exclusion overrides wildcard",
			requested: "*;q=1, en;q=0",
			supported: []string{"en", "fr"},
			want:      "fr",
		},
		{
			name:      "regional exclusion covers a regional candidate",
			requested: "*;q=0.8, en;q=0",
			supported: []string{"en-GB", "fr"},
			want:      "fr",
		},
		{
			name:      "nested prefix uses the narrowest range",
			requested: "zh;q=0.8, zh-Hans;q=0.1, fr;q=0.5",
			supported: []string{"zh-Hans-CN", "fr"},
			want:      "fr",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := PreferredLocale(tt.requested, tt.supported); got != tt.want {
				t.Fatalf("PreferredLocale(%q, %#v) = %q, want %q", tt.requested, tt.supported, got, tt.want)
			}
		})
	}
}
