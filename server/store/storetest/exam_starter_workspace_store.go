// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestExamStarterWorkspaceStore verifies the bounded hierarchy, replay, move,
// content-pointer replacement, and nonempty-directory guarantees shared by
// every adapter.
func TestExamStarterWorkspaceStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "starter-workspace-unit")
	creator := saveUser(t, ctx, ss)
	at := model.NowUTC()
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "starter-workspace-exam")
	examID := created.Value.Exam.ID

	directoryID := model.NewStarterWorkspaceEntryID()
	directory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 1, directoryID, "cmd", at.Add(time.Second))
	directoryResult, err := ss.ExamStarterWorkspace().CreateDirectory(ctx, directory, examCommand(creator.ID, "exam.starter_workspace.directory.create.v1", "workspace-dir", "workspace-dir-command"))
	requireNoError(t, err)
	if directoryResult.DraftRevision != 2 || directoryResult.Entry.Path != "cmd" {
		t.Fatalf("directory result = %#v", directoryResult)
	}
	subdirectoryID := model.NewStarterWorkspaceEntryID()
	subdirectory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 2, subdirectoryID, "cmd/app", at.Add(2*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, subdirectory, examCommand(creator.ID, "exam.starter_workspace.directory.create.v1", "workspace-subdir", "workspace-subdir-command"))
	requireNoError(t, err)

	fileID := model.NewStarterWorkspaceEntryID()
	fileMutation := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, fileID, "cmd/app/main.go", at.Add(3*time.Second), 13)
	fileCommand := examCommand(creator.ID, "exam.starter_workspace.file.create.v1", "workspace-file", "workspace-file-command")
	fileResult, err := ss.ExamStarterWorkspace().CreateFile(ctx, fileMutation, fileCommand)
	requireNoError(t, err)
	if fileResult.DraftRevision != 4 || fileResult.Object == nil || fileResult.Entry.CurrentObjectID != fileResult.Object.ID {
		t.Fatalf("file result = %#v", fileResult)
	}
	// The exact retry arrives after the first finalize advanced the Draft. Its
	// fresh opaque reservation must remain possible so the named idempotent
	// mutation can return the already-committed outcome.
	replay := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, model.NewStarterWorkspaceEntryID(), "cmd/app/main.go", at.Add(3*time.Second+2*time.Millisecond), 13)
	replayed, err := ss.ExamStarterWorkspace().CreateFile(ctx, replay, fileCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.DraftRevision != 4 || replayed.Entry.ID != fileID {
		t.Fatalf("file replay = %#v", replayed)
	}

	items, err := ss.ExamStarterWorkspace().List(ctx, examID)
	requireNoError(t, err)
	if len(items) != 3 || items[2].Entry.Path != "cmd/app/main.go" {
		t.Fatalf("initial hierarchy = %#v", items)
	}

	move := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 4, directoryID, "src", at.Add(4*time.Second))
	moved, err := ss.ExamStarterWorkspace().MoveEntry(ctx, move, examCommand(creator.ID, "exam.starter_workspace.entry.move.v1", "workspace-move", "workspace-move-command"))
	requireNoError(t, err)
	if moved.DraftRevision != 5 || moved.Entry.Path != "src" {
		t.Fatalf("move result = %#v", moved)
	}
	items, err = ss.ExamStarterWorkspace().List(ctx, examID)
	requireNoError(t, err)
	if len(items) != 3 || items[0].Entry.Path != "src" || items[1].Entry.Path != "src/app" || items[2].Entry.Path != "src/app/main.go" {
		t.Fatalf("moved hierarchy = %#v", items)
	}

	nonempty := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 5, directoryID, "", at.Add(5*time.Second))
	_, err = ss.ExamStarterWorkspace().RemoveEntry(ctx, nonempty, examCommand(creator.ID, "exam.starter_workspace.entry.remove.v1", "workspace-nonempty", "workspace-nonempty-command"))
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) || conflict.Constraint != "workspace_directory_not_empty" {
		t.Fatalf("nonempty directory error = %v", err)
	}

	oldObjectID := fileResult.Object.ID
	staleReplacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(5*time.Second+time.Millisecond), 5)
	staleReplacement.ExpectedContentVersion = model.NewWorkspaceContentVersion()
	_, err = ss.ExamStarterWorkspace().ReplaceFile(ctx, staleReplacement, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "workspace-replace-stale", "workspace-replace-stale-command"))
	if !errors.As(err, &conflict) || conflict.Constraint != "workspace_content_version" {
		t.Fatalf("stale content version error = %v", err)
	}
	replacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(6*time.Second), 5)
	replacement.ExpectedContentVersion = fileResult.Object.ContentVersion
	replaced, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, replacement, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "workspace-replace", "workspace-replace-command"))
	requireNoError(t, err)
	if replaced.DraftRevision != 6 || replaced.Object == nil || replaced.Object.ID == oldObjectID || replaced.ReclaimableObject != oldObjectID {
		t.Fatalf("replacement = %#v", replaced)
	}
	replacementReplay := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(6*time.Second+time.Millisecond), 5)
	replacementReplay.ExpectedContentVersion = fileResult.Object.ContentVersion
	replayedReplacement, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, replacementReplay, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "workspace-replace", "workspace-replace-command"))
	requireNoError(t, err)
	if !replayedReplacement.Replayed || replayedReplacement.DraftRevision != 6 || replayedReplacement.Object == nil || replayedReplacement.Object.ID != replaced.Object.ID {
		t.Fatalf("replacement replay = %#v", replayedReplacement)
	}
	got, err := ss.ExamStarterWorkspace().GetFile(ctx, examID, fileID)
	requireNoError(t, err)
	if got.Object == nil || got.Object.ID != replaced.Object.ID || got.Entry.Path != "src/app/main.go" {
		t.Fatalf("current file = %#v", got)
	}

	// Two callers can reserve independently against the same Draft revision,
	// but the ordered Draft/entry locks admit exactly one conditional replace.
	raceFirst := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 6, fileID, "", at.Add(6*time.Second+time.Millisecond), 6)
	raceSecond := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 6, fileID, "", at.Add(6*time.Second+2*time.Millisecond), 7)
	raceFirst.ExpectedContentVersion = replaced.Object.ContentVersion
	raceSecond.ExpectedContentVersion = replaced.Object.ContentVersion
	type raceOutcome struct {
		result *store.ExamStarterWorkspaceMutationResult
		err    error
	}
	raceResults := make(chan raceOutcome, 2)
	go func() {
		result, replaceErr := ss.ExamStarterWorkspace().ReplaceFile(ctx, raceFirst, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "workspace-race-first", "workspace-race-first-command"))
		raceResults <- raceOutcome{result: result, err: replaceErr}
	}()
	go func() {
		result, replaceErr := ss.ExamStarterWorkspace().ReplaceFile(ctx, raceSecond, examCommand(creator.ID, "exam.starter_workspace.file.replace.v1", "workspace-race-second", "workspace-race-second-command"))
		raceResults <- raceOutcome{result: result, err: replaceErr}
	}()
	var raceWinner *store.ExamStarterWorkspaceMutationResult
	losers := 0
	for range 2 {
		outcome := <-raceResults
		if outcome.err == nil {
			if raceWinner != nil {
				t.Fatal("both concurrent replacements succeeded")
			}
			raceWinner = outcome.result
			continue
		}
		if !errors.As(outcome.err, &conflict) || conflict.Constraint != "exam_draft_revision" {
			t.Fatalf("concurrent replacement error = %v", outcome.err)
		}
		losers++
	}
	if raceWinner == nil || raceWinner.DraftRevision != 7 || losers != 1 {
		t.Fatalf("concurrent replacement winner=%#v losers=%d", raceWinner, losers)
	}

	removeFile := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 7, fileID, "", at.Add(7*time.Second))
	removed, err := ss.ExamStarterWorkspace().RemoveEntry(ctx, removeFile, examCommand(creator.ID, "exam.starter_workspace.entry.remove.v1", "workspace-remove-file", "workspace-remove-file-command"))
	requireNoError(t, err)
	if removed.DraftRevision != 8 || !removed.Entry.ArchivedAt.Valid || removed.ReclaimableObject != raceWinner.Object.ID {
		t.Fatalf("file removal = %#v", removed)
	}
	for index, entryID := range []model.StarterWorkspaceEntryID{subdirectoryID, directoryID} {
		revision := int64(8 + index)
		remove := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, revision, entryID, "", at.Add(time.Duration(revision+1)*time.Second))
		_, err = ss.ExamStarterWorkspace().RemoveEntry(ctx, remove, examCommand(creator.ID, "exam.starter_workspace.entry.remove.v1", "workspace-remove-"+entryID.String(), "workspace-remove-command-"+entryID.String()))
		requireNoError(t, err)
	}
	items, err = ss.ExamStarterWorkspace().List(ctx, examID)
	requireNoError(t, err)
	if len(items) != 0 {
		t.Fatalf("removed hierarchy = %#v", items)
	}

	// An upload that never finalized remains invisible, becomes cleanup-eligible
	// only after its lease plus safety window, and is recoverably claim-fenced.
	oldAt := model.NowUTC().Add(-26 * time.Hour)
	abandoned, err := model.NewStagedStarterWorkspaceObject(model.NewStarterWorkspaceObjectID(), examID, creator.ID, oldAt, oldAt.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.ExamStarterWorkspace().ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: abandoned})
	requireNoError(t, err)
	claimed, err := ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-claim")
	requireNoError(t, err)
	if len(claimed) != 1 || claimed[0].ID != abandoned.ID || claimed[0].State != model.StarterWorkspaceObjectClaimed {
		t.Fatalf("cleanup claim = %#v", claimed)
	}
	requireNoError(t, ss.ExamStarterWorkspace().ReleaseObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-claim"))
	claimed, err = ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-retry")
	requireNoError(t, err)
	if len(claimed) != 1 || claimed[0].ID != abandoned.ID {
		t.Fatalf("released cleanup retry = %#v", claimed)
	}
	requireNoError(t, ss.ExamStarterWorkspace().CompleteObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-retry"))
	// Completion may commit even when the caller observes an unknown transport
	// outcome. Repeating the same exact cleanup must converge successfully.
	requireNoError(t, ss.ExamStarterWorkspace().CompleteObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-retry"))
	claimed, err = ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-empty")
	requireNoError(t, err)
	if len(claimed) != 0 {
		t.Fatalf("completed cleanup remained claimable = %#v", claimed)
	}
}

func starterWorkspaceMutation(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID,
	unitID model.AcademicUnitID, expected int64, entryID model.StarterWorkspaceEntryID, path string, at time.Time) *store.ExamStarterWorkspaceMutation {
	t.Helper()
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, unitID)
	return &store.ExamStarterWorkspaceMutation{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected,
		ChangedAt: model.MillisFromTime(at), AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at), EntryID: entryID, Path: path}
}

func reserveStarterWorkspaceObject(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID,
	unitID model.AcademicUnitID, expected int64, entryID model.StarterWorkspaceEntryID, path string, at time.Time, size int64) *store.ExamStarterWorkspaceMutation {
	t.Helper()
	object, err := model.NewStagedStarterWorkspaceObject(model.NewStarterWorkspaceObjectID(), examID, actorID, at, at.Add(model.StarterWorkspaceUploadLease))
	requireNoError(t, err)
	_, err = ss.ExamStarterWorkspace().ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: object})
	requireNoError(t, err)
	mutation := starterWorkspaceMutation(t, ctx, ss, examID, actorID, unitID, expected, entryID, path, at.Add(time.Millisecond))
	mutation.ObjectID = object.ID
	mutation.ContentVersion = model.NewWorkspaceContentVersion()
	mutation.MediaType = "text/plain"
	mutation.SizeBytes = size
	mutation.SHA256 = strings.Repeat("a", 64)
	return mutation
}
