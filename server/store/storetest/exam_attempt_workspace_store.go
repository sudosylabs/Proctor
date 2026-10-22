// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	admittedCapacity := model.DefaultExamCapacityPolicy()
	admittedCapacity.WorkspaceMaximumEntries = 420
	admittedCapacity.WorkspaceMaximumFileBytes = 8 << 20
	admittedCapacity.WorkspaceMaximumTotalBytes = 5 * admittedCapacity.WorkspaceMaximumFileBytes
	fixture := newExamAttemptFixtureWithCapacity(t, ctx, ss, nil, nil, admittedCapacity)
	// Admission and later Workspace growth use the policy frozen into the
	// published Revision, not a subsequently lowered Institution policy.
	institution, err := ss.Institution().GetSingleton(ctx)
	require.NoError(t, err)
	lowered := model.DefaultExamCapacityPolicy()
	lowered.WorkspaceMaximumEntries = 1
	lowered.WorkspaceMaximumFileBytes = 1
	lowered.WorkspaceMaximumTotalBytes = 1
	institution.ExamCapacity = lowered
	_, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	credentialHash := model.HashToken(model.NewCredentialToken())
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-connect", "workspace-connect"))
	require.NoError(t, err)

	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: connected.Connection.ID, ContinuityCredentialHash: credentialHash}
	// This scenario outlasts one initial Participation lease on a loaded
	// database, so the fixture renews it between phases as a live client would.
	renewal := &store.ExamAttemptParticipationRenewal{DesktopCompatibilityPolicyRevision: 1, AttemptID: connected.Attempt.ID,
		ParticipationID: connected.Participation.ID, ConnectionID: connected.Connection.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, Generation: connected.Participation.Generation, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ContinuityCredentialHash: credentialHash}
	prepareParticipationRenewalCoverage(t, ctx, ss, renewal)
	renew := func() {
		t.Helper()
		renewal.ConnectionID = access.ConnectionID
		renewal.Sequence++
		_, renewErr := ss.ExamAttempt().RenewParticipation(ctx, renewal)
		require.NoError(t, renewErr)
	}
	presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, store.CandidateAttemptAccess{
		AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
		DesktopRegistrationID: access.DesktopRegistrationID, DPoPKeyThumbprint: access.DPoPKeyThumbprint,
		ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash,
	})
	require.NoError(t, err)
	require.Equal(t, admittedCapacity, presentation.Capacity)
	require.NotEqual(t, lowered, presentation.Capacity)
	reservation := &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()}
	reserved, err := workspace.ReserveObject(ctx, reservation)
	require.NoError(t, err)
	require.NotNil(t, reserved)
	require.Equal(t, reservation.ObjectID, reserved.ID)
	require.Equal(t, connected.Workspace.ID, reserved.WorkspaceID)
	require.Equal(t, model.AttemptWorkspaceObjectStaged, reserved.State)
	require.Equal(t, model.AttemptWorkspaceStorageAttempt, reserved.StorageOrigin)
	require.False(t, reserved.CreatedAt.IsZero())
	require.Equal(t, reserved.CreatedAt, reserved.UpdatedAt)
	require.Equal(t, model.AttemptWorkspaceStageLifetime, reserved.ExpiresAt.Sub(reserved.CreatedAt))
	require.False(t, reserved.HasContent())
	replayed, err := workspace.ReserveObject(ctx, reservation)
	require.NoError(t, err)
	require.NotNil(t, replayed, "ReserveObject(repeat) = %#v, first = %#v", replayed, reserved)
	require.Equal(t, reserved.ID, replayed.ID, "ReserveObject(repeat) = %#v, first = %#v", replayed, reserved)
	require.True(t, replayed.CreatedAt.Equal(reserved.CreatedAt), "ReserveObject(repeat) = %#v, first = %#v", replayed, reserved)
	require.True(t, replayed.ExpiresAt.Equal(reserved.ExpiresAt), "ReserveObject(repeat) = %#v, first = %#v", replayed, reserved)
	target, err := workspace.ResolveMutationTarget(ctx, access)
	require.NoError(t, err)
	require.NotNil(t, target)
	require.Equal(t, fixture.examID, target.ExamID)
	require.Equal(t, fixture.sitting.ID, target.SittingID)
	require.Equal(t, fixture.class.ID, target.ClassID)
	require.Equal(t, fixture.candidate.ID, target.CandidateUserID)
	require.Equal(t, connected.Workspace.ID, target.WorkspaceID)
	readyInput := &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: reserved.ID,
		ContentVersion: model.NewWorkspaceContentVersion(),
		Content:        model.AttemptWorkspaceContent{MediaType: "text/x-go", SizeBytes: 12, SHA256: strings.Repeat("a", 64)}}
	ready, err := workspace.MarkObjectReady(ctx, readyInput)
	require.NoError(t, err)
	require.NotNil(t, ready)
	require.Equal(t, reserved.ID, ready.ID)
	require.Equal(t, model.AttemptWorkspaceObjectStaged, ready.State)
	require.Equal(t, readyInput.ContentVersion, ready.ContentVersion)
	require.Equal(t, readyInput.Content.MediaType, ready.MediaType)
	require.Equal(t, readyInput.Content.SizeBytes, ready.SizeBytes)
	require.Equal(t, readyInput.Content.SHA256, ready.SHA256)
	readyReplay, err := workspace.MarkObjectReady(ctx, readyInput)
	require.NoError(t, err)
	require.NotNil(t, readyReplay, "MarkObjectReady(repeat) = %#v, first = %#v", readyReplay, ready)
	require.Equal(t, ready.ID, readyReplay.ID, "MarkObjectReady(repeat) = %#v, first = %#v", readyReplay, ready)
	require.True(t, readyReplay.UpdatedAt.Equal(ready.UpdatedAt), "MarkObjectReady(repeat) = %#v, first = %#v", readyReplay, ready)
	changedReady := *readyInput
	changedReady.Content.SHA256 = strings.Repeat("b", 64)
	_, err = workspace.MarkObjectReady(ctx, &changedReady)
	require.True(t, store.IsConflict(err), "MarkObjectReady(changed repeat) error")
	entryID := model.NewAttemptWorkspaceEntryID()
	mutation := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateFile,
		EntryID: entryID, DestinationPath: "notes.go", ObjectID: ready.ID,
		AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	command := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation,
		"workspace-create-file", "workspace-create-file")
	created, err := workspace.ApplyMutation(ctx, mutation, command)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.False(t, created.Replayed)
	require.Equal(t, fixture.sitting.ID, created.SittingID)
	require.Equal(t, fixture.class.ID, created.ClassID)
	require.Equal(t, fixture.candidate.ID, created.CandidateUserID)
	require.Equal(t, connected.Workspace.ID, created.WorkspaceID)
	require.NotNil(t, created.Entry)
	require.Equal(t, entryID, created.Entry.EntryID)
	require.Equal(t, mutation.DestinationPath, created.Entry.Path)
	require.Equal(t, ready.ContentVersion, created.Entry.ContentVersion)
	require.Equal(t, int64(1), created.Change.Cursor)
	require.Equal(t, model.AttemptWorkspaceMutationCreateFile, created.Change.Operation)
	require.Equal(t, mutation.DestinationPath, created.Change.NewPath)
	replayMutation := *mutation
	replayMutation.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	replayedMutation, err := workspace.ApplyMutation(ctx, &replayMutation, command)
	require.NoError(t, err)
	require.NotNil(t, replayedMutation, "ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	require.True(t, replayedMutation.Replayed, "ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	require.Equal(t, created.Change.Cursor, replayedMutation.Change.Cursor, "ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	require.NotNil(t, replayedMutation.Entry, "ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	require.Equal(t, created.Entry.EntryID, replayedMutation.Entry.EntryID, "ApplyMutation(replay) = %#v, first = %#v", replayedMutation, created)
	requireSuccessfulAudit(t, ctx, ss, mutation.AuditEventID)
	requireSuccessfulAudit(t, ctx, ss, replayMutation.AuditEventID)
	closed, err := ss.ExamAttempt().CloseConnection(ctx, &store.ExamAttemptConnectionClose{
		ConnectionID: access.ConnectionID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		Reason: model.AttemptConnectionCloseTransport, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, closed)
	require.True(t, closed.Changed)
	_, err = workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: -1, Limit: 200})
	require.True(t, store.IsNotFound(err), "List(after Connection close) error")
	_, err = workspace.ResolveFile(ctx, journalAccess(access), entryID)
	require.True(t, store.IsNotFound(err), "ResolveFile(after Connection close) error")
	_, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), Limit: 200})
	require.True(t, store.IsNotFound(err), "ListJournal(after Connection close) error")
	reconnect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: credentialHash, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, reconnect)
	reconnected, err := ss.ExamAttempt().Connect(ctx, reconnect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-reconnect", "workspace-reconnect"))
	require.NoError(t, err)
	require.NotNil(t, reconnected)
	require.Equal(t, connected.Attempt.ID, reconnected.Attempt.ID)
	require.Equal(t, connected.Workspace.ID, reconnected.Workspace.ID)
	require.Equal(t, connected.Participation.ID, reconnected.Participation.ID)
	require.Equal(t, connected.Participation.Generation, reconnected.Participation.Generation)
	require.Equal(t, reconnect.ConnectionID, reconnected.Connection.ID)
	access.ConnectionID = reconnected.Connection.ID
	replayAfterReconnect := *mutation
	replayAfterReconnect.Access = access
	replayAfterReconnect.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	replayedAfterReconnect, err := workspace.ApplyMutation(ctx, &replayAfterReconnect, command)
	require.NoError(t, err)
	require.NotNil(t, replayedAfterReconnect)
	require.True(t, replayedAfterReconnect.Replayed)
	require.Equal(t, created.Change.Cursor, replayedAfterReconnect.Change.Cursor)
	requireSuccessfulAudit(t, ctx, ss, replayAfterReconnect.AuditEventID)
	journal, err := workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
		Access: store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID,
			SessionID: access.SessionID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
			DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash},
		AfterCursor: 0, Limit: model.AttemptWorkspaceJournalReadMaximum})
	require.NoError(t, err)
	require.NotNil(t, journal)
	require.Equal(t, connected.Workspace.ID, journal.WorkspaceID)
	require.Equal(t, int64(1), journal.CurrentCursor)
	require.False(t, journal.HasMore)
	require.False(t, journal.RefreshRequired)
	require.Len(t, journal.Entries, 1)
	require.Equal(t, int64(1), journal.Entries[0].Cursor)
	require.Equal(t, entryID, journal.Entries[0].EntryID)
	require.Equal(t, mutation.DestinationPath, journal.Entries[0].NewPath)
	manifest, err := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: store.CandidateAttemptAccess{
		AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID, SessionID: access.SessionID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		ConnectionID: access.ConnectionID, ContinuityCredentialHash: access.ContinuityCredentialHash}, ExpectedCursor: -1, Limit: 200})
	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.Equal(t, connected.Workspace.ID, manifest.WorkspaceID)
	require.Equal(t, int64(1), manifest.Cursor)
	require.False(t, manifest.RefreshRequired)
	require.Len(t, manifest.Items, 3)
	refresh, err := workspace.List(ctx, store.CandidateWorkspaceListOptions{Access: journalAccess(access), ExpectedCursor: 0,
		AfterEntryID: manifest.Items[0].EntryID, Limit: 200})
	require.NoError(t, err)
	require.NotNil(t, refresh)
	require.True(t, refresh.RefreshRequired)
	require.Empty(t, refresh.Items)
	require.Equal(t, int64(1), refresh.Cursor)

	const concurrentCreates = 12
	mutations := make([]*store.ExamAttemptWorkspaceMutation, concurrentCreates)
	commands := make([]*store.CommandIdempotency, concurrentCreates)
	for i := range mutations {
		mutations[i] = &store.ExamAttemptWorkspaceMutation{Access: access,
			Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(),
			DestinationPath: "race-" + string(rune('a'+i)), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID,
			AuditAt: model.NowUTC()}
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
		require.NoError(t, listErr)
		require.False(t, page.RefreshRequired, "List(concurrent snapshot) cursor=%d items=%d page=%#v", page.Cursor, len(page.Items), page)
		require.False(t, page.HasMore, "List(concurrent snapshot) cursor=%d items=%d page=%#v", page.Cursor, len(page.Items), page)
		require.Len(t, page.Items, int(page.Cursor)+2, "List(concurrent snapshot) cursor=%d items=%d page=%#v", page.Cursor, len(page.Items), page)
		select {
		case writeErr := <-writeDone:
			require.NoError(t, writeErr)
			goto writesComplete
		default:
			time.Sleep(time.Millisecond)
		}
	}

writesComplete:
	journal, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: 0, Limit: 1})
	require.NoError(t, err)
	require.NotNil(t, journal)
	require.True(t, journal.HasMore)
	require.Len(t, journal.Entries, 1)
	require.Equal(t, int64(concurrentCreates+1), journal.CurrentCursor)
	reserveReady := func(label string) *model.AttemptWorkspaceObject {
		t.Helper()
		object, reserveErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
			Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
		})
		require.NoError(t, reserveErr)
		object, reserveErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
			ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(),
			Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: int64(len(label)), SHA256: strings.Repeat("c", 64)}})
		require.NoError(t, reserveErr)
		return object
	}
	apply := func(operation model.AttemptWorkspaceMutationKind, label string, configure func(*store.ExamAttemptWorkspaceMutation)) (*store.ExamAttemptWorkspaceMutationResult, error) {
		t.Helper()
		input := &store.ExamAttemptWorkspaceMutation{Access: access, Operation: operation,
			EntryID: model.NewAttemptWorkspaceEntryID(), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
		configure(input)
		return workspace.ApplyMutation(ctx, input, examCommand(fixture.candidate.ID,
			store.ExamAttemptWorkspaceMutationOperation, "workspace-"+label, "workspace-"+label))
	}

	renew()
	replacement := reserveReady("replacement")
	replaced, err := apply(model.AttemptWorkspaceMutationReplaceFile, "replace", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion, input.ObjectID = entryID, mutation.DestinationPath, ready.ContentVersion, replacement.ID
	})
	require.NoError(t, err)
	require.NotNil(t, replaced.Entry)
	require.Equal(t, entryID, replaced.Entry.EntryID)
	require.Equal(t, replacement.ContentVersion, replaced.Entry.ContentVersion)
	require.Equal(t, int64(concurrentCreates+2), replaced.Change.Cursor)
	resolved, err := workspace.ResolveFile(ctx, journalAccess(access), entryID)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, replacement.ContentVersion, resolved.Entry.ContentVersion)
	require.Equal(t, replacement.ID, resolved.AttemptObjectID)
	movedFile, err := apply(model.AttemptWorkspaceMutationMoveEntry, "move-file", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.DestinationPath = entryID, mutation.DestinationPath, "renamed.go"
	})
	require.NoError(t, err)
	resolvedAfterMove, err := workspace.ResolveFile(ctx, journalAccess(access), entryID)
	require.NoError(t, err)
	require.NotNil(t, movedFile.Entry, "file move changed content object: moved=%#v before=%#v after=%#v", movedFile, resolved, resolvedAfterMove)
	require.Equal(t, "renamed.go", movedFile.Entry.Path, "file move changed content object: moved=%#v before=%#v after=%#v", movedFile, resolved, resolvedAfterMove)
	require.Equal(t, resolved.AttemptObjectID, resolvedAfterMove.AttemptObjectID, "file move changed content object: moved=%#v before=%#v after=%#v", movedFile, resolved, resolvedAfterMove)
	require.Equal(t, resolved.ContentVersion, resolvedAfterMove.ContentVersion, "file move changed content object: moved=%#v before=%#v after=%#v", movedFile, resolved, resolvedAfterMove)
	var starterFile store.CandidateAttemptWorkspaceItem
	for _, item := range manifest.Items {
		if item.Kind == model.StarterWorkspaceEntryFile && item.EntryID != entryID {
			starterFile = item
			break
		}
	}
	require.True(t, starterFile.EntryID.IsValid(), "fixture has no Starter-origin file")
	cowObject := reserveReady("copy-on-write")
	cow, err := apply(model.AttemptWorkspaceMutationReplaceFile, "replace-starter", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion, input.ObjectID = starterFile.EntryID, starterFile.Path,
			starterFile.ContentVersion, cowObject.ID
	})
	require.NoError(t, err)
	require.NotNil(t, cow.Entry)
	require.Equal(t, starterFile.EntryID, cow.Entry.EntryID)
	require.Equal(t, cowObject.ContentVersion, cow.Entry.ContentVersion)
	cowResolved, err := workspace.ResolveFile(ctx, journalAccess(access), starterFile.EntryID)
	require.NoError(t, err)
	require.Equal(t, model.AttemptWorkspaceStorageAttempt, cowResolved.StorageOrigin)
	require.Equal(t, cowObject.ID, cowResolved.AttemptObjectID)
	require.Equal(t, starterFile.EntryID, cowResolved.Entry.EntryID)
	childObject := reserveReady("child")
	childID := model.NewAttemptWorkspaceEntryID()
	child, err := apply(model.AttemptWorkspaceMutationCreateFile, "create-child", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.DestinationPath, input.ObjectID = childID, "race-a/child.txt", childObject.ID
	})
	require.NoError(t, err)
	require.NotNil(t, child.Entry)
	require.Equal(t, "race-a/child.txt", child.Entry.Path)
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
	require.NoError(t, err)
	require.NotNil(t, moved.Entry)
	require.Equal(t, "moved", moved.Entry.Path)
	_, err = apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-nonempty", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath = raceARoot, "moved"
	})
	require.True(t, store.IsConflict(err), "ApplyMutation(delete non-empty directory) error")
	deletedChild, err := apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-child", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.ExpectedContentVersion = childID, "moved/child.txt", childObject.ContentVersion
	})
	require.NoError(t, err)
	require.Nil(t, deletedChild.Entry)
	require.Equal(t, "moved/child.txt", deletedChild.Change.OldPath)
	deletedDirectory, err := apply(model.AttemptWorkspaceMutationDeleteEntry, "delete-directory", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath = raceARoot, "moved"
	})
	require.NoError(t, err)
	require.Nil(t, deletedDirectory.Entry)
	require.Equal(t, model.StarterWorkspaceEntryDirectory, deletedDirectory.Change.EntryKind)
	_, err = apply(model.AttemptWorkspaceMutationMoveEntry, "path-collision", func(input *store.ExamAttemptWorkspaceMutation) {
		input.EntryID, input.ExpectedPath, input.DestinationPath = mutations[1].EntryID, "race-b", "race-c"
	})
	require.True(t, store.IsConflict(err), "ApplyMutation(path collision) error")
	_, err = apply(model.AttemptWorkspaceMutationCreateDirectory, "missing-parent", func(input *store.ExamAttemptWorkspaceMutation) {
		input.DestinationPath = "missing/child"
	})
	requireConflictConstraint(t, err, "attempt_workspace_path")

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
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
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
		require.FailNow(t, fmt.Sprintf("ApplyMutation(same-entry race) errors = %v, %v", firstRace.err, secondRace.err))
	}
	replayWinner := *winner.input
	replayWinner.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID
	winnerReplay, err := workspace.ApplyMutation(ctx, &replayWinner, winner.command)
	require.NoError(t, err)
	require.NotNil(t, winnerReplay, "ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	require.True(t, winnerReplay.Replayed, "ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	require.Equal(t, winner.result.Change.Cursor, winnerReplay.Change.Cursor, "ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	require.NotNil(t, winnerReplay.Entry, "ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	require.Equal(t, winner.result.Entry.Path, winnerReplay.Entry.Path, "ApplyMutation(race winner replay) = %#v, winner = %#v", winnerReplay, winner.result)
	for index := 0; index < 5; index++ {
		large, reserveErr := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
			Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
		})
		require.NoError(t, reserveErr)
		large, reserveErr = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access,
			ObjectID: large.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{
				MediaType: "application/octet-stream", SizeBytes: admittedCapacity.WorkspaceMaximumFileBytes, SHA256: strings.Repeat("d", 64)}})
		require.NoError(t, reserveErr)
		_, reserveErr = apply(model.AttemptWorkspaceMutationCreateFile, "size-quota-"+string(rune('a'+index)), func(input *store.ExamAttemptWorkspaceMutation) {
			input.DestinationPath, input.ObjectID = "large-"+string(rune('a'+index)), large.ID
		})
		if index < 4 {
			require.NoError(t, reserveErr)
		} else {
			requireConflictConstraint(t, reserveErr, "attempt_workspace_size_limit")
		}
	}

	err = workspace.MarkObjectReclaimable(ctx, ready.ID)
	require.True(t, store.IsConflict(err), "MarkObjectReclaimable(current referenced object) error")
	abandoned, err := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	require.NoError(t, err)
	require.NoError(t, workspace.MarkObjectReclaimable(ctx, abandoned.ID))
	claimed, err := workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 10, ClaimToken: "attempt-workspace-cleanup"})
	require.NoError(t, err)
	require.Empty(t, claimed)
	renew()
	if len(probes) > 0 && probes[0].MakeObjectCleanupDue != nil {
		probes[0].MakeObjectCleanupDue(t, ctx, ready.ID)
		probes[0].MakeObjectCleanupDue(t, ctx, abandoned.ID)
		claimed, err = workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 10, ClaimToken: "attempt-workspace-cleanup"})
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		require.Equal(t, abandoned.ID, claimed[0].ID)
		require.Equal(t, model.AttemptWorkspaceObjectClaimed, claimed[0].State)
		require.Equal(t, "attempt-workspace-cleanup", claimed[0].ClaimToken)
		if probes[0].MakeCleanupClaimStale != nil {
			probes[0].MakeCleanupClaimStale(t, ctx, abandoned.ID)
			claimed, err = workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 10, ClaimToken: "attempt-workspace-cleanup-stale-retry"})
			require.NoError(t, err)
			require.Len(t, claimed, 1)
			require.Equal(t, abandoned.ID, claimed[0].ID)
			require.Equal(t, "attempt-workspace-cleanup-stale-retry", claimed[0].ClaimToken)
		}
		err = workspace.ReleaseObjectCleanup(ctx, &store.AttemptWorkspaceCleanupRelease{ObjectID: abandoned.ID, ClaimToken: "attempt-workspace-cleanup"})
		require.True(t, store.IsConflict(err), "ReleaseObjectCleanup(old token) error")
		require.NoError(t, workspace.ReleaseObjectCleanup(ctx, &store.AttemptWorkspaceCleanupRelease{ObjectID: abandoned.ID, ClaimToken: "attempt-workspace-cleanup-stale-retry"}))
		claimed, err = workspace.ClaimObjectsForCleanup(ctx, &store.AttemptWorkspaceCleanupClaim{Limit: 10, ClaimToken: "attempt-workspace-cleanup-retry"})
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		require.Equal(t, abandoned.ID, claimed[0].ID)
		require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.AttemptWorkspaceCleanupCompletion{ObjectID: abandoned.ID, ClaimToken: "attempt-workspace-cleanup-retry"}))
		require.NoError(t, workspace.CompleteObjectCleanup(ctx, &store.AttemptWorkspaceCleanupCompletion{ObjectID: abandoned.ID, ClaimToken: "attempt-workspace-cleanup-retry"}))
	}
	renew()
	if len(probes) > 0 && probes[0].FillWorkspaceEntryQuota != nil {
		probes[0].FillWorkspaceEntryQuota(t, ctx, connected.Workspace.ID)
		_, err = apply(model.AttemptWorkspaceMutationCreateDirectory, "entry-quota", func(input *store.ExamAttemptWorkspaceMutation) {
			input.DestinationPath = "entry-overflow"
		})
		requireConflictConstraint(t, err, "attempt_workspace_entry_limit")
	}
	if len(probes) > 0 && probes[0].MakeJournalGap != nil {
		probes[0].MakeJournalGap(t, ctx, connected.Workspace.ID)
		journal, err = workspace.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{Access: journalAccess(access), AfterCursor: 0, Limit: 200})
		require.NoError(t, err)
		require.NotNil(t, journal)
		require.True(t, journal.RefreshRequired)
		require.Empty(t, journal.Entries)
	}

	endedAt := model.NowUTC()
	ended, err := ss.ClassMember().SeedEnd(ctx, &store.ClassMemberSeedEnd{ID: fixture.membership.ID, ExpectedRevision: fixture.membership.Revision, EndAt: endedAt})
	require.NoError(t, err)
	// Membership timestamps have millisecond precision while PostgreSQL decides
	// current eligibility with a higher-precision clock. Cross the millisecond
	// boundary before asserting that the just-ended relationship is inactive.
	time.Sleep(100 * time.Millisecond)
	_, err = workspace.ResolveMutationTarget(ctx, access)
	require.True(t, store.IsNotFound(err), "ResolveMutationTarget(after membership revoke) error")
	restored, err := ss.ClassMember().SeedEnroll(ctx, &model.ClassMember{ClassID: fixture.class.ID,
		UserID: fixture.candidate.ID, StartsAt: model.TimeUTC(endedAt)})
	require.NoError(t, err)
	target, err = workspace.ResolveMutationTarget(ctx, access)
	require.NoError(t, err)
	require.NotNil(t, target, "ResolveMutationTarget(with historical and current membership) = %#v; restored=%#v ended=%#v", target, restored, ended)
	require.Equal(t, connected.Workspace.ID, target.WorkspaceID, "ResolveMutationTarget(with historical and current membership) = %#v; restored=%#v ended=%#v", target, restored, ended)
	require.NotNil(t, restored.Membership, "ResolveMutationTarget(with historical and current membership) = %#v; restored=%#v ended=%#v", target, restored, ended)
	require.True(t, ended.EndsAt.Valid, "ResolveMutationTarget(with historical and current membership) = %#v; restored=%#v ended=%#v", target, restored, ended)
	if len(probes) > 0 && probes[0].SetParticipationLeaseExpired != nil {
		testExamAttemptWorkspaceSuspensionAndMultiNodeReplay(t, ctx, ss, workspace, probes[0])
	}
	testAttemptWorkspaceRecursiveDeletion(t, ss, workspace, probes...)
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
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "workspace-fence-connect", "workspace-fence-connect"))
	require.NoError(t, err)
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
				AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID, AuditAt: model.NowUTC()}
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
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.NotNil(t, first.value, "ApplyMutation(two-adapter exact race)")
		require.NotNil(t, second.value, "ApplyMutation(two-adapter exact race)")
		require.NotEqual(t, second.value.Replayed, first.value.Replayed, "ApplyMutation(two-adapter exact race)")
		require.Equal(t, second.value.Change.Cursor, first.value.Change.Cursor, "ApplyMutation(two-adapter exact race)")
		require.Equal(t, int64(1), first.value.Change.Cursor, "ApplyMutation(two-adapter exact race)")
		require.NotNil(t, first.value.Entry, "ApplyMutation(two-adapter exact race)")
		require.NotNil(t, second.value.Entry, "ApplyMutation(two-adapter exact race)")
		require.Equal(t, entryID, first.value.Entry.EntryID, "ApplyMutation(two-adapter exact race)")
		require.Equal(t, entryID, second.value.Entry.EntryID, "ApplyMutation(two-adapter exact race)")
		for _, input := range inputs {
			requireSuccessfulAudit(t, ctx, ss, input.AuditEventID)
		}
		journal, journalErr := probe.ConcurrentPeer.ListJournal(ctx, store.CandidateWorkspaceJournalOptions{
			Access: journalAccess(access), AfterCursor: 0, Limit: 200})
		require.NoError(t, journalErr)
		require.NotNil(t, journal)
		require.Equal(t, int64(1), journal.CurrentCursor)
		require.Len(t, journal.Entries, 1)
		require.Equal(t, int64(1), journal.Entries[0].Cursor)
		require.Equal(t, entryID, journal.Entries[0].EntryID)
	}

	probe.SetParticipationLeaseExpired(t, ctx, connected.Participation.ID)
	audit := saveExamAttemptSystemAudit(t, ctx, ss, fixture)
	expired, err := ss.ExamAttempt().ExpireParticipation(ctx, &store.ExamAttemptParticipationExpiry{
		AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID,
		Generation: connected.Participation.Generation, EvidenceID: model.NewIntegrityEvidenceID(),
		FlagID: model.NewIntegrityFlagID(), SuspensionID: model.NewAttemptSuspensionID(),
		AuditEventID: audit.ID, AuditAt: model.NowUTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, expired)
	require.NotNil(t, expired.Attempt)
	require.Equal(t, model.ExamAttemptSuspended, expired.Attempt.State)
	_, err = workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{
		Access: access, ObjectID: model.NewAttemptWorkspaceObjectID(),
	})
	require.True(t, store.IsConflict(err), "ReserveObject(suspended Attempt) error")
}

func journalAccess(access store.ExamAttemptWorkspaceMutationAccess) store.CandidateAttemptAccess {
	return store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: access.CandidateUserID,
		SessionID: access.SessionID, DesktopRegistrationID: access.DesktopRegistrationID,
		DPoPKeyThumbprint: access.DPoPKeyThumbprint, ConnectionID: access.ConnectionID,
		ContinuityCredentialHash: access.ContinuityCredentialHash}
}
