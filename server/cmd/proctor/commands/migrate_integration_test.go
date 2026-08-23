//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestMigrateIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is required for integration tests")
	}
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", dataSource)
	cfg, err := json.Marshal(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	if code != 0 {
		t.Fatalf("migrate up code=%d stderr=%q", code, stderr)
	}
	version, err := sqlstore.LocalSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "version "+strconv.Itoa(version)) {
		t.Fatalf("migrate up output = %q", stdout)
	}

	code, stdout, stderr = executeForTest(context.Background(), []string{"--config", configPath, "migrate", "status"}, nil, productionExecutors())
	if code != 0 {
		t.Fatalf("migrate status code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "pending migrations 0") {
		t.Fatalf("migrate status output = %q", stdout)
	}
}
