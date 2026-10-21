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
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

func TestMigrateIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required for integration tests")
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", dataSource)
	cfg, err := json.Marshal(config.Default())
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	err = os.WriteFile(configPath, cfg, 0o600)
	require.NoError(t, err)

	code, stdout, stderr := executeForTest(context.Background(), []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate up code=%d stderr=%q", code, stderr)
	version, err := sqlstore.LocalSchemaVersion()
	require.NoError(t, err)
	require.Contains(t, stdout, "version "+strconv.Itoa(version))

	code, stdout, stderr = executeForTest(context.Background(), []string{"--config", configPath, "migrate", "status"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate status code=%d stderr=%q", code, stderr)
	require.Contains(t, stdout, "pending migrations 0")
}

func TestMigrateStatusIsReadOnlyIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required for integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dataSource)
	require.NoError(t, err)
	defer admin.Close()
	name := "proctor_status_" + strings.ToLower(model.NewID()[:12])
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	fresh, err := url.Parse(dataSource)
	require.NoError(t, err)
	fresh.Path = "/" + name
	t.Setenv("PROCTOR_DATABASE_DATA_SOURCE", fresh.String())
	cfg, err := json.Marshal(config.Default())
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configPath, cfg, 0o600))

	code, stdout, stderr := executeForTest(ctx, []string{"--config", configPath, "migrate", "status"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate status code=%d stderr=%q", code, stderr)
	require.Contains(t, stdout, "pending migrations 1")

	freshDB, err := sql.Open("postgres", fresh.String())
	require.NoError(t, err)
	defer freshDB.Close()
	var tables int
	require.NoError(t, freshDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables))
	require.Equal(t, 0, tables, "migrate status created")
}
