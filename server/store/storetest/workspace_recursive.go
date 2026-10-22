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

// StarterWorkspaceSQLProbe changes only cleanup timing that public commands
// deliberately cannot control.
type StarterWorkspaceSQLProbe struct {
	MakeObjectCleanupDue func(*testing.T, context.Context, model.StarterWorkspaceObjectID)
}

func testStarterWorkspaceRecursiveRemoval(t *testing.T, ss store.Store, probes ...StarterWorkspaceSQLProbe) {
	t.Helper()
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "recursive-starter")
	user := saveUser(t, ctx, ss)
	created := createCatalogExam(t, ctx, ss, unit.ID, user.ID, model.NowUTC(), "recursive-starter")
	examID, revision := created.Value.Exam.ID, int64(1)
	workspace := ss.ExamStarterWorkspace()
	mutate := func(id model.StarterWorkspaceEntryID, path string) *store.ExamStarterWorkspaceMutation {
		return starterWorkspaceMutation(t, ctx, ss, examID, user.ID, unit.ID, revision, id, path, model.NowUTC())
	}
	command := func(operation, label string) *store.CommandIdempotency {
		value := examCommand(user.ID, "exam.starter_workspace."+operation, label, label)
		value.Retention = time.Millisecond
		return value
	}
	directory := func(path string) model.StarterWorkspaceEntryID {
		id := model.NewStarterWorkspaceEntryID()
		result, err := workspace.CreateDirectory(ctx, mutate(id, path), command("directory.create", path))
		require.NoError(t, err)
		revision = result.DraftRevision
		return id
	}
	file := func(path string) *store.ExamStarterWorkspaceMutationResult {
		input := reserveStarterWorkspaceObject(t, ctx, ss, examID, user.ID, unit.ID, revision,
			model.NewStarterWorkspaceEntryID(), path, model.NowUTC(), 1)
		result, err := workspace.CreateFile(ctx, input, command("file.create", path))
		require.NoError(t, err)
		revision = result.DraftRevision
		return result
	}
	root := directory("src%_!")
	directory("src%_!/nested")
	directory("srcXYZ")
	directory("src%_!-sibling")
	pinned := file("src%_!/nested/published.txt")
	publication, err := ss.ExamRevision().Publish(ctx, examRevisionPublication(t, ctx, ss, examID, user.ID, unit.ID, revision, model.NowUTC()),
		examCommand(user.ID, "exam.revision.publish", "recursive-publish", "recursive-publish"))
	require.NoError(t, err)
	revision = publication.DraftRevision
	stale := mutate(root, "")
	stale.Recursive = true
	unpinned := file("src%_!/nested/draft.txt")
	_, err = workspace.RemoveEntry(ctx, stale, command("entry.remove", "stale-recursive"))
	requireConflictConstraint(t, err, "exam_draft_revision")
	items, err := workspace.List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, items, 6)
	invalidFile := mutate(unpinned.Entry.ID, "")
	invalidFile.Recursive = true
	_, err = workspace.RemoveEntry(ctx, invalidFile, command("entry.remove", "recursive-file"))
	require.Error(t, err, "recursive file removal succeeded")
	// Remove earlier creation outcomes before testing the subtree's own outcome
	// protection. Expired-but-retained records must still protect bytes.
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	require.NoError(t, err)
	input := mutate(root, "")
	input.Recursive = true
	deleteCommand := command("entry.remove", "recursive-delete")
	removed, err := workspace.RemoveEntry(ctx, input, deleteCommand)
	require.NoError(t, err)
	require.Equal(t, revision+1, removed.DraftRevision)
	require.Equal(t, root, removed.Entry.ID)
	require.True(t, removed.Entry.ArchivedAt.Valid)
	replay := mutate(root, "")
	replay.Recursive = true
	replayed, err := workspace.RemoveEntry(ctx, replay, deleteCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, removed.DraftRevision, replayed.DraftRevision)
	items, err = workspace.List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "src%_!-sibling", items[0].Entry.Path)
	require.Equal(t, "srcXYZ", items[1].Entry.Path)
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, publication.Revision.ID)
	require.NoError(t, err)
	require.Len(t, snapshot.StarterWorkspace, 5)
	if len(probes) == 0 || probes[0].MakeObjectCleanupDue == nil {
		return
	}
	for _, object := range []*model.StarterWorkspaceObject{pinned.Object, unpinned.Object} {
		probes[0].MakeObjectCleanupDue(t, ctx, object.ID)
	}
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, &store.StarterWorkspaceCleanupClaim{Limit: 100, ClaimToken: "recursive-starter-retained"})
	require.NoError(t, err)
	require.Empty(t, claimed)
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	require.NoError(t, err)
	claimed, err = workspace.ClaimObjectsForCleanup(ctx, &store.StarterWorkspaceCleanupClaim{Limit: 100, ClaimToken: "recursive-starter-reclaim"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, unpinned.Object.ID, claimed[0].ID)
	require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.StarterWorkspaceCleanupCompletion{ObjectID: unpinned.Object.ID, ClaimToken: "recursive-starter-reclaim"}))
	require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.StarterWorkspaceCleanupCompletion{ObjectID: unpinned.Object.ID, ClaimToken: "recursive-starter-reclaim"}))
}

func testAttemptWorkspaceRecursiveDeletion(t *testing.T, ss store.Store, workspace store.ExamAttemptWorkspaceStore, probes ...ExamAttemptWorkspaceSQLProbe) {
	t.Helper()
	ctx := context.Background()
	f := newExamAttemptFixture(t, ctx, ss)
	credential := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: f.sitting.ID, CandidateUserID: f.candidate.ID,
		SessionID: f.session.ID, DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(),
		ConnectionID: model.NewAttemptConnectionID(), ContinuityCredentialHash: credential,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(f.candidate.ID, store.ExamAttemptConnectOperation, "recursive-connect", "recursive-connect"))
	require.NoError(t, err)
	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, CandidateUserID: f.candidate.ID, SessionID: f.session.ID,
		DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credential}
	prepare := func(kind model.AttemptWorkspaceMutationKind, label string) (*store.ExamAttemptWorkspaceMutation, *store.CommandIdempotency) {
		input := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: kind, EntryID: model.NewAttemptWorkspaceEntryID(),
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID, AuditAt: model.NowUTC()}
		command := examCommand(f.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, label, label)
		command.Retention = time.Millisecond
		return input, command
	}
	manifest := func() *store.CandidateAttemptWorkspacePage {
		page, listErr := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: -1, Limit: 200})
		require.NoError(t, listErr)
		return page
	}
	var root model.AttemptWorkspaceEntryID
	for _, item := range manifest().Items {
		if item.Path == "cmd" {
			root = item.EntryID
		}
	}
	createDir, dirCommand := prepare(model.AttemptWorkspaceMutationCreateDirectory, "recursive-nested")
	createDir.DestinationPath = "cmd/nested"
	_, err = workspace.ApplyMutation(ctx, createDir, dirCommand)
	require.NoError(t, err)
	ready := func() *model.AttemptWorkspaceObject {
		object, readyErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
		require.NoError(t, readyErr)
		object, readyErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: object.ID,
			ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 1, SHA256: strings.Repeat("a", 64)}})
		require.NoError(t, readyErr)
		return object
	}
	object := ready()
	createFile, fileCommand := prepare(model.AttemptWorkspaceMutationCreateFile, "recursive-owned")
	createFile.DestinationPath, createFile.ObjectID = "cmd/nested/owned.txt", object.ID
	_, err = workspace.ApplyMutation(ctx, createFile, fileCommand)
	require.NoError(t, err)
	staleCursor := manifest().Cursor
	sibling, siblingCommand := prepare(model.AttemptWorkspaceMutationCreateDirectory, "recursive-sibling")
	sibling.DestinationPath = "cmd-sibling"
	_, err = workspace.ApplyMutation(ctx, sibling, siblingCommand)
	require.NoError(t, err)
	staleDelete, staleCommand := prepare(model.AttemptWorkspaceMutationDeleteEntry, "recursive-stale")
	staleDelete.EntryID, staleDelete.ExpectedPath, staleDelete.Recursive, staleDelete.ExpectedWorkspaceCursor = root, "cmd", true, &staleCursor
	_, err = workspace.ApplyMutation(ctx, staleDelete, staleCommand)
	requireConflictConstraint(t, err, "attempt_workspace_cursor")
	require.Len(t, manifest().Items, 5, "stale subtree deletion changed hierarchy")
	// Expire the creation outcome so cleanup must rely on the recursive
	// deletion's bounded retirement group, rather than a file-ID list.
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	require.NoError(t, err)
	cursor := manifest().Cursor
	deletion, deleteCommand := prepare(model.AttemptWorkspaceMutationDeleteEntry, "recursive-delete-race")
	deletion.EntryID, deletion.ExpectedPath, deletion.Recursive, deletion.ExpectedWorkspaceCursor = root, "cmd", true, &cursor
	replacementObject := ready()
	replacement, replacementCommand := prepare(model.AttemptWorkspaceMutationReplaceFile, "recursive-replace-race")
	replacement.EntryID, replacement.ExpectedPath, replacement.ExpectedContentVersion, replacement.ObjectID = createFile.EntryID, createFile.DestinationPath, object.ContentVersion, replacementObject.ID
	peer := workspace
	if len(probes) > 0 && probes[0].ConcurrentPeer != nil {
		peer = probes[0].ConcurrentPeer
	}
	type raceResult struct {
		deletion bool
		value    *store.ExamAttemptWorkspaceMutationResult
		err      error
	}
	results, start := make(chan raceResult, 2), make(chan struct{})
	go func() {
		<-start
		value, applyErr := workspace.ApplyMutation(ctx, deletion, deleteCommand)
		results <- raceResult{true, value, applyErr}
	}()
	go func() {
		<-start
		value, applyErr := peer.ApplyMutation(ctx, replacement, replacementCommand)
		results <- raceResult{false, value, applyErr}
	}()
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	require.NoError(t, first.err, "subtree/edit race errors")
	require.True(t, store.IsConflict(second.err), "subtree/edit race errors")
	removed := first.value
	if !first.deletion {
		object = replacementObject
		time.Sleep(2 * time.Millisecond)
		_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
		require.NoError(t, err)
		cursor = manifest().Cursor
		deletion, deleteCommand = prepare(model.AttemptWorkspaceMutationDeleteEntry, "recursive-delete-after-race")
		deletion.EntryID, deletion.ExpectedPath, deletion.Recursive, deletion.ExpectedWorkspaceCursor = root, "cmd", true, &cursor
		removed, err = workspace.ApplyMutation(ctx, deletion, deleteCommand)
		require.NoError(t, err)
	}
	require.Nil(t, removed.Entry)
	require.True(t, removed.Change.Recursive)
	require.Equal(t, "cmd", removed.Change.OldPath)
	require.Equal(t, cursor+1, removed.Change.Cursor)
	replay := *deletion
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID
	replayed, err := workspace.ApplyMutation(ctx, &replay, deleteCommand)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, removed.Change, replayed.Change)
	page := manifest()
	require.Len(t, page.Items, 1)
	require.Equal(t, sibling.EntryID, page.Items[0].EntryID)
	journal, err := workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: cursor, Limit: 200})
	require.NoError(t, err)
	require.Len(t, journal.Entries, 1)
	require.True(t, journal.Entries[0].Recursive)
	require.Equal(t, removed.Change, journal.Entries[0])
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, f.examID, f.revisionID)
	require.NoError(t, err)
	require.Len(t, snapshot.StarterWorkspace, 2, "recursive deletion changed the published source")
	if len(probes) == 0 || probes[0].MakeObjectCleanupDue == nil {
		return
	}
	probes[0].MakeObjectCleanupDue(t, ctx, object.ID)
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 200, ClaimToken: "recursive-attempt-retained"}) //nolint:gosec // a cleanup claim token, not a credential
	require.NoError(t, err)
	require.Empty(t, claimed)
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	require.NoError(t, err)
	claimed, err = workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 200, ClaimToken: "recursive-attempt-reclaim"}) //nolint:gosec // a cleanup claim token, not a credential
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, object.ID, claimed[0].ID)
	require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.AttemptWorkspaceCleanupCompletion{ObjectID: object.ID, ClaimToken: "recursive-attempt-reclaim"})) //nolint:gosec // a cleanup claim token, not a credential
	require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.AttemptWorkspaceCleanupCompletion{ObjectID: object.ID, ClaimToken: "recursive-attempt-reclaim"})) //nolint:gosec // a cleanup claim token, not a credential
}
