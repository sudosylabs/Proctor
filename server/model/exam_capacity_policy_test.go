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

func TestExamCapacityPolicyDefaultsAndSafetyCeilings(t *testing.T) {
	t.Parallel()
	policy := DefaultExamCapacityPolicy()
	require.NoError(t, policy.Validate())
	require.Equal(t, 10, policy.ResourceMaximumCount)
	require.Equal(t, int64(10<<20), policy.ResourceMaximumBytes)
	require.Equal(t, 500, policy.WorkspaceMaximumEntries)
	require.Equal(t, int64(10<<20), policy.WorkspaceMaximumFileBytes)
	require.Equal(t, int64(50<<20), policy.WorkspaceMaximumTotalBytes)
	for _, invalid := range []ExamCapacityPolicy{
		{},
		{ResourceMaximumCount: ExamResourceMaximumCount + 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: ExamResourceMaximumBytes + 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: ExamWorkspaceMaximumEntries + 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 2, WorkspaceMaximumTotalBytes: 1},
	} {
		require.Error(t, invalid.Validate(), "invalid policy accepted: %#v", invalid)
	}
}
