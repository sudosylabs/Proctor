// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRecursiveWorkspaceDeletionRequiresCandidateSnapshot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		configure func(*DeleteWorkspaceEntryCommand)
	}{
		{"missing cursor", func(c *DeleteWorkspaceEntryCommand) { c.ExpectedWorkspaceCursor = nil }},
		{"negative cursor", func(c *DeleteWorkspaceEntryCommand) { value := int64(-1); c.ExpectedWorkspaceCursor = &value }},
		{"content version", func(c *DeleteWorkspaceEntryCommand) { c.ExpectedContentVersion = model.NewWorkspaceContentVersion() }},
		{"execution host", func(c *DeleteWorkspaceEntryCommand) { c.Origin = WorkspaceMutationOriginExecutionHost }},
		{"cursor without recursive", func(c *DeleteWorkspaceEntryCommand) { c.Recursive = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			cursor := int64(0)
			command := DeleteWorkspaceEntryCommand{Origin: WorkspaceMutationOriginCandidate, Access: validWorkspaceMutationAccess(f),
				EntryID: model.NewAttemptWorkspaceEntryID(), ExpectedPath: "src", Recursive: true, ExpectedWorkspaceCursor: &cursor, IdempotencyKey: "recursive"}
			test.configure(&command)
			if _, err := f.service.DeleteWorkspaceEntry(context.Background(), f.call, command); err == nil || f.workspace.mutation != nil {
				t.Fatalf("error=%v mutation=%#v", err, f.workspace.mutation)
			}
		})
	}
}

func TestRecursiveWorkspaceDeletionCarriesSnapshotAndSemanticIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	workspaceID, entryID := model.NewExamAttemptWorkspaceID(), model.NewAttemptWorkspaceEntryID()
	f.workspace.target = &store.ExamAttemptWorkspaceMutationTarget{ExamID: f.sitting.ExamID, SittingID: f.sitting.ID,
		ClassID: f.sitting.ClassID, CandidateUserID: f.userID, WorkspaceID: workspaceID}
	f.workspace.mutationResult = workspaceMutationResultFixture(f, workspaceID, entryID, model.StarterWorkspaceEntryDirectory,
		model.AttemptWorkspaceMutationDeleteEntry, "src", "", "", false)
	f.workspace.mutationResult.Entry = nil
	f.workspace.mutationResult.Change.Recursive = true
	cursor := int64(0)
	command := DeleteWorkspaceEntryCommand{Origin: WorkspaceMutationOriginCandidate, Access: validWorkspaceMutationAccess(f),
		EntryID: entryID, ExpectedPath: "src", Recursive: true, ExpectedWorkspaceCursor: &cursor, IdempotencyKey: "recursive"}
	result, err := f.service.DeleteWorkspaceEntry(context.Background(), f.call, command)
	if err != nil || !result.Change.Recursive || f.workspace.mutation == nil || !f.workspace.mutation.Recursive ||
		f.workspace.mutation.ExpectedWorkspaceCursor == nil || *f.workspace.mutation.ExpectedWorkspaceCursor != 0 {
		t.Fatalf("result=%#v error=%v mutation=%#v", result, err, f.workspace.mutation)
	}
	first := *f.workspace.idempotency
	cursor = 1
	if _, err = f.service.DeleteWorkspaceEntry(context.Background(), f.call, command); err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == f.workspace.idempotency.Fingerprint || first.KeyDigest != f.workspace.idempotency.KeyDigest {
		t.Fatal("recursive snapshot was not part of the semantic fingerprint")
	}
}
