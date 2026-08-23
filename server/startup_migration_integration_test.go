//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/config"
)

func TestNewAutomaticallyMigratesAnEmptyDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSource := newStartupMigrationDatabase(t, ctx)
	cfg := config.Default()
	cfg.Database.DataSource = dataSource
	cfg.VFS.Local.Root = t.TempDir()
	cfg.Server.WebappDirectory = createStartupMigrationWebapp(t)
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := MigrateStatus(ctx, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if before.DatabaseVersion != 0 || before.PendingMigrations == 0 {
		t.Fatalf("migration status before New = %#v, want empty database with pending migrations", before)
	}

	node, err := New(ctx, WithConfigPath(configPath))
	if err != nil {
		t.Fatalf("New() did not converge the empty database: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("close migrated node: %v", err)
	}

	after, err := MigrateStatus(ctx, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.DatabaseVersion != after.ServerVersion || after.PendingMigrations != 0 {
		t.Fatalf("migration status after New = %#v, want converged schema", after)
	}
}

func createStartupMigrationWebapp(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	build := CurrentBuildInfo()
	manifest, err := json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		Version       string `json:"version"`
		Commit        string `json:"commit"`
	}{SchemaVersion: 1, Version: build.Version, Commit: build.Commit})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "webapp-build.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>Proctor</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func newStartupMigrationDatabase(t *testing.T, ctx context.Context) string {
	t.Helper()

	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is required for integration tests")
	}
	parsed, err := url.Parse(dataSource)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("proctor_startup_%d", time.Now().UnixNano())
	admin, err := sql.Open("postgres", dataSource)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if err := admin.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(
			cleanupCtx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1",
			name,
		)
		if _, err := admin.ExecContext(cleanupCtx, "DROP DATABASE "+pq.QuoteIdentifier(name)); err != nil {
			t.Errorf("drop temporary startup database: %v", err)
		}
	})
	parsed.Path = "/" + name
	return parsed.String()
}
