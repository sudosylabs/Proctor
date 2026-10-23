//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	before, err := MigrateStatus(ctx, configPath)
	require.NoError(t, err)
	require.Equal(t, 0, before.DatabaseVersion, "migration status before New = %#v, want empty database with pending migrations", before)
	require.NotEqual(t, 0, before.PendingMigrations, "migration status before New = %#v, want empty database with pending migrations", before)

	node, err := New(ctx, WithConfigPath(configPath))
	require.NoError(t, err)
	require.NoError(t, node.Close())

	after, err := MigrateStatus(ctx, configPath)
	require.NoError(t, err)
	require.Equal(t, after.ServerVersion, after.DatabaseVersion, "migration status after New = %#v, want converged schema", after)
	require.Equal(t, 0, after.PendingMigrations, "migration status after New = %#v, want converged schema", after)
}

func createStartupMigrationWebapp(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	build := CurrentBuildInfo()
	manifest, err := json.Marshal(struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}{Version: build.Version, Commit: build.Commit})
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(directory, "webapp-build.json"), manifest, 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(directory, "index.html"), []byte("<!doctype html><title>Proctor</title>"), 0o600)
	require.NoError(t, err)
	return directory
}

func newStartupMigrationDatabase(t *testing.T, ctx context.Context) string {
	t.Helper()

	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required for integration tests")
	parsed, err := url.Parse(dataSource)
	require.NoError(t, err)
	name := fmt.Sprintf("proctor_startup_%d", time.Now().UnixNano())
	admin, err := sql.Open("postgres", dataSource)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, admin.PingContext(ctx))
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(
			cleanupCtx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1",
			name,
		)
		_, err := admin.ExecContext(cleanupCtx, "DROP DATABASE "+pq.QuoteIdentifier(name))
		assert.NoError(t, err)
	})
	parsed.Path = "/" + name
	return parsed.String()
}
