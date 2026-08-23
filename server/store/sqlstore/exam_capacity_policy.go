// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
)

type examCapacityPolicyRow struct {
	ResourceMaximumCount       int   `db:"exam_resource_max_count" json:"exam_resource_max_count"`
	ResourceMaximumBytes       int64 `db:"exam_resource_max_bytes" json:"exam_resource_max_bytes"`
	WorkspaceMaximumEntries    int   `db:"exam_workspace_max_entries" json:"exam_workspace_max_entries"`
	WorkspaceMaximumFileBytes  int64 `db:"exam_workspace_max_file_bytes" json:"exam_workspace_max_file_bytes"`
	WorkspaceMaximumTotalBytes int64 `db:"exam_workspace_max_total_bytes" json:"exam_workspace_max_total_bytes"`
}

func (row examCapacityPolicyRow) policy() (model.ExamCapacityPolicy, error) {
	policy := model.ExamCapacityPolicy{
		ResourceMaximumCount: row.ResourceMaximumCount, ResourceMaximumBytes: row.ResourceMaximumBytes,
		WorkspaceMaximumEntries: row.WorkspaceMaximumEntries, WorkspaceMaximumFileBytes: row.WorkspaceMaximumFileBytes,
		WorkspaceMaximumTotalBytes: row.WorkspaceMaximumTotalBytes,
	}
	if err := policy.Validate(); err != nil {
		return model.ExamCapacityPolicy{}, invalidPersistedState("exam_capacity", "policy", err)
	}
	return policy, nil
}

func currentExamCapacityPolicy(ctx context.Context, tx *sqlxTxWrapper) (model.ExamCapacityPolicy, error) {
	var row examCapacityPolicyRow
	if err := tx.Get(ctx, &row, `SELECT exam_resource_max_count,exam_resource_max_bytes,
		exam_workspace_max_entries,exam_workspace_max_file_bytes,exam_workspace_max_total_bytes
		FROM institutions WHERE singleton=TRUE AND archived_at IS NULL FOR SHARE`); err != nil {
		return model.ExamCapacityPolicy{}, fmt.Errorf("read institution Exam capacity policy: %w", err)
	}
	return row.policy()
}

func attemptWorkspaceCapacityPolicy(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID) (model.ExamCapacityPolicy, error) {
	var row examCapacityPolicyRow
	if err := tx.Get(ctx, &row, `SELECT r.exam_resource_max_count,r.exam_resource_max_bytes,
		r.exam_workspace_max_entries,r.exam_workspace_max_file_bytes,r.exam_workspace_max_total_bytes
		FROM exam_attempt_workspaces w JOIN exam_revisions r ON r.id=w.admission_revision_id
		WHERE w.id=? AND r.sealed=TRUE`, workspaceID.String()); err != nil {
		return model.ExamCapacityPolicy{}, fmt.Errorf("read Attempt Workspace capacity policy: %w", err)
	}
	return row.policy()
}
