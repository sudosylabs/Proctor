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

	server "github.com/sudosylabs/proctor/server"
)

func TestMigrateCommandsUseNarrowServerOperations(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.migrateUp = func(_ context.Context, path string) (int, error) {
		if path != "/etc/proctor.json" {
			t.Fatalf("up config path = %q", path)
		}
		return 7, nil
	}
	execute.migrateStatus = func(_ context.Context, path string) (server.MigrationStatus, error) {
		if path != "/etc/proctor.json" {
			t.Fatalf("status config path = %q", path)
		}
		return server.MigrationStatus{DatabaseVersion: 5, ServerVersion: 7, PendingMigrations: 2}, nil
	}

	code, stdout, stderr := executeForTest(
		context.Background(), []string{"migrate", "up", "--config", "/etc/proctor.json"}, nil, execute,
	)
	if code != 0 || stdout != "database schema migrated to version 7\n" || stderr != "" {
		t.Fatalf("up: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = executeForTest(
		context.Background(), []string{"migrate", "status", "--config", "/etc/proctor.json"}, nil, execute,
	)
	if code != 0 || stdout != "database schema version 5; server schema version 7; pending migrations 2\n" || stderr != "" {
		t.Fatalf("status: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
