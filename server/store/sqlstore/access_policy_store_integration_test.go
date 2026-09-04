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
	})
}
