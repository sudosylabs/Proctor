// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttemptWorkspaceLimitsAndPathsAreBounded(t *testing.T) {
	t.Parallel()
	require.Equal(t, 5000, AttemptWorkspaceMaximumEntries, "Attempt Workspace limits changed")
	require.Equal(t, 16, AttemptWorkspaceMaximumDepth, "Attempt Workspace limits changed")
	require.Equal(t, 255, AttemptWorkspaceMaximumSegmentBytes, "Attempt Workspace limits changed")
	require.Equal(t, 1024, AttemptWorkspaceMaximumPathBytes, "Attempt Workspace limits changed")
	require.Equal(t, AttemptWorkspaceMaximumFileBytes, int64(100<<20), "Attempt Workspace limits changed")
	require.Equal(t, AttemptWorkspaceMaximumTotalBytes, int64(1<<30), "Attempt Workspace limits changed")
	require.Equal(t, AttemptWorkspaceMaximumRequestBytes, int64((100<<20)+(64<<10)), "Attempt Workspace limits changed")
	require.Equal(t, 4096, AttemptWorkspaceJournalRetention, "Attempt Workspace limits changed")
	require.Equal(t, 200, AttemptWorkspaceJournalReadMaximum, "Attempt Workspace limits changed")
	for _, path := range []string{"main.go", "cmd/proctor/main.go", strings.Repeat("a", 255)} {
		normalized, err := NormalizeAttemptWorkspacePath(path)
		assert.NoError(t, err, "NormalizeAttemptWorkspacePath")
		assert.Equal(t, path, normalized, "NormalizeAttemptWorkspacePath")
	}
	for _, path := range []string{"", "/main.go", "cmd//main.go", "cmd/../main.go", `.proctor/state`, `cmd\\main.go`,
		strings.Repeat("a/", AttemptWorkspaceMaximumDepth) + "main.go",
		strings.Repeat("é", AttemptWorkspaceMaximumSegmentBytes/2+1),
		strings.Repeat("a/", AttemptWorkspaceMaximumPathBytes/2) + "b",
		"cmd/line\nbreak.go", string([]byte{0xff}),
	} {
		_, err := NormalizeAttemptWorkspacePath(path)
		assert.Error(t, err, "NormalizeAttemptWorkspacePath(%q) succeeded", path)
	}
}

func TestAttemptWorkspaceCandidateEntriesRetainIdentityAcrossMoveAndReplace(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	workspace, err := NewExamAttemptWorkspace(NewExamAttemptWorkspaceID(), NewExamAttemptID(), at)
	require.NoError(t, err)
	object, err := NewAttemptOriginAttemptWorkspaceObject(NewAttemptWorkspaceObjectID(), workspace.ID,
		NewWorkspaceContentVersion(), "text/x-go", 12, strings.Repeat("a", 64), at)
	require.NoError(t, err)
	entry, err := NewCandidateAttemptWorkspaceFile(NewAttemptWorkspaceEntryID(), workspace.ID, NewExamRevisionID(),
		"main.go", object.ID, at)
	require.NoError(t, err)
	entryID := entry.ID
	require.True(t, entry.SourceStarterEntryID.IsZero(), "candidate entry=%#v object=%#v", entry, object)
	require.Equal(t, AttemptWorkspaceStorageAttempt, object.StorageOrigin, "candidate entry=%#v object=%#v", entry, object)
	require.True(t, object.StarterObjectID.IsZero(), "candidate entry=%#v object=%#v", entry, object)
	err = entry.Move("cmd/main.go", at.Add(time.Second))
	require.NoError(t, err, "Move() entry=%#v error", entry)
	require.Equal(t, entryID, entry.ID, "Move() entry=%#v error", entry)
	require.Equal(t, "cmd/main.go", entry.Path, "Move() entry=%#v error", entry)
	nextObjectID := NewAttemptWorkspaceObjectID()
	err = entry.ReplaceCurrentObject(nextObjectID, at.Add(2*time.Second))
	require.NoError(t, err, "ReplaceCurrentObject() entry=%#v error", entry)
	require.Equal(t, entryID, entry.ID, "ReplaceCurrentObject() entry=%#v error", entry)
	require.Equal(t, nextObjectID, entry.CurrentObjectID, "ReplaceCurrentObject() entry=%#v error", entry)
	err = workspace.AdvanceCursor(0, at.Add(2*time.Second))
	require.NoError(t, err, "AdvanceCursor() workspace=%#v error", workspace)
	require.Equal(t, int64(1), workspace.Cursor, "AdvanceCursor() workspace=%#v error", workspace)
	err = workspace.AdvanceCursor(0, at.Add(3*time.Second))
	require.Error(t, err, "stale AdvanceCursor() workspace=%#v error", workspace)
	require.Equal(t, int64(1), workspace.Cursor, "stale AdvanceCursor() workspace=%#v error", workspace)
}

func TestAttemptWorkspaceJournalDescribesSafeOrderedAcknowledgements(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	workspaceID, entryID := NewExamAttemptWorkspaceID(), NewAttemptWorkspaceEntryID()
	version := NewWorkspaceContentVersion()
	entry := AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 3, EntryID: entryID,
		EntryKind: StarterWorkspaceEntryFile, Operation: AttemptWorkspaceMutationReplaceFile,
		OldPath: "cmd/main.go", NewPath: "cmd/main.go", ContentVersion: version, ChangedAt: at}
	require.NoError(t, entry.Validate())
	move := entry
	move.Cursor, move.Operation, move.NewPath = 4, AttemptWorkspaceMutationMoveEntry, "internal/main.go"
	require.NoError(t, move.Validate())
	deleted := entry
	deleted.Cursor, deleted.Operation, deleted.NewPath, deleted.ContentVersion = 5, AttemptWorkspaceMutationDeleteEntry, "", ""
	require.NoError(t, deleted.Validate())
	deleted.Recursive = true
	require.Error(t, deleted.Validate(), "journal accepted recursive file deletion")
	deleted.EntryKind = StarterWorkspaceEntryDirectory
	require.NoError(t, deleted.Validate())
	move.Recursive = true
	require.Error(t, move.Validate(), "journal accepted a recursive non-deletion")
	move.Recursive = false
	bad := move
	bad.NewPath = "../escape"
	require.Error(t, bad.Validate(), "journal accepted an unsafe path")
	bad = entry
	bad.Cursor = 0
	require.Error(t, bad.Validate(), "journal accepted cursor zero")
}

func TestAttemptWorkspaceObjectLifecycleFencesVisibilityAndCleanup(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	object, err := NewStagedAttemptWorkspaceObject(NewAttemptWorkspaceObjectID(), NewExamAttemptWorkspaceID(), at,
		at.Add(AttemptWorkspaceStageLifetime))
	require.NoError(t, err, "NewStagedAttemptWorkspaceObject() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectStaged, object.State, "NewStagedAttemptWorkspaceObject() = %#v", object)
	require.False(t, object.HasContent(), "NewStagedAttemptWorkspaceObject() = %#v", object)
	version := NewWorkspaceContentVersion()
	err = object.MarkContentReady(version, "text/x-go", 12, strings.Repeat("b", 64), at.Add(time.Second))
	require.NoError(t, err, "MarkContentReady() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectStaged, object.State, "MarkContentReady() = %#v", object)
	require.True(t, object.HasContent(), "MarkContentReady() = %#v", object)
	err = object.MarkCurrent(at.Add(2 * time.Second))
	require.NoError(t, err, "MarkCurrent() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectCurrent, object.State, "MarkCurrent() = %#v", object)
	require.True(t, object.ExpiresAt.IsZero(), "MarkCurrent() = %#v", object)
	reclaimAfter := at.Add(AttemptWorkspaceReclaimSafetyWindow)
	err = object.MarkReclaimable(reclaimAfter, at.Add(3*time.Second))
	require.NoError(t, err, "MarkReclaimable() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectReclaimable, object.State, "MarkReclaimable() = %#v", object)
	claimAt := reclaimAfter.Add(time.Second)
	err = object.ClaimForCleanup("node-1/claim-1", claimAt)
	require.NoError(t, err, "ClaimForCleanup() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectClaimed, object.State, "ClaimForCleanup() = %#v", object)
	err = object.ReleaseCleanup("node-1/claim-1", claimAt.Add(-time.Nanosecond))
	require.Error(t, err, "time-regressing ReleaseCleanup() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectClaimed, object.State, "time-regressing ReleaseCleanup() = %#v", object)
	err = object.ReleaseCleanup("node-1/claim-1", claimAt.Add(time.Second))
	require.NoError(t, err, "ReleaseCleanup() = %#v", object)
	require.Equal(t, AttemptWorkspaceObjectReclaimable, object.State, "ReleaseCleanup() = %#v", object)
}
