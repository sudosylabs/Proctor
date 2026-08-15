// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateUserSettingsSourceAcceptsVersionOneJSONC(t *testing.T) {
	source := "{\n" +
		"  // portable presentation\n" +
		"  \"workbench.colorTheme\": \"dark\",\n" +
		"  \"unknown.futureSetting\": {\"enabled\": true},\n" +
		"  \"[go]\": {\n" +
		"    \"editor.tabSize\": 4,\n" +
		"  },\n" +
		"}\n"

	diagnostics := validateUserSettingsSource(source)
	if len(diagnostics) != 0 {
		t.Fatalf("validateUserSettingsSource() diagnostics = %#v, want none", diagnostics)
	}
}

func TestValidateUserSettingsSourceRejectsUnsafeGrammar(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   string
	}{
		{name: "top-level array", source: `[]`, code: "top_level_object_required"},
		{name: "duplicate top-level key", source: `{"editor.fontSize": 12, "editor.fontSize": 14}`, code: "duplicate_key"},
		{name: "escaped duplicate key", source: `{"editor.fontSize": 12, "editor.font\u0053ize": 14}`, code: "duplicate_key"},
		{name: "duplicate nested key", source: `{"editor.options": {"mode": 1, "mode": 2}}`, code: "duplicate_key"},
		{name: "invalid setting key", source: `{"editor fontSize": 12}`, code: "invalid_setting_key"},
		{name: "invalid language value", source: `{"[go]": true}`, code: "language_block_object_required"},
		{name: "invalid language setting key", source: `{"[go]": {"editor tabSize": 4}}`, code: "invalid_setting_key"},
		{name: "unterminated comment", source: `{"editor.fontSize": 12 /*`, code: "unterminated_comment"},
		{name: "invalid number", source: `{"editor.fontSize": 1e}`, code: "invalid_value"},
		{name: "non-finite number", source: `{"editor.fontSize": NaN}`, code: "invalid_value"},
		{name: "trailing value", source: `{"editor.fontSize": 12} true`, code: "trailing_value"},
		{name: "executable expression", source: `{"editor.fontSize": call()}`, code: "invalid_value"},
		{name: "invalid UTF-8", source: string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}), code: "invalid_utf8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := validateUserSettingsSource(test.source)
			if len(diagnostics) != 1 {
				t.Fatalf("validateUserSettingsSource() diagnostics = %#v, want one", diagnostics)
			}
			if diagnostics[0].Code != test.code {
				t.Fatalf("diagnostic code = %q, want %q", diagnostics[0].Code, test.code)
			}
			if diagnostics[0].Line < 1 || diagnostics[0].Column < 1 {
				t.Fatalf("diagnostic location = %d:%d, want positive", diagnostics[0].Line, diagnostics[0].Column)
			}
		})
	}
}

func TestValidateUserSettingsSourceEnforcesBounds(t *testing.T) {
	largeObject := func(entries int) string {
		var source strings.Builder
		source.WriteByte('{')
		for index := range entries {
			if index != 0 {
				source.WriteByte(',')
			}
			fmt.Fprintf(&source, `"setting.%d": true`, index)
		}
		source.WriteByte('}')
		return source.String()
	}
	manyPaths := func() string {
		var source strings.Builder
		source.WriteByte('{')
		for group := range 9 {
			if group != 0 {
				source.WriteByte(',')
			}
			fmt.Fprintf(&source, `"setting.group%d":{`, group)
			for entry := range userSettingsCollectionMaxEntries {
				if entry != 0 {
					source.WriteByte(',')
				}
				fmt.Fprintf(&source, `"entry%d":true`, entry)
			}
			source.WriteByte('}')
		}
		source.WriteByte('}')
		return source.String()
	}
	deepObject := func(depth int) string {
		value := "true"
		for index := depth; index > 1; index-- {
			value = fmt.Sprintf(`{"nested":%s}`, value)
		}
		return fmt.Sprintf(`{"setting.deep":%s}`, value)
	}

	tests := []struct {
		name   string
		source string
		code   string
	}{
		{name: "source bytes", source: `{"setting.value":"` + strings.Repeat("x", userSettingsSourceMaxBytes) + `"}`, code: "source_too_large"},
		{name: "nesting", source: deepObject(userSettingsNestingMaxDepth + 1), code: "nesting_too_deep"},
		{name: "key bytes", source: `{"` + strings.Repeat("k", userSettingsKeyMaxBytes+1) + `":true}`, code: "key_too_large"},
		{name: "string bytes", source: `{"setting.value":"` + strings.Repeat("v", userSettingsStringMaxBytes+1) + `"}`, code: "string_too_large"},
		{name: "collection entries", source: largeObject(userSettingsCollectionMaxEntries + 1), code: "collection_too_large"},
		{name: "setting paths", source: manyPaths(), code: "too_many_setting_paths"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := validateUserSettingsSource(test.source)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("validateUserSettingsSource() diagnostics = %#v, want %q", diagnostics, test.code)
			}
		})
	}
}

func TestValidateUserSettingsSourceAcceptsBoundaryValues(t *testing.T) {
	value := strings.Repeat("v", userSettingsStringMaxBytes)
	source := `{"setting.value":"` + value + `"}`
	if diagnostics := validateUserSettingsSource(source); len(diagnostics) != 0 {
		t.Fatalf("validateUserSettingsSource() diagnostics = %#v, want none", diagnostics)
	}
}

func FuzzValidateUserSettingsSourceNeverPanics(f *testing.F) {
	f.Add(`{}`)
	f.Add(`{"editor.fontSize": 14,}`)
	f.Add("{/* comment */\n\"[go]\":{\"editor.tabSize\":4}}")
	f.Add(string([]byte{0xff, 0xfe}))

	f.Fuzz(func(t *testing.T, source string) {
		diagnostics := validateUserSettingsSource(source)
		if len(diagnostics) > 32 {
			t.Fatalf("diagnostic count = %d, want at most 32", len(diagnostics))
		}
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == "" || diagnostic.Line < 1 || diagnostic.Column < 1 {
				t.Fatalf("unsafe diagnostic = %#v", diagnostic)
			}
		}
	})
}
