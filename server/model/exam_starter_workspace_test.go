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
)

func TestNormalizeStarterWorkspacePathAcceptsCanonicalPOSIXRelativePaths(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"main.go", "cmd/proctor/main.go", "src/épreuve.go", "nested/.proctor/file"} {
		got, err := NormalizeStarterWorkspacePath(value)
		if err != nil || got != value {
			t.Fatalf("NormalizeStarterWorkspacePath(%q) = %q, %v", value, got, err)
		}
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
		if got, err := NormalizeStarterWorkspacePath(value); err == nil {
			t.Fatalf("NormalizeStarterWorkspacePath(%q) = %q, want error", value, got)
		}
	}
}

func TestStarterWorkspaceEntrySeparatesDirectoriesFromFileObjects(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.FixedZone("offset", 3600))
	examID := ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee")
	directory, err := NewStarterWorkspaceDirectory(StarterWorkspaceEntryID("dddddddddddddddddddddddddd"), examID, "cmd/proctor", at)
	if err != nil || directory.Kind != StarterWorkspaceEntryDirectory || !directory.CurrentObjectID.IsZero() {
		t.Fatalf("directory = %#v, %v", directory, err)
	}
	object := StarterWorkspaceObject{
		ID: StarterWorkspaceObjectID("oooooooooooooooooooooooooo"), ExamID: examID,
		CreatedByUserID: UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu"), State: StarterWorkspaceObjectCurrent,
		ContentVersion: WorkspaceContentVersion("vvvvvvvvvvvvvvvvvvvvvvvvvv"), MediaType: "text/x-go",
		SizeBytes: 0, SHA256: strings.Repeat("0", 64), CreatedAt: TimeUTC(at), UpdatedAt: TimeUTC(at),
		ExpiresAt: TimeUTC(at).Add(time.Hour),
	}
	if err = object.Validate(); err != nil {
		t.Fatalf("valid current object: %v", err)
	}
	file, err := NewStarterWorkspaceFile(StarterWorkspaceEntryID("ffffffffffffffffffffffffff"), examID, "cmd/proctor/main.go", object.ID, at)
	if err != nil || file.Kind != StarterWorkspaceEntryFile || file.CurrentObjectID != object.ID {
		t.Fatalf("file = %#v, %v", file, err)
	}
	file.CurrentObjectID = ""
	if err = file.Validate(); err == nil {
		t.Fatal("file without current object validated")
	}
	directory.CurrentObjectID = object.ID
	if err = directory.Validate(); err == nil {
		t.Fatal("directory with content object validated")
	}
}

func TestStarterWorkspaceArchivedFileMayReleaseItsCurrentObject(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	entry, err := NewStarterWorkspaceFile(NewStarterWorkspaceEntryID(), NewExamID(), "main.go", NewStarterWorkspaceObjectID(), at)
	if err != nil {
		t.Fatal(err)
	}
	entry.ArchivedAt = OptionalTimeFrom(at.Add(time.Minute))
	entry.UpdatedAt = at.Add(time.Minute)
	entry.CurrentObjectID = ""
	if err = entry.Validate(); err != nil {
		t.Fatalf("archived file without retained object = %v", err)
	}
	entry.ArchivedAt = OptionalTime{}
	if err = entry.Validate(); err == nil {
		t.Fatal("active file without a current object was accepted")
	}
}

func TestStarterWorkspaceObjectLifecycleIsExplicit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	object, err := NewStagedStarterWorkspaceObject(
		StarterWorkspaceObjectID("oooooooooooooooooooooooooo"),
		ExamID("eeeeeeeeeeeeeeeeeeeeeeeeee"),
		UserID("uuuuuuuuuuuuuuuuuuuuuuuuuu"), at, at.Add(StarterWorkspaceUploadLease),
	)
	if err != nil || object.State != StarterWorkspaceObjectStaged || !object.ContentVersion.IsZero() {
		t.Fatalf("staged object = %#v, %v", object, err)
	}
	if err = object.MarkCurrent(WorkspaceContentVersion("vvvvvvvvvvvvvvvvvvvvvvvvvv"), "text/plain", 0, strings.Repeat("a", 64), at.Add(time.Minute)); err != nil {
		t.Fatalf("mark current: %v", err)
	}
	if object.State != StarterWorkspaceObjectCurrent || object.ContentVersion.IsZero() {
		t.Fatalf("current object = %#v", object)
	}
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
	if err := object.Validate(); err != nil {
		t.Fatalf("metadata-less cleanup claim: %v", err)
	}
	object.State = StarterWorkspaceObjectCurrent
	object.ReclaimAfter = OptionalTime{}
	object.ClaimToken = ""
	object.ClaimedAt = OptionalTime{}
	if err := object.Validate(); err == nil {
		t.Fatal("metadata-less current object validated")
	}
}
