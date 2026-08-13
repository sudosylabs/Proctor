//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestMigrateIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is required for integration tests")
	}
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", dataSource)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run(context.Background(), []string{"migrate", "up"}, &stdout, &stderr); err != nil {
		t.Fatalf("migrate up error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version 12") {
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
