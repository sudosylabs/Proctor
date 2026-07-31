// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
)

func TestRunConfigValidate(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "validate"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}
	if stdout.String() != "configuration is valid\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	originalVersion := app.Version
	originalCommit := app.Commit
	originalBuildTime := app.BuildTime
	t.Cleanup(func() {
		app.Version = originalVersion
		app.Commit = originalCommit
		app.BuildTime = originalBuildTime
	})
	app.Version = "1.2.3"
	app.Commit = "abc123"
	app.BuildTime = "2026-07-26T00:00:00Z"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"version", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var info api.BuildInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3" || info.Commit != "abc123" {
		t.Fatalf("version info = %#v", info)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) {
		t.Fatalf("run() error = %v, want *UsageError", err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("usage was not written: %q", stderr.String())
	}
}

func TestRunMigrateRequiresAction(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(context.Background(), []string{"migrate"}, &stdout, &stderr)
	var usageError *UsageError
	if !errors.As(err, &usageError) || !strings.Contains(err.Error(), "migrate <up|status>") {
		t.Fatalf("run() error = %v, want migrate usage error", err)
	}
}

func TestMigrateIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", dataSource)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"migrate", "up"}, &stdout, &stderr); err != nil {
		t.Fatalf("migrate up error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version 9") {
		t.Fatalf("migrate up output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"migrate", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("migrate status error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "pending migrations 0") {
		t.Fatalf("migrate status output = %q", stdout.String())
	}
}
