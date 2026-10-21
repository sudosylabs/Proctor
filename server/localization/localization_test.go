// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package localization

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	locales := localizer.SupportedLocales()
	require.Equal(t, "en", localizer.DefaultLocale(), "locale metadata = default")
	require.Len(t, locales, 2, "locale metadata = default")
	require.Equal(t, "en", locales[0], "locale metadata = default")
	require.Equal(t, "fr", locales[1], "locale metadata = default")
	locales[0] = "changed"
	require.Equal(t, "en", localizer.SupportedLocales()[0], "supported locales exposed mutable localizer state")
	translated, err := localizer.Resolve("fr-CA", "greeting", struct{ Name string }{"Ada"})
	require.NoError(t, err)
	require.Equal(t, "fr", translated.Locale)
	require.Equal(t, "Bonjour Ada", translated.Text)
	fallback, err := localizer.Resolve("fr", "only.english", nil)
	require.NoError(t, err)
	require.Equal(t, "en", fallback.Locale)
	require.Equal(t, "English only", fallback.Text)
	_, err = localizer.Resolve("en", "greeting", struct{}{})
	require.Error(t, err, "missing interpolation data was accepted")
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
			_, err := New(files, "en")
			require.Error(t, err, "invalid catalog was accepted")
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
	require.NoError(t, err)
	definitions := []Definition{
		{ID: "plain", Origin: "test"},
		{ID: "greeting", Origin: "test", Variables: []string{"Name"}},
	}
	require.NoError(t, localizer.ValidateDefinitions(definitions))
	missing, err := localizer.MissingDefinitions("fr", definitions)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	require.Equal(t, "greeting", missing[0].ID)
}

func TestLocalizerRejectsDefinitionDrift(t *testing.T) {
	t.Parallel()
	localizer, err := New(fstest.MapFS{"en.json": {Data: []byte(`[
  {"id":"greeting","translation":"Hello {{.Name}}"},
  {"id":"orphan","translation":"Orphan"}
]`)}}, "en")
	require.NoError(t, err)
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
			require.Error(t, localizer.ValidateDefinitions(definitions), "definition drift was accepted")
		})
	}
}

func TestLocalizerDelegatesOneExplicitCatalogNamespace(t *testing.T) {
	t.Parallel()
	localizer, err := New(fstest.MapFS{"en.json": {Data: []byte(`[
  {"id":"server.message","translation":"Server"},
  {"id":"webapp.message","translation":"Browser"}
]`)}}, "en")
	require.NoError(t, err)
	definitions := []Definition{{ID: "server.message", Origin: "server"}}
	require.NoError(t, localizer.ValidateDefinitionsWithDelegatedPrefixes(definitions, "webapp."))

	tests := map[string]struct {
		definitions []Definition
		prefixes    []string
	}{
		"invalid prefix": {
			definitions: definitions,
			prefixes:    []string{"webapp"},
		},
		"duplicate prefix": {
			definitions: definitions,
			prefixes:    []string{"webapp.", "webapp."},
		},
		"delegated definition": {
			definitions: []Definition{
				{ID: "server.message", Origin: "server"},
				{ID: "webapp.message", Origin: "server"},
			},
			prefixes: []string{"webapp."},
		},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, localizer.ValidateDefinitionsWithDelegatedPrefixes(test.definitions, test.prefixes...), "invalid localization ownership was accepted")
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
	require.NoError(t, err)
	definitions := []Definition{{
		ID: "items", Origin: "test", Variables: []string{"PluralCount"}, PluralVariable: "PluralCount",
	}}
	require.NoError(t, localizer.ValidateDefinitions(definitions))
	for count, want := range map[int]string{1: "1 item", 2: "2 items"} {
		got, err := localizer.TranslateRequest("en", Request{ID: "items", PluralCount: count})
		require.NoError(t, err)
		require.Equal(t, want, got, "count %d", count)
	}
	_, err = localizer.Resolve("en", "items", nil)
	require.Error(t, err, "plural message resolved without a count")
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
			require.Equal(t, tt.want, PreferredLocale(tt.requested, tt.supported), "PreferredLocale(%q, %#v)", tt.requested, tt.supported)
		})
	}
}
