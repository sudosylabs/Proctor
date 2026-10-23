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
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

// freshInspectionDatabase creates an empty database, points the command
// configuration at it, and returns the configuration path and a connection.
func freshInspectionDatabase(t *testing.T, ctx context.Context) (string, *sql.DB) {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is required for integration tests")
	admin, err := sql.Open("postgres", dataSource)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	name := "proctor_inspect_" + strings.ToLower(model.NewID()[:12])
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
	database, err := sql.Open("postgres", fresh.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	return configPath, database
}

func TestClusterStatusIsReadOnlyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	configPath, database := freshInspectionDatabase(t, ctx)

	code, stdout, stderr := executeForTest(ctx, []string{"--config", configPath, "cluster", "status"}, nil, productionExecutors())
	require.Equal(t, 0, code, "cluster status on an empty schema: stderr=%q", stderr)
	require.Contains(t, stdout, "database schema version 0")
	var tables int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables))
	require.Equal(t, 0, tables, "cluster status created a table")

	code, _, stderr = executeForTest(ctx, []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate up: stderr=%q", stderr)
	_, err := database.ExecContext(ctx, `INSERT INTO serving_node_leases (node_id, lease_id, updated_at, expires_at)
		VALUES ('node-a', $1, clock_timestamp(), clock_timestamp() + interval '1 minute')`, model.NewID())
	require.NoError(t, err)

	code, stdout, stderr = executeForTest(ctx, []string{"--config", configPath, "cluster", "status", "--json"}, nil, productionExecutors())
	require.Equal(t, 0, code, "cluster status: stderr=%q", stderr)
	var status struct {
		ServingNodes []struct {
			NodeID string `json:"node_id"`
			Live   bool   `json:"live"`
		} `json:"serving_nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &status))
	require.Len(t, status.ServingNodes, 1)
	require.Equal(t, "node-a", status.ServingNodes[0].NodeID)
	require.True(t, status.ServingNodes[0].Live, "a fresh lease reported expired")
}

func TestJobsCommandsReadWithoutPrintingPayloadsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	configPath, database := freshInspectionDatabase(t, ctx)

	code, _, stderr := executeForTest(ctx, []string{"--config", configPath, "jobs", "list"}, nil, productionExecutors())
	require.Equal(t, 1, code)
	require.Contains(t, stderr, "database has no schema yet")
	var tables int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables))
	require.Equal(t, 0, tables, "jobs list created a table")

	code, _, stderr = executeForTest(ctx, []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate up: stderr=%q", stderr)
	settings := sqlstore.SettingsFromConfig(config.Default().Database)
	settings.DataSource = os.Getenv("PROCTOR_DATABASE_DATA_SOURCE")
	writer, err := sqlstore.New(ctx, settings)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	const sentinel = "sentinel-user-content"
	now := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault,
		json.RawMessage(`{"user_id":"`+sentinel+`"}`), "dedupe-"+sentinel, now, now, 3)
	require.NoError(t, err)
	_, inserted, err := writer.Job().Enqueue(ctx, &store.JobEnqueue{Job: job})
	require.NoError(t, err)
	require.True(t, inserted)

	code, stdout, stderr := executeForTest(ctx, []string{"--config", configPath, "jobs", "list", "--type", string(model.JobTypeProfilePictureGenerateDefault), "--state", "queued"}, nil, productionExecutors())
	require.Equal(t, 0, code, "jobs list: stderr=%q", stderr)
	require.Contains(t, stdout, job.ID.String())
	require.NotContains(t, stdout, sentinel)

	code, stdout, stderr = executeForTest(ctx, []string{"--config", configPath, "jobs", "list", "--state", "failed"}, nil, productionExecutors())
	require.Equal(t, 0, code, "jobs list: stderr=%q", stderr)
	require.Equal(t, "no Jobs match\n", stdout)

	for _, args := range [][]string{{"jobs", "show", job.ID.String()}, {"jobs", "show", job.ID.String(), "--json"}} {
		code, stdout, stderr = executeForTest(ctx, append([]string{"--config", configPath}, args...), nil, productionExecutors())
		require.Equal(t, 0, code, "run(%q): stderr=%q", args, stderr)
		require.Contains(t, stdout, "profile_picture.generate_default", "run(%q)", args)
		require.NotContains(t, stdout, sentinel, "run(%q)", args)
	}

	code, _, stderr = executeForTest(ctx, []string{"--config", configPath, "jobs", "show", model.NewJobID().String()}, nil, productionExecutors())
	require.Equal(t, 1, code)
	require.Contains(t, stderr, "job not found")
}
