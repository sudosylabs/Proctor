//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"sync"
	"testing"
	"time"
)

func holdSystemAdministratorAuthenticationPathFence(t *testing.T, ctx context.Context, persistence *SQLStore) (int, func()) {
	return holdSQLAdvisoryFence(t, ctx, persistence, systemAdministratorAuthenticationPathLock)
}

func holdServingNodeLeaseFence(t *testing.T, ctx context.Context, persistence *SQLStore) (int, func()) {
	return holdSQLAdvisoryFence(t, ctx, persistence, servingNodeLeaseFenceKey)
}

func holdSQLAdvisoryFence(t *testing.T, ctx context.Context, persistence *SQLStore, key string) (int, func()) {
	t.Helper()
	connection, err := persistence.GetMaster().DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = connection.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if _, err = connection.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	var once sync.Once
	return pid, func() {
		once.Do(func() {
			_, _ = connection.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key)
			_ = connection.Close()
		})
	}
}

func waitForBlockedSystemAdministratorAuthenticationPathTransactions(t *testing.T, ctx context.Context, persistence *SQLStore, blockerPID, want int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := persistence.GetMaster().DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity
			WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%pg_advisory_xact_lock%'`, blockerPID).Scan(&count)
		if err == nil && count >= want {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %d authentication-path transactions: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}
