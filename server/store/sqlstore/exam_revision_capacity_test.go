// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamRevisionCapacityExceededChecksEveryPolicyDimension(t *testing.T) {
	t.Parallel()
	resource := model.ExamRevisionResource{SizeBytes: 8}
	file := model.ExamRevisionStarterWorkspaceEntry{SizeBytes: 12}
	resourceCount := model.DefaultExamCapacityPolicy()
	resourceCount.ResourceMaximumCount = 1
	resourceBytes := model.DefaultExamCapacityPolicy()
	resourceBytes.ResourceMaximumBytes = 7
	workspaceEntries := model.DefaultExamCapacityPolicy()
	workspaceEntries.WorkspaceMaximumEntries = 1
	workspaceFileBytes := model.DefaultExamCapacityPolicy()
	workspaceFileBytes.WorkspaceMaximumFileBytes = 11
	workspaceTotalBytes := model.DefaultExamCapacityPolicy()
	workspaceTotalBytes.WorkspaceMaximumFileBytes = 12
	workspaceTotalBytes.WorkspaceMaximumTotalBytes = 20
	for _, test := range []struct {
		name      string
		policy    model.ExamCapacityPolicy
		resources []model.ExamRevisionResource
		workspace []model.ExamRevisionStarterWorkspaceEntry
	}{
		{name: "resource count", policy: resourceCount, resources: make([]model.ExamRevisionResource, 2)},
		{name: "resource bytes", policy: resourceBytes, resources: []model.ExamRevisionResource{resource}},
		{name: "workspace entries", policy: workspaceEntries, workspace: make([]model.ExamRevisionStarterWorkspaceEntry, 2)},
		{name: "workspace file bytes", policy: workspaceFileBytes, workspace: []model.ExamRevisionStarterWorkspaceEntry{file}},
		{name: "workspace total bytes", policy: workspaceTotalBytes, workspace: []model.ExamRevisionStarterWorkspaceEntry{file, file}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !examRevisionCapacityExceeded(test.policy, test.resources, test.workspace) {
				t.Fatal("capacity excess was accepted")
			}
		})
	}
	if examRevisionCapacityExceeded(model.DefaultExamCapacityPolicy(), []model.ExamRevisionResource{resource}, []model.ExamRevisionStarterWorkspaceEntry{file}) {
		t.Fatal("bounded content was rejected")
	}
}
