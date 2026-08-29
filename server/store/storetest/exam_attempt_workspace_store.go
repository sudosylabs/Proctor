// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExamAttemptWorkspaceSQLProbe exposes only fixture setup which public Store
// operations deliberately cannot perform, plus an independently constructed
// adapter for multi-node concurrency characterization.
type ExamAttemptWorkspaceSQLProbe struct {
	MakeObjectCleanupDue         func(*testing.T, context.Context, model.AttemptWorkspaceObjectID)
	MakeCleanupClaimStale        func(*testing.T, context.Context, model.AttemptWorkspaceObjectID)
	FillWorkspaceEntryQuota      func(*testing.T, context.Context, model.ExamAttemptWorkspaceID)
	MakeJournalGap               func(*testing.T, context.Context, model.ExamAttemptWorkspaceID)
	SetParticipationLeaseExpired func(*testing.T, context.Context, model.AttemptParticipationID)
	ConcurrentPeer               store.ExamAttemptWorkspaceStore
}

// TestExamAttemptWorkspaceStore verifies the public acknowledged Workspace
// contract against a concrete adapter and, when supplied, an independent peer.
func TestExamAttemptWorkspaceStore(t *testing.T, ss store.Store, workspace store.ExamAttemptWorkspaceStore, probes ...ExamAttemptWorkspaceSQLProbe) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	// Admission and later Workspace growth use the policy frozen into the
	// published Revision, not a subsequently lowered Institution policy.
	institution, err := ss.Institution().GetSingleton(ctx)
	requireNoError(t, err)
	lowered := model.DefaultExamCapacityPolicy()
	lowered.WorkspaceMaximumEntries = 1
	lowered.WorkspaceMaximumFileBytes = 1
	lowered.WorkspaceMaximumTotalBytes = 1
	institution.ExamCapacity = lowered
	_, err = ss.Institution().Update(ctx, institution)
	requireNoError(t, err)
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-connect", "workspace-connect"))
	requireNoError(t, err)

	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	reservation := &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()}
	reserved, err := workspace.ReserveObject(ctx, reservation)
	requireNoError(t, err)
	if reserved == nil || reserved.ID != reservation.ObjectID || reserved.WorkspaceID != connected.Workspace.ID ||
		reserved.State != model.AttemptWorkspaceObjectStaged || reserved.StorageOrigin != model.AttemptWorkspaceStorageAttempt ||
		reserved.CreatedAt.IsZero() || reserved.UpdatedAt != reserved.CreatedAt ||
		reserved.ExpiresAt.Sub(reserved.CreatedAt) != model.AttemptWorkspaceStageLifetime || reserved.HasContent() {
		t.Fatalf("ReserveObject() = %#v", reserved)
	}
	replayed, err := workspace.ReserveObject(ctx, reservation)
	requireNoError(t, err)
	if replayed == nil || replayed.ID != reserved.ID || !replayed.CreatedAt.Equal(reserved.CreatedAt) ||
		!replayed.ExpiresAt.Equal(reserved.ExpiresAt) {
		t.Fatalf("ReserveObject(repeat) = %#v, first = %#v", replayed, reserved)
	}
	target, err := workspace.ResolveMutationTarget(ctx, access)
	requireNoError(t, err)
	if target == nil || target.ExamID != fixture.examID || target.SittingID != fixture.sitting.ID ||
		target.ClassID != fixture.class.ID || target.CandidateUserID != fixture.candidate.ID ||
		target.WorkspaceID != connected.Workspace.ID {
		t.Fatalf("ResolveMutationTarget() = %#v", target)
	}
	readyInput := &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: reserved.ID,
		ContentVersion: model.NewWorkspaceContentVersion(),
		Content:        model.AttemptWorkspaceContent{MediaType: "text/x-go", SizeBytes: 12, SHA256: strings.Repeat("a", 64)}}
	ready, err := workspace.MarkObjectReady(ctx, readyInput)
	requireNoError(t, err)
	if ready == nil || ready.ID != reserved.ID || ready.State != model.AttemptWorkspaceObjectStaged ||
		ready.ContentVersion != readyInput.ContentVersion || ready.MediaType != readyInput.Content.MediaType ||
		ready.SizeBytes != readyInput.Content.SizeBytes || ready.SHA256 != readyInput.Content.SHA256 {
		t.Fatalf("MarkObjectReady() = %#v", ready)
	}
	readyReplay, err := workspace.MarkObjectReady(ctx, readyInput)
	requireNoError(t, err)
	if readyReplay == nil || readyReplay.ID != ready.ID || !readyReplay.UpdatedAt.Equal(ready.UpdatedAt) {
		t.Fatalf("MarkObjectReady(repeat) = %#v, first = %#v", readyReplay, ready)
	}
	changedReady := *readyInput
	changedReady.Content.SHA256 = strings.Repeat("b", 64)
	if _, err = workspace.MarkObjectReady(ctx, &changedReady); !store.IsConflict(err) {
		t.Fatalf("MarkObjectReady(changed repeat) error = %v", err)
	}
	entryID := model.NewAttemptWorkspaceEntryID()
	mutation := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateFile,
		EntryID: entryID, DestinationPath: "notes.go", ObjectID: ready.ID,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	command := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation,
		"workspace-create-file", "workspace-create-file")
	created, err := workspace.ApplyMutation(ctx, mutation, command)
	requireNoError(t, err)
	if created == nil || created.Replayed || created.SittingID != fixture.sitting.ID || created.ClassID != fixture.class.ID ||
		created.CandidateUserID != fixture.candidate.ID || created.WorkspaceID != connected.Workspace.ID || created.Entry == nil ||
		created.Entry.EntryID != entryID || created.Entry.Path != mutation.DestinationPath ||
		created.Entry.ContentVersion != ready.ContentVersion || created.Change.Cursor != 1 ||
		created.Change.Operation != model.AttemptWorkspaceMutationCreateFile || created.Change.NewPath != mutation.DestinationPath {
		t.Fatalf("ApplyMutation(create file) = %#v", created)
	}
	replayMutation := *mutation
	replayMutation.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replayedMutation, err := workspace.ApplyMutation(ctx, &replayMutation, command)
	requireNoError(t, err)
	if replayedMutation == nil || !replayedMutation.Replayed || replayedMutation.Change.Cursor != created.Change.Cursor ||
		replayedMutation.Entry == nil || replayedMutation.Entry.EntryID != created.Entry.EntryID {
		t.Fatalf("ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	}
	requireSuccessfulAudit(t, ctx, ss, mutation.AuditEventID)
	requireSuccessfulAudit(t, ctx, ss, replayMutation.AuditEventID)
	closed, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{
		ConnectionID: access.ConnectionID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		Reason: model.AttemptConnectionCloseTransport, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if closed == nil || !closed.Changed {
		t.Fatalf("CloseConnection(workspace replay fence) = %#v", closed)
	}
	if _, err = workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: -1, Limit: 200}); !store.IsNotFound(err) {
		t.Fatalf("List(after Connection close) error = %v", err)
	}
	if _, err = workspace.ResolveFile(ctx, journalAccess(access), entryID); !store.IsNotFound(err) {
		t.Fatalf("ResolveFile(after Connection close) error = %v", err)
	}
	if _, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), Limit: 200}); !store.IsNotFound(err) {
		t.Fatalf("ListJournal(after Connection close) error = %v", err)
	}
	reconnect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, reconnect)
	reconnected, err := ss.ExamAttempt().Connect(ctx, reconnect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-reconnect", "workspace-reconnect"))
	requireNoError(t, err)
	if reconnected == nil || reconnected.Attempt.ID != connected.Attempt.ID || reconnected.Workspace.ID != connected.Workspace.ID ||
		reconnected.Participation.ID != connected.Participation.ID || reconnected.Participation.Generation != connected.Participation.Generation ||
		reconnected.Connection.ID != reconnect.ConnectionID {
		t.Fatalf("Connect(workspace reconnect) = %#v", reconnected)
	}
	access.ConnectionID = reconnected.Connection.ID
	replayAfterReconnect := *mutation
	replayAfterReconnect.Access = access
	replayAfterReconnect.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	replayedAfterReconnect, err := workspace.ApplyMutation(ctx, &replayAfterReconnect, command)
	requireNoError(t, err)
	if replayedAfterReconnect == nil || !replayedAfterReconnect.Replayed || replayedAfterReconnect.Change.Cursor != created.Change.Cursor {
		t.Fatalf("ApplyMutation(replay after reconnect) = %#v", replayedAfterReconnect)
	}
	requireSuccessfulAudit(t, ctx, ss, replayAfterReconnect.AuditEventID)
	journal, err := workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
		Access: store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID,
			SessionID: access.SessionID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
			DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash},
		AfterCursor: 0, Limit: model.AttemptWorkspaceJournalReadMaximum})
	requireNoError(t, err)
	if journal == nil || journal.WorkspaceID != connected.Workspace.ID || journal.CurrentCursor != 1 || journal.HasMore ||
		journal.RefreshRequired || len(journal.Entries) != 1 || journal.Entries[0].Cursor != 1 ||
		journal.Entries[0].EntryID != entryID || journal.Entries[0].NewPath != mutation.DestinationPath {
		t.Fatalf("ListJournal() = %#v", journal)
	}
	manifest, err := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: store.CandidateAttemptAccess{
		AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash}, ExpectedCursor: -1, Limit: 200})
	requireNoError(t, err)
	if manifest == nil || manifest.WorkspaceID != connected.Workspace.ID || manifest.Cursor != 1 || manifest.RefreshRequired ||
		len(manifest.Items) != 3 {
		t.Fatalf("List(first manifest page) = %#v", manifest)
	}
	refresh, err := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: 0,
		AfterEntryID: manifest.Items[0].EntryID, Limit: 200})
	requireNoError(t, err)
	if refresh == nil || !refresh.RefreshRequired || len(refresh.Items) != 0 || refresh.Cursor != 1 {
		t.Fatalf("List(stale manifest cursor) = %#v", refresh)
	}

	const concurrentCreates = 12
	mutations := make([]*store.ExamAttemptWorkspaceMutation, concurrentCreates)
	commands := make([]*store.CommandIdempotency, concurrentCreates)
	for i := range mutations {
		mutations[i] = &store.ExamAttemptWorkspaceMutation{Access: access,
			Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(),
			DestinationPath: "race-" + string(rune('a'+i)), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(),
			AuditAt: model.GetMillis()}
		commands[i] = examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation,
			"workspace-race-"+string(rune('a'+i)), "workspace-race-"+string(rune('a'+i)))
	}
	writeDone := make(chan error, 1)
	go func() {
		for i := range mutations {
			if _, writeErr := workspace.ApplyMutation(ctx, mutations[i], commands[i]); writeErr != nil {
				writeDone <- writeErr
				return
			}
		}
		writeDone <- nil
	}()
	for {
		page, listErr := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: -1, Limit: 200})
		requireNoError(t, listErr)
		if page.RefreshRequired || page.HasMore || len(page.Items) != int(page.Cursor)+2 {
			t.Fatalf("List(concurrent snapshot) cursor=%d items=%d page=%#v", page.Cursor, len(page.Items), page)
		}
		select {
		case writeErr := <-writeDone:
			requireNoError(t, writeErr)
			goto writesComplete
		default:
			time.Sleep(time.Millisecond)
		}
	}

writesComplete:
	journal, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: 0, Limit: 1})
	requireNoError(t, err)
	if journal == nil || !journal.HasMore || len(journal.Entries) != 1 || journal.CurrentCursor != concurrentCreates+1 {
		t.Fatalf("ListJournal(bounded page) = %#v", journal)
	}
	reserveReady := func(label string) *model.AttemptWorkspaceObject {
		t.Helper()
		object, reserveErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
			Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
		})
		requireNoError(t, reserveErr)
		object, reserveErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
			ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(),
			Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: int64(len(label)), SHA256: strings.Repeat("c", 64)}})
		requireNoError(t, reserveErr)
		return object
	}
	apply := func(operation model.AttemptWorkspaceMutationKind, label string, configure func(*store.ExamAttemptWorkspaceMutation)) (*store.ExamAttemptWorkspaceMutationResult, error) {
		t.Helper()
		input := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: operation,
			EntryID: model.NewAttemptWorkspaceEntryID(), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		configure(input)
		return workspace.ApplyMutation(ctx, input, examCommand(fixture.candidate.ID,
			store.ExamAttemptWorkspaceMutationOperation, "workspace-"+label, "workspace-"+label))
	}

	replacement := reserveReady("replacement")
	replaced, err := apply(model.AttemptWorkspaceMutationReplaceFile, "replace", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion, input.ObjectID = entryID, mutation.DestinationPath, ready.ContentVersion, replacement.ID
	})
	requireNoError(t, err)
	if replaced.Entry == nil || replaced.Entry.EntryID != entryID || replaced.Entry.ContentVersion != replacement.ContentVersion ||
		replaced.Change.Cursor != concurrentCreates+2 {
		t.Fatalf("ApplyMutation(replace) = %#v", replaced)
	}
	resolved, err := workspace.ResolveFile(ctx, journalAccess(access), entryID)
	requireNoError(t, err)
	if resolved == nil || resolved.Entry.ContentVersion != replacement.ContentVersion || resolved.AttemptObjectID != replacement.ID {
		t.Fatalf("ResolveFile(replaced) = %#v", resolved)
	}
	movedFile, err := apply(model.AttemptWorkspaceMutationMoveEntry, "move-file", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.DestinationPath = entryID, mutation.DestinationPath, "renamed.go"
	})
	requireNoError(t, err)
	resolvedAfterMove, err := workspace.ResolveFile(ctx, journalAccess(access), entryID)
	requireNoError(t, err)
	if movedFile.Entry == nil || movedFile.Entry.Path != "renamed.go" || resolvedAfterMove.AttemptObjectID != resolved.AttemptObjectID ||
		resolvedAfterMove.ContentVersion != resolved.ContentVersion {
		t.Fatalf("file move changed content object: moved=%#v before=%#v after=%#v", movedFile, resolved, resolvedAfterMove)
	}
	var starterFile store.CandidateAttemptWorkspaceItem
	for _, item := range manifest.Items {
		if item.Kind == model.StarterWorkspaceEntryFile && item.EntryID != entryID {
			starterFile = item
			break
		}
	}
	if !starterFile.EntryID.IsValid() {
		t.Fatal("fixture has no Starter-origin file")
	}
	cowObject := reserveReady("copy-on-write")
	cow, err := apply(model.AttemptWorkspaceMutationReplaceFile, "replace-starter", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion, input.ObjectID = starterFile.EntryID, starterFile.Path,
			starterFile.ContentVersion, cowObject.ID
	})
	requireNoError(t, err)
	if cow.Entry == nil || cow.Entry.EntryID != starterFile.EntryID || cow.Entry.ContentVersion != cowObject.ContentVersion {
		t.Fatalf("ApplyMutation(copy-on-write starter) = %#v", cow)
	}
	cowResolved, err := workspace.ResolveFile(ctx, journalAccess(access), starterFile.EntryID)
	requireNoError(t, err)
	if cowResolved.StorageOrigin != model.AttemptWorkspaceStorageAttempt || cowResolved.AttemptObjectID != cowObject.ID ||
		cowResolved.Entry.EntryID != starterFile.EntryID {
		t.Fatalf("ResolveFile(copy-on-write starter) = %#v", cowResolved)
	}
	childObject := reserveReady("child")
	childID := model.NewAttemptWorkspaceEntryID()
	child, err := apply(model.AttemptWorkspaceMutationCreateFile, "create-child", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.DestinationPath, input.ObjectID = childID, "race-a/child.txt", childObject.ID
	})
	requireNoError(t, err)
	if child.Entry == nil || child.Entry.Path != "race-a/child.txt" {
		t.Fatalf("ApplyMutation(create child) = %#v", child)
	}
	var raceARoot model.AttemptWorkspaceEntryID
	for i := range mutations {
		if mutations[i].DestinationPath == "race-a" {
			raceARoot = mutations[i].EntryID
			break
		}
	}
	moved, err := apply(model.AttemptWorkspaceMutationMoveEntry, "move-tree", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.DestinationPath = raceARoot, "race-a", "moved"
	})
	requireNoError(t, err)
	if moved.Entry == nil || moved.Entry.Path != "moved" {
		t.Fatalf("ApplyMutation(move directory) = %#v", moved)
	}
	_, err = apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-nonempty", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath = raceARoot, "moved"
	})
	if !store.IsConflict(err) {
		t.Fatalf("ApplyMutation(delete non-empty directory) error = %v", err)
	}
	deletedChild, err := apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-child", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion = childID, "moved/child.txt", childObject.ContentVersion
	})
	requireNoError(t, err)
	if deletedChild.Entry != nil || deletedChild.Change.OldPath != "moved/child.txt" {
		t.Fatalf("ApplyMutation(delete child) = %#v", deletedChild)
	}
	deletedDirectory, err := apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-directory", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath = raceARoot, "moved"
	})
	requireNoError(t, err)
	if deletedDirectory.Entry != nil || deletedDirectory.Change.EntryKind != model.StarterWorkspaceEntryDirectory {
		t.Fatalf("ApplyMutation(delete directory) = %#v", deletedDirectory)
	}
	_, err = apply(model.AttemptWorkspaceMutationMoveEntry, "path-collision", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.DestinationPath = mutations[1].EntryID, "race-b", "race-c"
	})
	if !store.IsConflict(err) {
		t.Fatalf("ApplyMutation(path collision) error = %v", err)
	}
	_, err = apply(model.AttemptWorkspaceMutationCreateDirectory, "missing-parent", func(input *store.ExamAttemptWorkspaceMutation) {
		input.DestinationPath = "missing/child"
	})
	assertExamAttemptConflict(t, err, "attempt_workspace_path")

	type moveRaceResult struct {
		input   *store.ExamAttemptWorkspaceMutation
		command *store.CommandIdempotency
		result  *store.ExamAttemptWorkspaceMutationResult
		err     error
	}
	moveRace := make(chan moveRaceResult, 2)
	for i, destination := range []string{"race-winner-a", "race-winner-b"} {
		input := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationMoveEntry,
			EntryID: mutations[3].EntryID, ExpectedPath: "race-d", DestinationPath: destination,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		command := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation,
			"workspace-move-race-"+string(rune('a'+i)), "workspace-move-race-"+string(rune('a'+i)))
		go func() {
			result, raceErr := workspace.ApplyMutation(ctx, input, command)
			moveRace <- moveRaceResult{input: input, command: command, result: result, err: raceErr}
		}()
	}
	firstRace, secondRace := <-moveRace, <-moveRace
	var winner moveRaceResult
	if firstRace.err == nil && store.IsConflict(secondRace.err) {
		winner = firstRace
	} else if secondRace.err == nil && store.IsConflict(firstRace.err) {
		winner = secondRace
	} else {
		t.Fatalf("ApplyMutation(same-entry race) errors = %v, %v", firstRace.err, secondRace.err)
	}
	replayWinner := *winner.input
	replayWinner.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	winnerReplay, err := workspace.ApplyMutation(ctx, &replayWinner, winner.command)
	requireNoError(t, err)
	if winnerReplay == nil || !winnerReplay.Replayed || winnerReplay.Change.Cursor != winner.result.Change.Cursor ||
		winnerReplay.Entry == nil || winnerReplay.Entry.Path != winner.result.Entry.Path {
		t.Fatalf("ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	}
	for index := 0; index < 5; index++ {
		large, reserveErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
			Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
		})
		requireNoError(t, reserveErr)
		large, reserveErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
			ObjectID: large.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{
				MediaType: "application/octet-stream", SizeBytes: model.ExamWorkspaceDefaultMaximumFileBytes, SHA256: strings.Repeat("d", 64)}})
		requireNoError(t, reserveErr)
		_, reserveErr = apply(model.AttemptWorkspaceMutationCreateFile, "size-quota-"+string(rune('a'+index)), func(input *store.ExamAttemptWorkspaceMutation) {
			input.DestinationPath, input.ObjectID = "large-"+string(rune('a'+index)), large.ID
		})
		if index < 4 {
			requireNoError(t, reserveErr)
		} else {
			assertExamAttemptConflict(t, reserveErr, "attempt_workspace_size_limit")
		}
	}

	if err = workspace.MarkObjectReclaimable(ctx, ready.ID); !store.IsConflict(err) {
		t.Fatalf("MarkObjectReclaimable(current referenced object) error = %v", err)
	}
	abandoned, err := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	requireNoError(t, err)
	requireNoError(t, workspace.MarkObjectReclaimable(ctx, abandoned.ID))
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, 10, "attempt-workspace-cleanup")
	requireNoError(t, err)
	if len(claimed) != 0 {
		t.Fatalf("ClaimObjectsForCleanup(before safety window) = %#v", claimed)
	}
	if len(probes) > 0 && probes[0].MakeObjectCleanupDue != nil {
		probes[0].MakeObjectCleanupDue(t, ctx, ready.ID)
		probes[0].MakeObjectCleanupDue(t, ctx, abandoned.ID)
		claimed, err = workspace.ClaimObjectsForCleanup(ctx, 10, "attempt-workspace-cleanup")
		requireNoError(t, err)
		if len(claimed) != 1 || claimed[0].ID != abandoned.ID ||
			claimed[0].State != model.AttemptWorkspaceObjectClaimed || claimed[0].ClaimToken != "attempt-workspace-cleanup" {
			t.Fatalf("ClaimObjectsForCleanup() = %#v", claimed)
		}
		if probes[0].MakeCleanupClaimStale != nil {
			probes[0].MakeCleanupClaimStale(t, ctx, abandoned.ID)
			claimed, err = workspace.ClaimObjectsForCleanup(ctx, 10, "attempt-workspace-cleanup-stale-retry")
			requireNoError(t, err)
			if len(claimed) != 1 || claimed[0].ID != abandoned.ID || claimed[0].ClaimToken != "attempt-workspace-cleanup-stale-retry" {
				t.Fatalf("ClaimObjectsForCleanup(stale retry) = %#v", claimed)
			}
		}
		if err = workspace.ReleaseObjectCleanup(ctx, abandoned.ID, "attempt-workspace-cleanup"); !store.IsConflict(err) {
			t.Fatalf("ReleaseObjectCleanup(old token) error = %v", err)
		}
		requireNoError(t, workspace.ReleaseObjectCleanup(ctx, abandoned.ID, "attempt-workspace-cleanup-stale-retry"))
		claimed, err = workspace.ClaimObjectsForCleanup(ctx, 10, "attempt-workspace-cleanup-retry")
		requireNoError(t, err)
		if len(claimed) != 1 || claimed[0].ID != abandoned.ID {
			t.Fatalf("ClaimObjectsForCleanup(after release) = %#v", claimed)
		}
		requireNoError(t, workspace.CompleteObjectCleanup(ctx, abandoned.ID, "attempt-workspace-cleanup-retry"))
		requireNoError(t, workspace.CompleteObjectCleanup(ctx, abandoned.ID, "attempt-workspace-cleanup-retry"))
	}
	if len(probes) > 0 && probes[0].FillWorkspaceEntryQuota != nil {
		probes[0].FillWorkspaceEntryQuota(t, ctx, connected.Workspace.ID)
		_, err = apply(model.AttemptWorkspaceMutationCreateDirectory, "entry-quota", func(input *store.ExamAttemptWorkspaceMutation) {
			input.DestinationPath = "entry-overflow"
		})
		assertExamAttemptConflict(t, err, "attempt_workspace_entry_limit")
	}
	if len(probes) > 0 && probes[0].MakeJournalGap != nil {
		probes[0].MakeJournalGap(t, ctx, connected.Workspace.ID)
		journal, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: 0, Limit: 200})
		requireNoError(t, err)
		if journal == nil || !journal.RefreshRequired || len(journal.Entries) != 0 {
			t.Fatalf("ListJournal(retained-window gap) = %#v", journal)
		}
	}

	endedAt := model.GetMillis()
	ended, err := ss.ClassMember().End(ctx, fixture.membership.ID.String(), fixture.membership.Revision, endedAt)
	requireNoError(t, err)
	// Membership timestamps have millisecond precision while PostgreSQL decides
	// current eligibility with a higher-precision clock. Cross the millisecond
	// boundary before asserting that the just-ended relationship is inactive.
	time.Sleep(100 * time.Millisecond)
	if _, err = workspace.ResolveMutationTarget(ctx, access); !store.IsNotFound(err) {
		t.Fatalf("ResolveMutationTarget(after membership revoke) error = %v", err)
	}
	restored, err := ss.ClassMember().Enroll(ctx, &model.ClassMember{ClassID: fixture.class.ID,
		UserID: fixture.candidate.ID, StartsAt: model.TimeFromMillis(endedAt)})
	requireNoError(t, err)
	target, err = workspace.ResolveMutationTarget(ctx, access)
	requireNoError(t, err)
	if target == nil || target.WorkspaceID != connected.Workspace.ID || restored.Membership == nil || !ended.EndsAt.Valid {
		t.Fatalf("ResolveMutationTarget(with historical and current membership) = %#v; restored=%#v ended=%#v", target, restored, ended)
	}
	if len(probes) > 0 && probes[0].SetParticipationLeaseExpired != nil {
		testExamAttemptWorkspaceSuspensionAndMultiNodeReplay(t, ctx, ss, workspace, probes[0])
	}
}

func testExamAttemptWorkspaceSuspensionAndMultiNodeReplay(t *testing.T, ctx context.Context, ss store.Store,
	workspace store.ExamAttemptWorkspaceStore, probe ExamAttemptWorkspaceSQLProbe,
) {
	t.Helper()
	fixture := newExamAttemptFixture(t, ctx, ss)
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-fence-connect", "workspace-fence-connect"))
	requireNoError(t, err)
	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}

	if probe.ConcurrentPeer != nil {
		entryID := model.NewAttemptWorkspaceEntryID()
		inputs := [2]*store.ExamAttemptWorkspaceMutation{}
		for index := range inputs {
			inputs[index] = &store.ExamAttemptWorkspaceMutation{Access: access,
				Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: entryID, DestinationPath: "multi-node",
				AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		}
		command := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation,
			"workspace-multi-node", "workspace-multi-node")
		type result struct {
			value *store.ExamAttemptWorkspaceMutationResult
			err   error
		}
		started := make(chan struct{})
		results := make(chan result, 2)
		for index, adapter := range []store.ExamAttemptWorkspaceStore{workspace, probe.ConcurrentPeer} {
			input := inputs[index]
			go func() {
				<-started
				value, applyErr := adapter.ApplyMutation(ctx, input, command)
				results <- result{value: value, err: applyErr}
			}()
		}
		close(started)
		first, second := <-results, <-results
		requireNoError(t, first.err)
		requireNoError(t, second.err)
		if first.value == nil || second.value == nil || first.value.Replayed == second.value.Replayed ||
			first.value.Change.Cursor != second.value.Change.Cursor || first.value.Change.Cursor != 1 ||
			first.value.Entry == nil || second.value.Entry == nil || first.value.Entry.EntryID != entryID ||
			second.value.Entry.EntryID != entryID {
			t.Fatalf("ApplyMutation(two-adapter exact race) = %#v, %#v", first.value, second.value)
		}
		for _, input := range inputs {
			requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
		}
		journal, journalErr := probe.ConcurrentPeer.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
			Access: journalAccess(access), AfterCursor: 0, Limit: 200})
		requireNoError(t, journalErr)
		if journal == nil || journal.CurrentCursor != 1 || len(journal.Entries) != 1 ||
			journal.Entries[0].Cursor != 1 || journal.Entries[0].EntryID != entryID {
			t.Fatalf("ListJournal(two-adapter exact race) = %#v", journal)
		}
	}

	probe.SetParticipationLeaseExpired(t, ctx, connected.Participation.ID)
	audit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
	expired, err := ss.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(),
		FlagID: model.NewIntegrityFlagID(), SuspensionID: model.NewAttemptSuspensionID(),
		AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
	})
	requireNoError(t, err)
	if expired == nil || expired.Attempt == nil || expired.Attempt.State != model.ExamAttemptSuspended {
		t.Fatalf("ExpireParticipation(workspace suspension fence) = %#v", expired)
	}
	_, err = workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	if !store.IsConflict(err) {
		t.Fatalf("ReserveObject(suspended Attempt) error = %v", err)
	}
}

func journalAccess(access store.ExamAttemptWorkspaceMutationAccess) store.CandidateAttemptAccess {
	return store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID,
		SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID,
		DPoPKeyThumbprint: access.DPoPKeyThumbprint, ConnectionID: access.ConnectionID,
		ContinuityCredentialHash: access.ContinuityCredentialHash}
}
