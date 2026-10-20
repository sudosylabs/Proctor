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
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestAccessPolicyStore(t *testing.T) {
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	storetest.TestAccessPolicyStore(t, persistence, storetest.AccessPolicySQLProbe{
		HoldAuthenticationPathFence: func(t *testing.T, ctx context.Context) (int, func()) {
			t.Helper()
			return holdSystemAdministratorAuthenticationPathFence(t, ctx, persistence)
		},
		WaitForBlockedTransactions: func(t *testing.T, ctx context.Context, blockerPID, want int) {
			t.Helper()
			waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, persistence, blockerPID, want)
		},
		LeadDatabaseClock: func(t *testing.T, ctx context.Context, id model.SessionID) {
			t.Helper()
			leadSessionDatabaseClock(t, ctx, persistence, id)
		},
	})
}

// leadSessionDatabaseClock moves one Session's and its credentials' node-clock
// instants a minute after PostgreSQL time, as a creating node whose clock leads
// the database does.
func leadSessionDatabaseClock(t *testing.T, ctx context.Context, persistence *SQLStore, id model.SessionID) {
	t.Helper()
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE sessions SET created_at=created_at+INTERVAL '1 minute',
		updated_at=updated_at+INTERVAL '1 minute', authenticated_at=authenticated_at+INTERVAL '1 minute',
		last_activity_at=last_activity_at+INTERVAL '1 minute' WHERE id=?`, id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `UPDATE session_credentials SET created_at=created_at+INTERVAL '1 minute',
		updated_at=updated_at+INTERVAL '1 minute' WHERE session_id=?`, id.String()); err != nil {
		t.Fatal(err)
	}
}
