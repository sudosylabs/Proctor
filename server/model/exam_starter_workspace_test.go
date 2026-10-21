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

	"github.com/stretchr/testify/require"
)

func TestNormalizeStarterWorkspacePathAcceptsCanonicalPOSIXRelativePaths(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"main.go", "cmd/proctor/main.go", "src/épreuve.go", "nested/.proctor/file"} {
		got, err := NormalizeStarterWorkspacePath(value)
		require.NoError(t, err, "NormalizeStarterWorkspacePath")
		require.Equal(t, value, got, "NormalizeStarterWorkspacePath")
	}
}

func TestNormalizeStarterWorkspacePathRejectsUnsafeOrNonCanonicalPaths(t *testing.T) {
	t.Parallel()
	tooDeep := strings.Repeat("a/", StarterWorkspaceMaximumDepth) + "file"
	longSegment := strings.Repeat("é", StarterWorkspaceMaximumSegmentBytes/2+1)
	longPath := strings.Repeat("a/", StarterWorkspaceMaximumPathBytes/2) + "b"
	for _, value := range []string{
		"", "/main.go", "main.go/", "src//main.go", "./main.go", "src/../main.go",
		`src\main.go`, ".proctor", ".proctor/manifest.json", "src/\x00main.go", "src/\x1fmain.go",
		tooDeep, longSegment, longPath,
	} {
		got, err := NormalizeStarterWorkspacePath(value)
		require.Error(t, err, "NormalizeStarterWorkspacePath(%q) = %q, want error", value, got)
	}
}

func TestStarterWorkspaceEntrySeparatesDirectoriesFromFileObjects(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.FixedZone("offset", 3600))
	examID := ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee")
	directory, err := NewStarterWorkspaceDirectory(StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), examID, "cmd/proctor", at)
	require.NoError(t, err, "directory = %#v", directory)
	require.Equal(t, StarterWorkspaceEntryDirectory, directory.Kind, "directory = %#v", directory)
	require.True(t, directory.CurrentObjectID.IsZero(), "directory = %#v", directory)
	object := StarterWorkspaceObject{
		ID: StarterWorkspaceObjectID("oooooooooooooooooooooooooo"), ExamID: examID,
		CreatedByUserID: UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu"), State: StarterWorkspaceObjectCurrent,
		ContentVersion: WorkspaceContentVersion("vvvvvvvvvvvvvvvvvvvvvvvvvv"), MediaType: "text/x-go",
		SizeBytes: 0, SHA256: strings.Repeat("0", 64), CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at),
		ExpiresAt: TimeUTC(at).Add(time.Hour),
	}
	err = object.Validate()
	require.NoError(t, err)
	file, err := NewStarterWorkspaceFile(StarterWorkspaceEntryID("ffffffffffffffffffffffffff"), examID, "cmd/proctor/main.go", object.ID, at)
	require.NoError(t, err, "file = %#v", file)
	require.Equal(t, StarterWorkspaceEntryFile, file.Kind, "file = %#v", file)
	require.Equal(t, object.ID, file.CurrentObjectID, "file = %#v", file)
	file.CurrentObjectID = ""
	err = file.Validate()
	require.Error(t, err, "file without current object validated")
	directory.CurrentObjectID = object.ID
	err = directory.Validate()
	require.Error(t, err, "directory with content object validated")
}

func TestStarterWorkspaceArchivedFileMayReleaseItsCurrentObject(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	entry, err := NewStarterWorkspaceFile(NewStarterWorkspaceEntryID(), NewExamID(), "main.go", NewStarterWorkspaceObjectID(), at)
	require.NoError(t, err)
	entry.ArchivedAt = OptionalTimeFrom(at.Add(time.Minute))
	entry.UpdatedAt = at.Add(time.Minute)
	entry.CurrentObjectID = ""
	err = entry.Validate()
	require.NoError(t, err)
	entry.ArchivedAt = OptionalTime{}
	err = entry.Validate()
	require.Error(t, err, "active file without a current object was accepted")
}

func TestStarterWorkspaceObjectLifecycleIsExplicit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	object, err := NewStagedStarterWorkspaceObject(
		StarterWorkspaceObjectID("oooooooooooooooooooooooooo"),
		ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee"),
		UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu"), at, at.Add(StarterWorkspaceUploadLease),
	)
	require.NoError(t, err, "staged object = %#v", object)
	require.Equal(t, StarterWorkspaceObjectStaged, object.State, "staged object = %#v", object)
	require.True(t, object.ContentVersion.IsZero(), "staged object = %#v", object)
	err = object.MarkCurrent(WorkspaceContentVersion("vvvvvvvvvvvvvvvvvvvvvvvvvv"), "text/plain", 0, strings.Repeat("a", 64), at.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, StarterWorkspaceObjectCurrent, object.State)
	require.False(t, object.ContentVersion.IsZero())
}

func TestStarterWorkspaceCleanupClaimMayRepresentAnExpiredUnfinishedUpload(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	object := StarterWorkspaceObject{
		ID: StarterWorkspaceObjectID("oooooooooooooooooooooooooo"), ExamID: ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee"),
		CreatedByUserID: UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu"), CreatedAt: at, UpdatedAt: at.Add(26 * time.Hour),
		ExpiresAt: at.Add(time.Hour), State: StarterWorkspaceObjectClaimed,
		ReclaimAfter: OptionalTimeFrom(at.Add(25 * time.Hour)), ClaimToken: "cleanup-claim", ClaimedAt: OptionalTimeFrom(at.Add(26 * time.Hour)),
	}
	require.NoError(t, object.Validate())
	object.State = StarterWorkspaceObjectCurrent
	object.ReclaimAfter = OptionalTime{}
	object.ClaimToken = ""
	object.ClaimedAt = OptionalTime{}
	require.Error(t, object.Validate(), "metadata-less current object validated")
}
