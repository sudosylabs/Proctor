// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "testing"

func TestExamCapacityPolicyDefaultsAndSafetyCeilings(t *testing.T) {
	t.Parallel()
	policy := DefaultExamCapacityPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.ResourceMaximumCount != 10 || policy.ResourceMaximumBytes != 10<<20 ||
		policy.WorkspaceMaximumEntries != 500 || policy.WorkspaceMaximumFileBytes != 10<<20 ||
		policy.WorkspaceMaximumTotalBytes != 50<<20 {
		t.Fatalf("default policy = %#v", policy)
	}
	for _, invalid := range []ExamCapacityPolicy{
		{},
		{ResourceMaximumCount: ExamResourceMaximumCount + 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: ExamResourceMaximumBytes + 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: ExamWorkspaceMaximumEntries + 1, WorkspaceMaximumFileBytes: 1, WorkspaceMaximumTotalBytes: 1},
		{ResourceMaximumCount: 1, ResourceMaximumBytes: 1, WorkspaceMaximumEntries: 1, WorkspaceMaximumFileBytes: 2, WorkspaceMaximumTotalBytes: 1},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %#v", invalid)
		}
	}
}
