// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestExampleConfigurationUsesPascalCaseAndIsValid(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Version", "Server", "Database", "Cache", "Cluster"} {
		if _, exists := document[key]; !exists {
			t.Fatalf("example configuration is missing PascalCase key %q", key)
		}
	}
	if _, exists := document["version"]; exists {
		t.Fatal("example configuration contains legacy lower-case key version")
	}

	fileStore, err := NewFileStore("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(context.Background(), fileStore, StoreOptions{LookupEnv: noEnvironment})
	if err != nil {
		t.Fatalf("load example configuration: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
}
