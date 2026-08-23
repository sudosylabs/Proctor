// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"testing"
	"time"
)

func TestAttemptWorkspaceLimitsAndPathsAreBounded(t *testing.T) {
	t.Parallel()
	if AttemptWorkspaceMaximumEntries != 5000 || AttemptWorkspaceMaximumDepth != 16 ||
		AttemptWorkspaceMaximumSegmentBytes != 255 || AttemptWorkspaceMaximumPathBytes != 1024 ||
		AttemptWorkspaceMaximumFileBytes != 100<<20 || AttemptWorkspaceMaximumTotalBytes != 1<<30 ||
		AttemptWorkspaceMaximumRequestBytes != (100<<20)+(64<<10) ||
		AttemptWorkspaceJournalRetention != 4096 || AttemptWorkspaceJournalReadMaximum != 200 {
		t.Fatalf("Attempt Workspace limits changed")
	}
	for _, path := range []string{"main.go", "cmd/proctor/main.go", strings.Repeat("a", 255)} {
		if normalized, err := NormalizeAttemptWorkspacePath(path); err != nil || normalized != path {
			t.Errorf("NormalizeAttemptWorkspacePath(%q) = %q, %v", path, normalized, err)
		}
	}
	for _, path := range []string{"", "/main.go", "cmd//main.go", "cmd/../main.go", `.proctor/state`, `cmd\\main.go`,
		strings.Repeat("a/", AttemptWorkspaceMaximumDepth) + "main.go",
		strings.Repeat("é", AttemptWorkspaceMaximumSegmentBytes/2+1),
		strings.Repeat("a/", AttemptWorkspaceMaximumPathBytes/2) + "b",
		"cmd/line\nbreak.go", string([]byte{0xff}),
	} {
		if _, err := NormalizeAttemptWorkspacePath(path); err == nil {
			t.Errorf("NormalizeAttemptWorkspacePath(%q) succeeded", path)
		}
	}
}

func TestAttemptWorkspaceCandidateEntriesRetainIdentityAcrossMoveAndReplace(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	workspace, err := NewExamAttemptWorkspace(NewExamAttemptWorkspaceID(), NewExamAttemptID(), at)
	if err != nil {
		t.Fatal(err)
	}
	object, err := NewAttemptOriginAttemptWorkspaceObject(NewAttemptWorkspaceObjectID(), workspace.ID,
		NewWorkspaceContentVersion(), "text/x-go", 12, strings.Repeat("a", 64), at)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewCandidateAttemptWorkspaceFile(NewAttemptWorkspaceEntryID(), workspace.ID, NewExamRevisionID(),
		"main.go", object.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	entryID := entry.ID
	if !entry.SourceStarterEntryID.IsZero() || object.StorageOrigin != AttemptWorkspaceStorageAttempt || !object.StarterObjectID.IsZero() {
		t.Fatalf("candidate entry=%#v object=%#v", entry, object)
	}
	if err = entry.Move("cmd/main.go", at.Add(time.Second)); err != nil || entry.ID != entryID || entry.Path != "cmd/main.go" {
		t.Fatalf("Move() entry=%#v error=%v", entry, err)
	}
	nextObjectID := NewAttemptWorkspaceObjectID()
	if err = entry.ReplaceCurrentObject(nextObjectID, at.Add(2*time.Second)); err != nil || entry.ID != entryID || entry.CurrentObjectID != nextObjectID {
		t.Fatalf("ReplaceCurrentObject() entry=%#v error=%v", entry, err)
	}
	if err = workspace.AdvanceCursor(0, at.Add(2*time.Second)); err != nil || workspace.Cursor != 1 {
		t.Fatalf("AdvanceCursor() workspace=%#v error=%v", workspace, err)
	}
	if err = workspace.AdvanceCursor(0, at.Add(3*time.Second)); err == nil || workspace.Cursor != 1 {
		t.Fatalf("stale AdvanceCursor() workspace=%#v error=%v", workspace, err)
	}
}

func TestAttemptWorkspaceJournalDescribesSafeOrderedAcknowledgements(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	workspaceID, entryID := NewExamAttemptWorkspaceID(), NewAttemptWorkspaceEntryID()
	version := NewWorkspaceContentVersion()
	entry := AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: 3, EntryID: entryID,
		EntryKind: StarterWorkspaceEntryFile, Operation: AttemptWorkspaceMutationReplaceFile,
		OldPath: "cmd/main.go", NewPath: "cmd/main.go", ContentVersion: version, ChangedAt: at}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	move := entry
	move.Cursor, move.Operation, move.NewPath = 4, AttemptWorkspaceMutationMoveEntry, "internal/main.go"
	if err := move.Validate(); err != nil {
		t.Fatal(err)
	}
	deleted := entry
	deleted.Cursor, deleted.Operation, deleted.NewPath, deleted.ContentVersion = 5, AttemptWorkspaceMutationDeleteEntry, "", ""
	if err := deleted.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := move
	bad.NewPath = "../escape"
	if err := bad.Validate(); err == nil {
		t.Fatal("journal accepted an unsafe path")
	}
	bad = entry
	bad.Cursor = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("journal accepted cursor zero")
	}
}

func TestAttemptWorkspaceObjectLifecycleFencesVisibilityAndCleanup(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	object, err := NewStagedAttemptWorkspaceObject(NewAttemptWorkspaceObjectID(), NewExamAttemptWorkspaceID(), at,
		at.Add(AttemptWorkspaceStageLifetime))
	if err != nil || object.State != AttemptWorkspaceObjectStaged || object.HasContent() {
		t.Fatalf("NewStagedAttemptWorkspaceObject() = %#v, %v", object, err)
	}
	version := NewWorkspaceContentVersion()
	if err = object.MarkContentReady(version, "text/x-go", 12, strings.Repeat("b", 64), at.Add(time.Second)); err != nil ||
		object.State != AttemptWorkspaceObjectStaged || !object.HasContent() {
		t.Fatalf("MarkContentReady() = %#v, %v", object, err)
	}
	if err = object.MarkCurrent(at.Add(2 * time.Second)); err != nil || object.State != AttemptWorkspaceObjectCurrent || !object.ExpiresAt.IsZero() {
		t.Fatalf("MarkCurrent() = %#v, %v", object, err)
	}
	reclaimAfter := at.Add(AttemptWorkspaceReclaimSafetyWindow)
	if err = object.MarkReclaimable(reclaimAfter, at.Add(3*time.Second)); err != nil || object.State != AttemptWorkspaceObjectReclaimable {
		t.Fatalf("MarkReclaimable() = %#v, %v", object, err)
	}
	claimAt := reclaimAfter.Add(time.Second)
	if err = object.ClaimForCleanup("node-1/claim-1", claimAt); err != nil || object.State != AttemptWorkspaceObjectClaimed {
		t.Fatalf("ClaimForCleanup() = %#v, %v", object, err)
	}
	if err = object.ReleaseCleanup("node-1/claim-1", claimAt.Add(-time.Nanosecond)); err == nil || object.State != AttemptWorkspaceObjectClaimed {
		t.Fatalf("time-regressing ReleaseCleanup() = %#v, %v", object, err)
	}
	if err = object.ReleaseCleanup("node-1/claim-1", claimAt.Add(time.Second)); err != nil || object.State != AttemptWorkspaceObjectReclaimable {
		t.Fatalf("ReleaseCleanup() = %#v, %v", object, err)
	}
}
