// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestExamStarterWorkspaceStore verifies the bounded hierarchy, replay, move,
// content-pointer replacement, and nonempty-directory guarantees shared by
// every adapter.
func TestExamStarterWorkspaceStore(t *testing.T, ss store.Store, probes ...StarterWorkspaceSQLProbe) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "starter-workspace-unit")
	creator := saveUser(t, ctx, ss)
	at := model.NowUTC().Truncate(time.Millisecond).Add(123 * time.Microsecond)
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "starter-workspace-exam")
	examID := created.Value.Exam.ID

	directoryID := model.NewStarterWorkspaceEntryID()
	directory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 1, directoryID, "cmd", at.Add(time.Second))
	directoryResult, err := ss.ExamStarterWorkspace().CreateDirectory(ctx, directory, examCommand(creator.ID, "exam.starter_workspace.directory.create", "workspace-dir", "workspace-dir-command"))
	require.NoError(t, err)
	require.Equal(t, int64(2), directoryResult.DraftRevision)
	require.Equal(t, "cmd", directoryResult.Entry.Path)
	subdirectoryID := model.NewStarterWorkspaceEntryID()
	subdirectory := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 2, subdirectoryID, "cmd/app", at.Add(2*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, subdirectory, examCommand(creator.ID, "exam.starter_workspace.directory.create", "workspace-subdir", "workspace-subdir-command"))
	require.NoError(t, err)

	fileID := model.NewStarterWorkspaceEntryID()
	fileMutation := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, fileID, "cmd/app/main.go", at.Add(3*time.Second), 13)
	fileCommand := examCommand(creator.ID, "exam.starter_workspace.file.create", "workspace-file", "workspace-file-command")
	fileResult, err := ss.ExamStarterWorkspace().CreateFile(ctx, fileMutation, fileCommand)
	require.NoError(t, err)
	require.Equal(t, int64(4), fileResult.DraftRevision)
	require.NotNil(t, fileResult.Object)
	require.Equal(t, fileResult.Object.ID, fileResult.Entry.CurrentObjectID)
	require.True(t, fileResult.Object.CreatedAt.Equal(at.Add(3*time.Second)), "file finalization lost the reservation timestamp precision")
	require.True(t, fileResult.Object.UpdatedAt.Equal(fileResult.Object.CreatedAt), "file finalization lost the reservation timestamp precision")
	// The exact retry arrives after the first finalize advanced the Draft. Its
	// fresh opaque reservation must remain possible so the named idempotent
	// mutation can return the already-committed outcome.
	replay := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 3, model.NewStarterWorkspaceEntryID(), "cmd/app/main.go", at.Add(3*time.Second+2*time.Millisecond), 13)
	replayed, err := ss.ExamStarterWorkspace().CreateFile(ctx, replay, fileCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int64(4), replayed.DraftRevision)
	require.Equal(t, fileID, replayed.Entry.ID)

	items, err := ss.ExamStarterWorkspace().List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "cmd/app/main.go", items[2].Entry.Path)

	move := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 4, directoryID, "src", at.Add(4*time.Second))
	moved, err := ss.ExamStarterWorkspace().MoveEntry(ctx, move, examCommand(creator.ID, "exam.starter_workspace.entry.move", "workspace-move", "workspace-move-command"))
	require.NoError(t, err)
	require.Equal(t, int64(5), moved.DraftRevision)
	require.Equal(t, "src", moved.Entry.Path)
	items, err = ss.ExamStarterWorkspace().List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "src", items[0].Entry.Path)
	require.Equal(t, "src/app", items[1].Entry.Path)
	require.Equal(t, "src/app/main.go", items[2].Entry.Path)

	nonempty := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 5, directoryID, "", at.Add(5*time.Second))
	_, err = ss.ExamStarterWorkspace().RemoveEntry(ctx, nonempty, examCommand(creator.ID, "exam.starter_workspace.entry.remove", "workspace-nonempty", "workspace-nonempty-command"))
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "workspace_directory_not_empty", conflict.Constraint)

	oldObjectID := fileResult.Object.ID
	staleReplacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(5*time.Second+time.Millisecond), 5)
	staleReplacement.ExpectedContentVersion = model.NewWorkspaceContentVersion()
	_, err = ss.ExamStarterWorkspace().ReplaceFile(ctx, staleReplacement, examCommand(creator.ID, "exam.starter_workspace.file.replace", "workspace-replace-stale", "workspace-replace-stale-command"))
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "workspace_content_version", conflict.Constraint)
	replacement := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(6*time.Second), 5)
	replacement.ExpectedContentVersion = fileResult.Object.ContentVersion
	replaced, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, replacement, examCommand(creator.ID, "exam.starter_workspace.file.replace", "workspace-replace", "workspace-replace-command"))
	require.NoError(t, err)
	require.Equal(t, int64(6), replaced.DraftRevision)
	require.NotNil(t, replaced.Object)
	require.NotEqual(t, oldObjectID, replaced.Object.ID)
	require.Equal(t, oldObjectID, replaced.ReclaimableObject)
	replacementReplay := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 5, fileID, "", at.Add(6*time.Second+time.Millisecond), 5)
	replacementReplay.ExpectedContentVersion = fileResult.Object.ContentVersion
	replayedReplacement, err := ss.ExamStarterWorkspace().ReplaceFile(ctx, replacementReplay, examCommand(creator.ID, "exam.starter_workspace.file.replace", "workspace-replace", "workspace-replace-command"))
	require.NoError(t, err)
	require.True(t, replayedReplacement.Replayed)
	require.Equal(t, int64(6), replayedReplacement.DraftRevision)
	require.NotNil(t, replayedReplacement.Object)
	require.Equal(t, replaced.Object.ID, replayedReplacement.Object.ID)
	got, err := ss.ExamStarterWorkspace().GetFile(ctx, examID, fileID)
	require.NoError(t, err)
	require.NotNil(t, got.Object)
	require.Equal(t, replaced.Object.ID, got.Object.ID)
	require.Equal(t, "src/app/main.go", got.Entry.Path)

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
		result, replaceErr := ss.ExamStarterWorkspace().ReplaceFile(ctx, raceFirst, examCommand(creator.ID, "exam.starter_workspace.file.replace", "workspace-race-first", "workspace-race-first-command"))
		raceResults <- raceOutcome{result: result, err: replaceErr}
	}()
	go func() {
		result, replaceErr := ss.ExamStarterWorkspace().ReplaceFile(ctx, raceSecond, examCommand(creator.ID, "exam.starter_workspace.file.replace", "workspace-race-second", "workspace-race-second-command"))
		raceResults <- raceOutcome{result: result, err: replaceErr}
	}()
	var raceWinner *store.ExamStarterWorkspaceMutationResult
	losers := 0
	for range 2 {
		outcome := <-raceResults
		if outcome.err == nil {
			require.Nil(t, raceWinner, "both concurrent replacements succeeded")
			raceWinner = outcome.result
			continue
		}
		require.ErrorAs(t, outcome.err, &conflict)
		require.Equal(t, "exam_draft_revision", conflict.Constraint)
		losers++
	}
	require.NotNil(t, raceWinner, "concurrent replacement winner")
	require.Equal(t, int64(7), raceWinner.DraftRevision, "concurrent replacement winner")
	require.Equal(t, 1, losers, "concurrent replacement winner")

	removeFile := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 7, fileID, "", at.Add(7*time.Second))
	removed, err := ss.ExamStarterWorkspace().RemoveEntry(ctx, removeFile, examCommand(creator.ID, "exam.starter_workspace.entry.remove", "workspace-remove-file", "workspace-remove-file-command"))
	require.NoError(t, err)
	require.Equal(t, int64(8), removed.DraftRevision)
	require.True(t, removed.Entry.ArchivedAt.Valid)
	require.Equal(t, raceWinner.Object.ID, removed.ReclaimableObject)
	for index, entryID := range []model.StarterWorkspaceEntryID{subdirectoryID, directoryID} {
		revision := int64(8 + index)
		remove := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, revision, entryID, "", at.Add(time.Duration(revision+1)*time.Second))
		_, err = ss.ExamStarterWorkspace().RemoveEntry(ctx, remove, examCommand(creator.ID, "exam.starter_workspace.entry.remove", "workspace-remove-"+entryID.String(), "workspace-remove-command-"+entryID.String()))
		require.NoError(t, err)
	}
	items, err = ss.ExamStarterWorkspace().List(ctx, examID)
	require.NoError(t, err)
	require.Empty(t, items)

	policy := model.DefaultExamCapacityPolicy()
	policy.WorkspaceMaximumEntries = 1
	policy.WorkspaceMaximumFileBytes = 8
	policy.WorkspaceMaximumTotalBytes = 8
	institution.ExamCapacity = policy
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	oversized := reserveStarterWorkspaceObject(t, ctx, ss, examID, creator.ID, unit.ID, 10,
		model.NewStarterWorkspaceEntryID(), "oversized.txt", at.Add(11*time.Second), 9)
	_, err = ss.ExamStarterWorkspace().CreateFile(ctx, oversized,
		examCommand(creator.ID, "exam.starter_workspace.file.create", "workspace-oversized", "workspace-oversized-command"))
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "workspace_total_size", conflict.Constraint)
	bounded := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 10,
		model.NewStarterWorkspaceEntryID(), "bounded", at.Add(12*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, bounded,
		examCommand(creator.ID, "exam.starter_workspace.directory.create", "workspace-bounded", "workspace-bounded-command"))
	require.NoError(t, err)
	overflow := starterWorkspaceMutation(t, ctx, ss, examID, creator.ID, unit.ID, 11,
		model.NewStarterWorkspaceEntryID(), "overflow", at.Add(13*time.Second))
	_, err = ss.ExamStarterWorkspace().CreateDirectory(ctx, overflow,
		examCommand(creator.ID, "exam.starter_workspace.directory.create", "workspace-overflow", "workspace-overflow-command"))
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "workspace_entry_limit", conflict.Constraint)
	institution.ExamCapacity = model.DefaultExamCapacityPolicy()
	_, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)

	// An upload that never finalized remains invisible, becomes cleanup-eligible
	// only after its lease plus safety window, and is recoverably claim-fenced.
	oldAt := model.NowUTC().Add(-26 * time.Hour)
	abandoned, err := model.NewStagedStarterWorkspaceObject(model.NewStarterWorkspaceObjectID(), examID, creator.ID, oldAt, oldAt.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.ExamStarterWorkspace().ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: abandoned})
	require.NoError(t, err)
	claimed, err := ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-claim")
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, abandoned.ID, claimed[0].ID)
	require.Equal(t, model.StarterWorkspaceObjectClaimed, claimed[0].State)
	require.NoError(t, ss.ExamStarterWorkspace().ReleaseObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-claim"))
	claimed, err = ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-retry")
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, abandoned.ID, claimed[0].ID)
	require.NoError(t, ss.ExamStarterWorkspace().CompleteObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-retry"))
	// Completion may commit even when the caller observes an unknown transport
	// outcome. Repeating the same exact cleanup must converge successfully.
	require.NoError(t, ss.ExamStarterWorkspace().CompleteObjectCleanup(ctx, abandoned.ID, "workspace-cleanup-retry"))
	claimed, err = ss.ExamStarterWorkspace().ClaimObjectsForCleanup(ctx, 10, "workspace-cleanup-empty")
	require.NoError(t, err)
	require.Empty(t, claimed)
	testStarterWorkspaceRecursiveRemoval(t, ss, probes...)
}

func starterWorkspaceMutation(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID,
	unitID model.AcademicUnitID, expected int64, entryID model.StarterWorkspaceEntryID, path string, at time.Time) *store.ExamStarterWorkspaceMutation {
	t.Helper()
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, unitID)
	return &store.ExamStarterWorkspaceMutation{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected,
		ChangedAt: at, AuditEventID: audit.ID, AuditAt: model.MillisFromTime(at), EntryID: entryID, Path: path}
}

func reserveStarterWorkspaceObject(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID,
	unitID model.AcademicUnitID, expected int64, entryID model.StarterWorkspaceEntryID, path string, at time.Time, size int64) *store.ExamStarterWorkspaceMutation {
	t.Helper()
	object, err := model.NewStagedStarterWorkspaceObject(model.NewStarterWorkspaceObjectID(), examID, actorID, at, at.Add(model.StarterWorkspaceUploadLease))
	require.NoError(t, err)
	_, err = ss.ExamStarterWorkspace().ReserveObject(ctx, &store.ExamStarterWorkspaceReservation{Object: object})
	require.NoError(t, err)
	mutation := starterWorkspaceMutation(t, ctx, ss, examID, actorID, unitID, expected, entryID, path, at)
	mutation.ObjectID = object.ID
	mutation.ContentVersion = model.NewWorkspaceContentVersion()
	mutation.MediaType = "text/plain"
	mutation.SizeBytes = size
	mutation.SHA256 = strings.Repeat("a", 64)
	return mutation
}
