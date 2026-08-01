// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

func TestValidateConfigAcceptsDefaultMemoryConfiguration(t *testing.T) {
	t.Parallel()

	if err := server.ValidateConfig(context.Background(), ""); err != nil {
		t.Fatalf("ValidateConfig() error = %v, want nil", err)
	}
}

func TestValidateConfigRejectsMissingFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	err := server.ValidateConfig(context.Background(), missing)
	if err == nil || !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("ValidateConfig() error = %v, want read failure", err)
	}
}

func TestValidateConfigRejectsMalformedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ValidateConfig(context.Background(), path); err == nil {
		t.Fatal("ValidateConfig() error = nil, want validation failure")
	}
}

func TestValidateConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown_field": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ValidateConfig(context.Background(), path); err == nil {
		t.Fatal("ValidateConfig() error = nil, want strict field failure")
	}
}

func TestMigrateCommandsRejectMissingConfigurationFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := server.MigrateUp(context.Background(), missing); err == nil {
		t.Fatal("MigrateUp() error = nil, want configuration failure")
	}
	if _, err := server.MigrateStatus(context.Background(), missing); err == nil {
		t.Fatal("MigrateStatus() error = nil, want configuration failure")
	}
}

func TestCurrentBuildInfoReportsBuildValues(t *testing.T) {
	t.Parallel()

	info := server.CurrentBuildInfo()
	if info.Version != "dev" || info.Commit != "unknown" || info.BuildTime != "unknown" {
		t.Fatalf("CurrentBuildInfo() = %#v, want default build values", info)
	}
	if info.GoVersion != runtime.Version() {
		t.Fatalf("CurrentBuildInfo().GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
}

func TestBuildInfoUsesWireFieldNames(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(server.BuildInfo{
		Version:   "1.2.3",
		Commit:    "abc123",
		BuildTime: "2026-07-26T00:00:00Z",
		GoVersion: "go1.25.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"version":    "1.2.3",
		"commit":     "abc123",
		"build_time": "2026-07-26T00:00:00Z",
		"go_version": "go1.25.4",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Fatalf("BuildInfo JSON field %q = %q, want %q (encoded %s)", key, fields[key], value, encoded)
		}
	}
}
