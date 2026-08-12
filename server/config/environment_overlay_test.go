// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"reflect"
	"testing"
)

func TestEnvironmentOverrideCatalogContracts(t *testing.T) {
	if len(environmentOverrideCatalog) != 95 {
		t.Fatalf("environment override definitions = %d, want 95", len(environmentOverrideCatalog))
	}

	seen := make(map[string]struct{}, len(environmentOverrideCatalog))
	for _, definition := range environmentOverrideCatalog {
		definition := definition
		t.Run(definition.key, func(t *testing.T) {
			if _, exists := seen[definition.key]; exists {
				t.Fatalf("environment override %q is duplicated", definition.key)
			}
			seen[definition.key] = struct{}{}

			persisted := Default()
			value := environmentValueThatChanges(t, definition, persisted)
			candidate := persisted.Clone()
			overlay, err := applyEnvironmentCatalog(
				&candidate,
				func(key string) (string, bool) {
					return value, key == definition.key
				},
				[]environmentOverride{definition},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(overlay.keys(), []string{definition.key}) {
				t.Fatalf("applied overrides = %v, want %q", overlay.keys(), definition.key)
			}
			if reflect.DeepEqual(candidate, persisted) {
				t.Fatal("environment override did not change configuration")
			}

			overlay.restore(&candidate, persisted)
			if !reflect.DeepEqual(candidate, persisted) {
				t.Fatal("environment override did not restore persisted configuration")
			}
		})
	}
}

func TestEnvironmentOverrideCatalogRejectsDuplicatesWithoutMutation(t *testing.T) {
	catalog := append([]environmentOverride(nil), environmentOverrideCatalog...)
	catalog = append(catalog, environmentOverrideCatalog[0])
	candidate := Default()
	want := candidate.Clone()

	overlay, err := applyEnvironmentCatalog(
		&candidate,
		func(string) (string, bool) { return "changed", true },
		catalog,
	)
	if err == nil {
		t.Fatal("duplicate environment override was accepted")
	}
	if overlay.keys() != nil {
		t.Fatalf("applied overrides = %v, want nil", overlay.keys())
	}
	if !reflect.DeepEqual(candidate, want) {
		t.Fatal("duplicate catalog mutated configuration")
	}
}

func environmentValueThatChanges(
	t *testing.T,
	definition environmentOverride,
	persisted Config,
) string {
	t.Helper()
	for _, value := range []string{
		"changed",
		"2",
		"3",
		"2s",
		"false",
		"true",
		"one.example:1,two.example:2",
	} {
		candidate := persisted.Clone()
		if err := definition.apply(&candidate, value); err == nil &&
			!reflect.DeepEqual(candidate, persisted) {
			return value
		}
	}
	t.Fatalf("no test value changes environment override %q", definition.key)
	return ""
}
