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

	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestAdministratorMFAResetStore(t *testing.T) {
	PristineStoreTest(t, func(t *testing.T, persistence store.Store) {
		sqlStore := persistence.(*SQLStore)
		storetest.TestAdministratorMFAResetStore(t, persistence, administratorMFAResetSQLProbe(sqlStore))
	})
}

func TestAdministratorMFAResetExternalStore(t *testing.T) {
	PristineStoreTest(t, storetest.TestAdministratorMFAResetExternalStore)
}

func administratorMFAResetSQLProbe(sqlStore *SQLStore) storetest.AdministratorMFAResetSQLProbe {
	return storetest.AdministratorMFAResetSQLProbe{
		ConcurrentPeer: newSQLInstallationStore(sqlStore),
		ServingFence: storetest.AdministratorRecoverySQLProbe{
			HoldServingNodeLeaseFence: func(t *testing.T, ctx context.Context) (int, func()) {
				return holdServingNodeLeaseFence(t, ctx, sqlStore)
			},
			WaitForBlockedTransactions: func(t *testing.T, ctx context.Context, blockerPID, want int) {
				waitForBlockedSystemAdministratorAuthenticationPathTransactions(t, ctx, sqlStore, blockerPID, want)
			},
		},
		RejectRecoveryEvidence: func(t *testing.T, ctx context.Context) func() {
			t.Helper()
			if _, err := sqlStore.GetMaster().Exec(ctx, `ALTER TABLE administrator_recovery_records ADD CONSTRAINT test_reject_mfa_recovery CHECK (NOT mfa_reset) NOT VALID`); err != nil {
				t.Fatal(err)
			}
			released := false
			release := func() {
				if released {
					return
				}
				released = true
				if _, err := sqlStore.GetMaster().Exec(context.Background(), `ALTER TABLE administrator_recovery_records DROP CONSTRAINT test_reject_mfa_recovery`); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(release)
			return release
		},
	}
}
