// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserSubmissionSettlementStatePrecedenceAndCounts(t *testing.T) {
	cases := []BrowserSubmissionSettlement{
		{State: "not_applicable", InventoryRevision: 1},
		{State: "settled", InventoryRevision: 2, SourceCount: 2},
		{State: "pending", InventoryRevision: 3, SourceCount: 2, PendingSourceCount: 1},
		{State: "pending", InventoryRevision: 4, SourceCount: 2, PendingSourceCount: 1, IncompleteSourceCount: 2},
		{State: "incomplete", InventoryRevision: 5, SourceCount: 2, IncompleteSourceCount: 1},
	}
	for _, v := range cases {
		require.NoError(t, v.Validate(), "valid settlement %#v", v)
		for _, state := range []string{"not_applicable", "settled", "pending", "incomplete", "complete", "gapped"} {
			if state == v.State {
				continue
			}
			bad := v
			bad.State = state
			require.Error(t, bad.Validate())
		}
	}
	for _, v := range []BrowserSubmissionSettlement{{State: "not_applicable"}, {State: "not_applicable", InventoryRevision: 1, SourceCount: -1}, {State: "pending", InventoryRevision: 1, PendingSourceCount: 1}, {State: "incomplete", InventoryRevision: 1, SourceCount: 1, IncompleteSourceCount: 2}, {State: "settled", InventoryRevision: 9007199254740992, SourceCount: 1}} {
		require.Error(t, v.Validate())
	}
}
