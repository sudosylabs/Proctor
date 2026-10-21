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

	"github.com/stretchr/testify/require"

	server "github.com/sudosylabs/proctor/server"
)

func TestMigrateCommandsUseNarrowServerOperations(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.migrateUp = func(_ context.Context, path string) (int, error) {
		require.Equal(t, "/etc/proctor.json", path)
		return 7, nil
	}
	execute.migrateStatus = func(_ context.Context, path string) (server.MigrationStatus, error) {
		require.Equal(t, "/etc/proctor.json", path)
		return server.MigrationStatus{DatabaseVersion: 5, ServerVersion: 7, PendingMigrations: 2}, nil
	}

	code, stdout, stderr := executeForTest(
		context.Background(), []string{"migrate", "up", "--config", "/etc/proctor.json"}, nil, execute,
	)
	require.Equal(t, 0, code, "up: code")
	require.Equal(t, "database schema migrated to version 7\n", stdout, "up: code")
	require.Empty(t, stderr, "up: code")

	code, stdout, stderr = executeForTest(
		context.Background(), []string{"migrate", "status", "--config", "/etc/proctor.json"}, nil, execute,
	)
	require.Equal(t, 0, code, "status: code")
	require.Equal(t, "database schema version 5; server schema version 7; pending migrations 2\n", stdout, "status: code")
	require.Empty(t, stderr, "status: code")
}
