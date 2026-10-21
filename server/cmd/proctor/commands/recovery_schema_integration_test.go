//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
)

func TestRecoveryCommandsRefuseUnmigratedSchemaIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dataSource)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "proctor_recovery_" + strings.ToLower(model.NewID()[:12])
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	fresh, err := url.Parse(dataSource)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Path = "/" + name
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", fresh.String())
	cfg, err := json.Marshal(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	institutionID, userID := model.NewID(), model.NewID()
	for _, args := range [][]string{
		{"--config", configPath, "administrator", "recover", "--institution-id", institutionID, "--user-id", userID, "--enable-local-login"},
		{"--config", configPath, "administrator", "reset-mfa", "--institution-id", institutionID, "--user-id", userID},
	} {
		code, _, stderr := executeForTest(ctx, args, nil, productionExecutors())
		if code == 0 || !strings.Contains(stderr, "proctor migrate up") {
			t.Fatalf("%s code=%d stderr=%q, want refusal pointing at migrate up", args[3], code, stderr)
		}
	}

	freshDB, err := sql.Open("postgres", fresh.String())
	if err != nil {
		t.Fatal(err)
	}
	defer freshDB.Close()
	var tables int
	if err := freshDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("recovery commands created %d tables; they must never migrate", tables)
	}
}
