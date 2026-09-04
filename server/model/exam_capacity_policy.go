// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "fmt"

const (
	ExamResourceDefaultMaximumCount       = 10
	ExamResourceDefaultMaximumBytes int64 = 10 << 20
	ExamResourceMaximumCount              = 100
	ExamResourceMaximumBytes        int64 = 100 << 20

	ExamWorkspaceDefaultMaximumEntries          = 500
	ExamWorkspaceDefaultMaximumFileBytes  int64 = 10 << 20
	ExamWorkspaceDefaultMaximumTotalBytes int64 = 50 << 20
	ExamWorkspaceMaximumEntries                 = 5000
	ExamWorkspaceMaximumFileBytes         int64 = 100 << 20
	ExamWorkspaceMaximumTotalBytes        int64 = 1 << 30
)

// ExamCapacityPolicy is the institution-owned admission policy for authored
// Exam content and candidate Workspace growth. Published Revisions freeze the
// policy so later institution changes cannot alter a Sitting in progress.
// Fixed constants above remain server safety ceilings and are not mutable
// institution policy.
type ExamCapacityPolicy struct {
	ResourceMaximumCount       int   `json:"resource_maximum_count"`
	ResourceMaximumBytes       int64 `json:"resource_maximum_bytes"`
	WorkspaceMaximumEntries    int   `json:"workspace_maximum_entries"`
	WorkspaceMaximumFileBytes  int64 `json:"workspace_maximum_file_bytes"`
	WorkspaceMaximumTotalBytes int64 `json:"workspace_maximum_total_bytes"`
}

func DefaultExamCapacityPolicy() ExamCapacityPolicy {
	return ExamCapacityPolicy{
		ResourceMaximumCount:       ExamResourceDefaultMaximumCount,
		ResourceMaximumBytes:       ExamResourceDefaultMaximumBytes,
		WorkspaceMaximumEntries:    ExamWorkspaceDefaultMaximumEntries,
		WorkspaceMaximumFileBytes:  ExamWorkspaceDefaultMaximumFileBytes,
		WorkspaceMaximumTotalBytes: ExamWorkspaceDefaultMaximumTotalBytes,
	}
}

func (policy ExamCapacityPolicy) IsZero() bool { return policy == (ExamCapacityPolicy{}) }

func (policy ExamCapacityPolicy) Validate() error {
	if policy.ResourceMaximumCount < 1 || policy.ResourceMaximumCount > ExamResourceMaximumCount {
		return fmt.Errorf("model: Exam resource maximum count must be between 1 and %d", ExamResourceMaximumCount)
	}
	if policy.ResourceMaximumBytes < 1 || policy.ResourceMaximumBytes > ExamResourceMaximumBytes {
		return fmt.Errorf("model: Exam resource maximum bytes must be between 1 and %d", ExamResourceMaximumBytes)
	}
	if policy.WorkspaceMaximumEntries < 1 || policy.WorkspaceMaximumEntries > ExamWorkspaceMaximumEntries {
		return fmt.Errorf("model: Exam Workspace maximum entries must be between 1 and %d", ExamWorkspaceMaximumEntries)
	}
	if policy.WorkspaceMaximumFileBytes < 1 || policy.WorkspaceMaximumFileBytes > ExamWorkspaceMaximumFileBytes {
		return fmt.Errorf("model: Exam Workspace file maximum bytes must be between 1 and %d", ExamWorkspaceMaximumFileBytes)
	}
	if policy.WorkspaceMaximumTotalBytes < policy.WorkspaceMaximumFileBytes || policy.WorkspaceMaximumTotalBytes > ExamWorkspaceMaximumTotalBytes {
		return fmt.Errorf("model: Exam Workspace total maximum bytes must be between the file limit and %d", ExamWorkspaceMaximumTotalBytes)
	}
	return nil
}
