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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMigrateDownRevertsTheBaselineOnlyWhenConfirmedAndNoNodeServesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	configPath, database := freshInspectionDatabase(t, ctx)
	schemaVersion := func() int {
		t.Helper()
		var version int
		require.NoError(t, database.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM db_migrations`).Scan(&version))
		return version
	}

	code, _, stderr := executeForTest(ctx, []string{"--config", configPath, "migrate", "up"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate up: stderr=%q", stderr)

	code, stdout, stderr := executeForTest(ctx, []string{"--config", configPath, "migrate", "down", "--steps", "2"}, nil, productionExecutors())
	require.Equal(t, 1, code)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "cannot revert 2 migrations: only 1 are applied")

	code, stdout, stderr = executeForTest(ctx, []string{"--config", configPath, "migrate", "down", "--steps", "1"}, nil, productionExecutors())
	require.Equal(t, 1, code)
	require.Contains(t, stdout, "000001 baseline")
	require.Contains(t, stdout, "leaves an empty schema")
	require.Contains(t, stderr, "re-run with --confirm")
	require.Equal(t, 1, schemaVersion(), "an unconfirmed rollback changed the schema")

	_, err := database.ExecContext(ctx, `INSERT INTO serving_node_leases (node_id, lease_id, updated_at, expires_at)
		VALUES ('node-a', $1, clock_timestamp(), clock_timestamp() + interval '1 minute')`, model.NewID())
	require.NoError(t, err)
	code, _, stderr = executeForTest(ctx, []string{"--config", configPath, "migrate", "down", "--steps", "1", "--confirm"}, nil, productionExecutors())
	require.Equal(t, 1, code)
	require.Contains(t, stderr, "serving nodes node-a hold a live lease")
	require.Equal(t, 1, schemaVersion(), "a rollback beside a live node changed the schema")

	_, err = database.ExecContext(ctx, `UPDATE serving_node_leases SET expires_at = clock_timestamp() - interval '1 second', updated_at = clock_timestamp() - interval '1 minute'`)
	require.NoError(t, err)
	code, stdout, stderr = executeForTest(ctx, []string{"--config", configPath, "migrate", "down", "--steps", "1", "--confirm"}, nil, productionExecutors())
	require.Equal(t, 0, code, "migrate down: stderr=%q", stderr)
	require.Contains(t, stdout, "database schema reverted to version 0")
	require.Equal(t, 0, schemaVersion())
	var tables []string
	rows, err := database.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())
	// Only Morph's own lock and bookkeeping tables survive the baseline.
	require.Equal(t, []string{"db_lock", "db_migrations"}, tables, "the reverted baseline left tables behind")
}
