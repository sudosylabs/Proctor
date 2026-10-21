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

	"github.com/stretchr/testify/require"

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
		{"cursor without recursive", func(c *DeleteWorkspaceEntryCommand) { c.Recursive = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			cursor := int64(0)
			command := DeleteWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
				EntryID: model.NewAttemptWorkspaceEntryID(), ExpectedPath: "src", Recursive: true, ExpectedWorkspaceCursor: &cursor, IdempotencyKey: "recursive"}
			test.configure(&command)
			_, err := f.service.DeleteCandidateExamWorkspaceEntry(context.Background(), f.invocation, command)
			require.Error(t, err)
			require.Nil(t, f.workspace.mutation)
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
	command := DeleteWorkspaceEntryCommand{Access: validWorkspaceMutationAccess(f),
		EntryID: entryID, ExpectedPath: "src", Recursive: true, ExpectedWorkspaceCursor: &cursor, IdempotencyKey: "recursive"}
	result, err := f.service.DeleteCandidateExamWorkspaceEntry(context.Background(), f.invocation, command)
	require.NoError(t, err, "result=%#v error=%v mutation", result, err)
	require.True(t, result.Change.Recursive, "result=%#v error=%v mutation", result, err)
	require.NotNil(t, f.workspace.mutation, "result=%#v error=%v mutation", result, err)
	require.True(t, f.workspace.mutation.Recursive, "result=%#v error=%v mutation", result, err)
	require.NotNil(t, f.workspace.mutation.ExpectedWorkspaceCursor, "result=%#v error=%v mutation", result, err)
	require.Equal(t, int64(0), *f.workspace.mutation.ExpectedWorkspaceCursor, "result=%#v error=%v mutation", result, err)
	first := *f.workspace.idempotency
	cursor = 1
	_, err = f.service.DeleteCandidateExamWorkspaceEntry(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.NotEqual(t, f.workspace.idempotency.Fingerprint, first.Fingerprint, "recursive snapshot was not part of the semantic fingerprint")
	require.Equal(t, f.workspace.idempotency.KeyDigest, first.KeyDigest, "recursive snapshot was not part of the semantic fingerprint")
}
