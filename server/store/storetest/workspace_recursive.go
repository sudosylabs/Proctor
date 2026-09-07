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
		value := examCommand(user.ID, "exam.starter_workspace."+operation+".v1", label, label)
		value.Retention = time.Millisecond
		return value
	}
	directory := func(path string) model.StarterWorkspaceEntryID {
		id := model.NewStarterWorkspaceEntryID()
		result, err := workspace.CreateDirectory(ctx, mutate(id, path), command("directory.create", path))
		requireNoError(t, err)
		revision = result.DraftRevision
		return id
	}
	file := func(path string) *store.ExamStarterWorkspaceMutationResult {
		input := reserveStarterWorkspaceObject(t, ctx, ss, examID, user.ID, unit.ID, revision,
			model.NewStarterWorkspaceEntryID(), path, model.NowUTC(), 1)
		result, err := workspace.CreateFile(ctx, input, command("file.create", path))
		requireNoError(t, err)
		revision = result.DraftRevision
		return result
	}
	root := directory("src%_!")
	directory("src%_!/nested")
	directory("srcXYZ")
	directory("src%_!-sibling")
	pinned := file("src%_!/nested/published.txt")
	publication, err := ss.ExamRevision().Publish(ctx, examRevisionPublication(t, ctx, ss, examID, user.ID, unit.ID, revision, model.NowUTC()),
		examCommand(user.ID, "exam.revision.publish.v1", "recursive-publish", "recursive-publish"))
	requireNoError(t, err)
	revision = publication.DraftRevision
	stale := mutate(root, "")
	stale.Recursive = true
	unpinned := file("src%_!/nested/draft.txt")
	_, err = workspace.RemoveEntry(ctx, stale, command("entry.remove", "stale-recursive"))
	assertExamAttemptConflict(t, err, "exam_draft_revision")
	items, err := workspace.List(ctx, examID)
	requireNoError(t, err)
	if len(items) != 6 {
		t.Fatalf("stale recursive removal changed hierarchy: %#v", items)
	}
	invalidFile := mutate(unpinned.Entry.ID, "")
	invalidFile.Recursive = true
	if _, err = workspace.RemoveEntry(ctx, invalidFile, command("entry.remove", "recursive-file")); err == nil {
		t.Fatal("recursive file removal succeeded")
	}
	// Remove earlier creation outcomes before testing the subtree's own outcome
	// protection. Expired-but-retained records must still protect bytes.
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	requireNoError(t, err)
	input := mutate(root, "")
	input.Recursive = true
	deleteCommand := command("entry.remove", "recursive-delete")
	removed, err := workspace.RemoveEntry(ctx, input, deleteCommand)
	requireNoError(t, err)
	if removed.DraftRevision != revision+1 || removed.Entry.ID != root || !removed.Entry.ArchivedAt.Valid {
		t.Fatalf("recursive removal=%#v", removed)
	}
	replay := mutate(root, "")
	replay.Recursive = true
	replayed, err := workspace.RemoveEntry(ctx, replay, deleteCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.DraftRevision != removed.DraftRevision {
		t.Fatalf("recursive replay=%#v", replayed)
	}
	items, err = workspace.List(ctx, examID)
	requireNoError(t, err)
	if len(items) != 2 || items[0].Entry.Path != "src%_!-sibling" || items[1].Entry.Path != "srcXYZ" {
		t.Fatalf("recursive removal affected sibling prefixes: %#v", items)
	}
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, examID, publication.Revision.ID)
	requireNoError(t, err)
	if len(snapshot.StarterWorkspace) != 5 {
		t.Fatalf("published hierarchy changed: %#v", snapshot.StarterWorkspace)
	}
	if len(probes) == 0 || probes[0].MakeObjectCleanupDue == nil {
		return
	}
	for _, object := range []*model.StarterWorkspaceObject{pinned.Object, unpinned.Object} {
		probes[0].MakeObjectCleanupDue(t, ctx, object.ID)
	}
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, 100, "recursive-starter-retained")
	requireNoError(t, err)
	if len(claimed) != 0 {
		t.Fatalf("retained recursive outcome lost object protection: %#v", claimed)
	}
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	requireNoError(t, err)
	claimed, err = workspace.ClaimObjectsForCleanup(ctx, 100, "recursive-starter-reclaim")
	requireNoError(t, err)
	if len(claimed) != 1 || claimed[0].ID != unpinned.Object.ID {
		t.Fatalf("recursive cleanup did not preserve published pin: %#v", claimed)
	}
	requireNoError(t, workspace.CompleteObjectCleanup(ctx, unpinned.Object.ID, "recursive-starter-reclaim"))
	requireNoError(t, workspace.CompleteObjectCleanup(ctx, unpinned.Object.ID, "recursive-starter-reclaim"))
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
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(f.candidate.ID, store.ExamAttemptConnectOperation, "recursive-connect", "recursive-connect"))
	requireNoError(t, err)
	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, CandidateUserID: f.candidate.ID, SessionID: f.session.ID,
		DesktopRegistrationID: f.session.DesktopRegistrationID, DPoPKeyThumbprint: f.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credential}
	prepare := func(kind model.AttemptWorkspaceMutationKind, label string) (*store.ExamAttemptWorkspaceMutation, *store.CommandIdempotency) {
		input := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: kind, EntryID: model.NewAttemptWorkspaceEntryID(),
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, f).ID.String(), AuditAt: model.GetMillis()}
		command := examCommand(f.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, label, label)
		command.Retention = time.Millisecond
		return input, command
	}
	manifest := func() *store.CandidateAttemptWorkspacePage {
		page, listErr := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: -1, Limit: 200})
		requireNoError(t, listErr)
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
	requireNoError(t, err)
	ready := func() *model.AttemptWorkspaceObject {
		object, readyErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
		requireNoError(t, readyErr)
		object, readyErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: object.ID,
			ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: 1, SHA256: strings.Repeat("a", 64)}})
		requireNoError(t, readyErr)
		return object
	}
	object := ready()
	createFile, fileCommand := prepare(model.AttemptWorkspaceMutationCreateFile, "recursive-owned")
	createFile.DestinationPath, createFile.ObjectID = "cmd/nested/owned.txt", object.ID
	_, err = workspace.ApplyMutation(ctx, createFile, fileCommand)
	requireNoError(t, err)
	staleCursor := manifest().Cursor
	sibling, siblingCommand := prepare(model.AttemptWorkspaceMutationCreateDirectory, "recursive-sibling")
	sibling.DestinationPath = "cmd-sibling"
	_, err = workspace.ApplyMutation(ctx, sibling, siblingCommand)
	requireNoError(t, err)
	staleDelete, staleCommand := prepare(model.AttemptWorkspaceMutationDeleteEntry, "recursive-stale")
	staleDelete.EntryID, staleDelete.ExpectedPath, staleDelete.Recursive, staleDelete.ExpectedWorkspaceCursor = root, "cmd", true, &staleCursor
	_, err = workspace.ApplyMutation(ctx, staleDelete, staleCommand)
	assertExamAttemptConflict(t, err, "attempt_workspace_cursor")
	if len(manifest().Items) != 5 {
		t.Fatal("stale subtree deletion changed hierarchy")
	}
	// Expire the creation outcome so cleanup must rely on the recursive
	// deletion's bounded retirement group, rather than a file-ID list.
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	requireNoError(t, err)
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
	if first.err != nil || !store.IsConflict(second.err) {
		t.Fatalf("subtree/edit race errors=%v, %v", first.err, second.err)
	}
	removed := first.value
	if !first.deletion {
		object = replacementObject
		time.Sleep(2 * time.Millisecond)
		_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
		requireNoError(t, err)
		cursor = manifest().Cursor
		deletion, deleteCommand = prepare(model.AttemptWorkspaceMutationDeleteEntry, "recursive-delete-after-race")
		deletion.EntryID, deletion.ExpectedPath, deletion.Recursive, deletion.ExpectedWorkspaceCursor = root, "cmd", true, &cursor
		removed, err = workspace.ApplyMutation(ctx, deletion, deleteCommand)
		requireNoError(t, err)
	}
	if removed.Entry != nil || !removed.Change.Recursive || removed.Change.OldPath != "cmd" || removed.Change.Cursor != cursor+1 {
		t.Fatalf("recursive acknowledgement=%#v", removed)
	}
	replay := *deletion
	replay.AuditEventID = saveExamAttemptAudit(t, ctx, ss, f).ID.String()
	replayed, err := workspace.ApplyMutation(ctx, &replay, deleteCommand)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Change != removed.Change {
		t.Fatalf("recursive replay=%#v", replayed)
	}
	page := manifest()
	if len(page.Items) != 1 || page.Items[0].EntryID != sibling.EntryID {
		t.Fatalf("recursive manifest=%#v", page)
	}
	journal, err := workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: cursor, Limit: 200})
	requireNoError(t, err)
	if len(journal.Entries) != 1 || !journal.Entries[0].Recursive || journal.Entries[0] != removed.Change {
		t.Fatalf("recursive journal=%#v", journal)
	}
	snapshot, err := ss.ExamRevision().GetSnapshot(ctx, f.examID, f.revisionID)
	requireNoError(t, err)
	if len(snapshot.StarterWorkspace) != 2 {
		t.Fatal("recursive deletion changed the published source")
	}
	if len(probes) == 0 || probes[0].MakeObjectCleanupDue == nil {
		return
	}
	probes[0].MakeObjectCleanupDue(t, ctx, object.ID)
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, 200, "recursive-attempt-retained")
	requireNoError(t, err)
	if len(claimed) != 0 {
		t.Fatalf("retained recursive outcome lost protection: %#v", claimed)
	}
	time.Sleep(2 * time.Millisecond)
	_, err = ss.CommandOutcome().DeleteExpired(ctx, 500)
	requireNoError(t, err)
	claimed, err = workspace.ClaimObjectsForCleanup(ctx, 200, "recursive-attempt-reclaim")
	requireNoError(t, err)
	if len(claimed) != 1 || claimed[0].ID != object.ID {
		t.Fatalf("recursive cleanup=%#v", claimed)
	}
	requireNoError(t, workspace.CompleteObjectCleanup(ctx, object.ID, "recursive-attempt-reclaim"))
	requireNoError(t, workspace.CompleteObjectCleanup(ctx, object.ID, "recursive-attempt-reclaim"))
}
