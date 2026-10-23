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

	"github.com/sudosylabs/proctor/server"
)

const baselineRollbackPlan = "database schema version 1; reverting 1 migration(s) to version 0:\n" +
	"  000001 baseline\n" +
	"warning: this reverts the single pre-release baseline and leaves an empty schema; every Proctor table and all of its data is dropped, and only a database backup restores it\n"

func migrateDownExecutors(t *testing.T, live []string, reverted *int) executors {
	t.Helper()
	execute := testExecutors()
	execute.planMigrateDown = func(_ context.Context, path string, steps int) (server.MigrationRollbackPlan, error) {
		require.Equal(t, "/etc/proctor.json", path)
		require.Equal(t, 1, steps)
		return server.MigrationRollbackPlan{
			DatabaseVersion: 1, Migrations: []server.RevertedMigration{{Version: 1, Name: "baseline"}}, LiveServingNodes: live,
		}, nil
	}
	execute.migrateDown = func(_ context.Context, _ string, steps int) (int, error) {
		*reverted += steps
		return 0, nil
	}
	return execute
}

func TestMigrateDownPrintsThePlanAndRequiresConfirmation(t *testing.T) {
	t.Parallel()

	reverted := 0
	code, stdout, stderr := executeForTest(context.Background(),
		[]string{"migrate", "down", "--steps", "1", "--config", "/etc/proctor.json"}, nil, migrateDownExecutors(t, nil, &reverted))
	require.Equal(t, 1, code)
	require.Equal(t, baselineRollbackPlan, stdout)
	require.Equal(t, "proctor: nothing was reverted; re-run with --confirm to revert these migrations\n", stderr)
	require.Zero(t, reverted)
}

func TestMigrateDownRevertsAfterPrintingThePlan(t *testing.T) {
	t.Parallel()

	reverted := 0
	code, stdout, stderr := executeForTest(context.Background(),
		[]string{"migrate", "down", "--steps", "1", "--confirm", "--config", "/etc/proctor.json"}, nil, migrateDownExecutors(t, nil, &reverted))
	require.Equal(t, 0, code, "stderr=%q", stderr)
	require.Equal(t, baselineRollbackPlan+"database schema reverted to version 0\n", stdout)
	require.Equal(t, 1, reverted)
}

func TestMigrateDownRefusesWhileServingNodesAreLive(t *testing.T) {
	t.Parallel()

	reverted := 0
	code, stdout, stderr := executeForTest(context.Background(),
		[]string{"migrate", "down", "--steps", "1", "--confirm", "--config", "/etc/proctor.json"}, nil, migrateDownExecutors(t, []string{"node-a", "node-b"}, &reverted))
	require.Equal(t, 1, code)
	require.Equal(t, baselineRollbackPlan, stdout)
	require.Contains(t, stderr, "serving nodes node-a, node-b hold a live lease")
	require.Zero(t, reverted)
}

func TestMigrateDownRequiresAnExplicitStepCount(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"migrate", "down"}, {"migrate", "down", "--steps", "0", "--confirm"}} {
		code, stdout, stderr := executeForTest(context.Background(), args, nil, testExecutors())
		require.Equal(t, 2, code, "run(%q)", args)
		require.Empty(t, stdout, "run(%q)", args)
		require.Contains(t, stderr, "migrate down requires --steps with a count of at least 1", "run(%q)", args)
	}
}
