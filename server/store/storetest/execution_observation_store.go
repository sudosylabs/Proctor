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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExecutionObservationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "control-connect", "control-connect"))
	requireNoError(t, err)
	grant, err := ss.ExecutionGrant().Reserve(ctx, store.ExecutionGrantReservation{ID: model.NewExecutionGrantID(), AttemptID: connected.Attempt.ID, HostID: "host", Image: "go", Network: model.ExecutionNetworkNone, At: model.NowUTC()})
	requireNoError(t, err)

	control, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, "epoch", model.NowUTC())
	requireNoError(t, err)
	control, err = ss.ExecutionGrant().AcknowledgeControl(ctx, control.Fence(), control.DesiredControlState, model.NowUTC())
	requireNoError(t, err)
	preparing, err := ss.ExecutionGrant().PrepareWorkspaceEffect(ctx, grant.ID, control.Revision, connected.Workspace.Cursor, model.NowUTC())
	requireNoError(t, err)
	_, err = ss.ExecutionGrant().MarkWorkspaceApplied(ctx, grant.ID, preparing.Revision, connected.Workspace.Cursor, model.NowUTC())
	requireNoError(t, err)
	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ConnectionID: connect.ConnectionID, ContinuityCredentialHash: connect.ContinuityCredentialHash}

	workspace := ss.ExamAttemptWorkspace()
	event := func(sequence int64, operation model.AttemptWorkspaceMutationKind, kind model.StarterWorkspaceEntryKind, identity, path string) store.ExecutionObservation {
		return store.ExecutionObservation{Fence: control.Fence(), HostSequence: sequence, Operation: operation, Kind: kind, NodeIdentity: identity, Path: path}
	}
	// The capture may precede a control transition, but it can never claim a
	// future revision or another epoch, even with otherwise valid access.
	for _, fence := range []model.ExecutionFence{
		{GrantID: grant.ID, EnvironmentEpoch: "another_epoch", ControlRevision: control.ControlRevision},
		{GrantID: grant.ID, EnvironmentEpoch: control.EnvironmentEpoch, ControlRevision: control.ControlRevision + 1},
	} {
		invalid := event(1, model.AttemptWorkspaceMutationCreateDirectory, model.StarterWorkspaceEntryDirectory, "invalid_capture", "invalid")
		invalid.Fence = fence
		source := access
		source.SourceGrantID, source.SourceObservation = grant.ID, &invalid
		if _, err := workspace.ResolveObservation(ctx, source); !store.IsConflict(err) {
			t.Fatalf("invalid capture fence accepted: %v", err)
		}
	}
	var lastInput store.ExamAttemptWorkspaceMutation
	var lastCommand *store.CommandIdempotency
	apply := func(observation store.ExecutionObservation) *store.ExamAttemptWorkspaceMutationResult {
		t.Helper()
		source := access
		source.SourceGrantID = grant.ID
		source.SourceObservation = &observation
		target, err := workspace.ResolveObservation(ctx, source)
		requireNoError(t, err)
		if target.Outcome != nil {
			return target.Outcome
		}
		input := store.ExamAttemptWorkspaceMutation{Access: source, Operation: observation.Operation, EntryID: target.EntryID, ExpectedPath: observation.Path,
			ExpectedContentVersion: target.ExpectedContentVersion, DestinationPath: observation.DestinationPath, AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
		if observation.Operation == model.AttemptWorkspaceMutationCreateDirectory || observation.Operation == model.AttemptWorkspaceMutationCreateFile {
			input.EntryID = model.NewAttemptWorkspaceEntryID()
			input.ExpectedPath = ""
			input.DestinationPath = observation.Path
		}
		if observation.Operation == model.AttemptWorkspaceMutationMoveEntry {
			input.ExpectedContentVersion = ""
		}
		if observation.Recursive != nil && *observation.Recursive {
			input.Recursive = true
			input.ExpectedWorkspaceCursor = &target.WorkspaceCursor
		}
		if observation.Content != nil {
			object, err := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: source, ObjectID: model.NewAttemptWorkspaceObjectID()})
			requireNoError(t, err)
			object, err = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: source, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: observation.Content.Size, SHA256: observation.Content.SHA256}})
			requireNoError(t, err)
			input.ObjectID = object.ID
		}
		key := fmt.Sprintf("host-observation-%d", observation.HostSequence)
		command := examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, key, key)
		result, err := workspace.ApplyMutation(ctx, &input, command)
		requireNoError(t, err)
		lastInput, lastCommand = input, command
		current, err := ss.ExecutionGrant().Current(ctx, connected.Attempt.ID)
		requireNoError(t, err)
		if current.ProcessedHostSequence != observation.HostSequence {
			t.Fatal("Workspace commit lost host sequence")
		}
		return result
	}
	content := func(seed string) *store.ExecutionProjectionContent {
		return &store.ExecutionProjectionContent{TransferID: model.NewId(), Size: 4, SHA256: strings.Repeat(seed, 64)}
	}
	apply(event(1, model.AttemptWorkspaceMutationCreateDirectory, model.StarterWorkspaceEntryDirectory, "directory", "guest"))
	create := event(2, model.AttemptWorkspaceMutationCreateFile, model.StarterWorkspaceEntryFile, "file", "guest/a.txt")
	create.Content = content("a")
	created := apply(create)
	// An unrelated Desktop edit may advance the aggregate cursor without changing
	// this guest node's original baseline.
	_, err = workspace.ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: "unrelated", AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, "unrelated", "unrelated"))
	requireNoError(t, err)
	write := event(3, model.AttemptWorkspaceMutationReplaceFile, model.StarterWorkspaceEntryFile, "file", "guest/a.txt")
	write.Content = content("b")
	written := apply(write)
	if written.Change.EntryID != created.Change.EntryID || written.Change.ContentVersion == created.Change.ContentVersion {
		t.Fatal("guest identity/version was not preserved")
	}
	source := lastInput.Access
	retained, err := workspace.ResolveObservation(ctx, source)
	requireNoError(t, err)
	if retained.Outcome == nil || retained.Outcome.Change.Cursor != written.Change.Cursor || retained.ExpectedContentVersion != created.Change.ContentVersion {
		t.Fatal("retained exact outcome lost its original precondition")
	}
	lastInput.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
	lastInput.AuditAt = model.GetMillis()
	replay, err := workspace.ApplyMutation(ctx, &lastInput, lastCommand)
	requireNoError(t, err)
	if !replay.Replayed || replay.Change.Cursor != written.Change.Cursor {
		t.Fatal("exact command replay repeated guest mutation")
	}
	changed := write
	changed.Content = content("c")
	source.SourceObservation = &changed
	if _, err := workspace.ResolveObservation(ctx, source); !store.IsConflict(err) {
		t.Fatalf("changed observation sequence: %v", err)
	}
	move := event(4, model.AttemptWorkspaceMutationMoveEntry, model.StarterWorkspaceEntryDirectory, "directory", "guest")
	move.DestinationPath = "moved"
	apply(move)
	write = event(5, model.AttemptWorkspaceMutationReplaceFile, model.StarterWorkspaceEntryFile, "file", "moved/a.txt")
	write.Content = content("c")
	rewritten := apply(write)
	if rewritten.Change.EntryID != created.Change.EntryID {
		t.Fatal("directory move lost child identity")
	}
	apply(event(6, model.AttemptWorkspaceMutationDeleteEntry, model.StarterWorkspaceEntryFile, "file", "moved/a.txt"))
	create = event(7, model.AttemptWorkspaceMutationCreateFile, model.StarterWorkspaceEntryFile, "second-file", "moved/b.txt")
	create.Content = content("d")
	apply(create)
	recursive := true
	remove := event(8, model.AttemptWorkspaceMutationDeleteEntry, model.StarterWorkspaceEntryDirectory, "directory", "moved")
	remove.Recursive = &recursive
	apply(remove)
	// A host cannot adopt a later competing Desktop directory as though it were
	// present at the original baseline, even if its path currently exists.
	competing := event(9, model.AttemptWorkspaceMutationMoveEntry, model.StarterWorkspaceEntryDirectory, "unknown", "unrelated")
	competing.DestinationPath = "overwrite"
	source = access
	source.SourceGrantID = grant.ID
	source.SourceObservation = &competing
	if _, err := workspace.ResolveObservation(ctx, source); !store.IsConflict(err) {
		t.Fatalf("competing Desktop topology: %v", err)
	}
	gap := event(10, model.AttemptWorkspaceMutationCreateDirectory, model.StarterWorkspaceEntryDirectory, "gap", "gap")
	source.SourceObservation = &gap
	if _, err := workspace.ResolveObservation(ctx, source); !store.IsConflict(err) {
		t.Fatalf("host sequence gap: %v", err)
	}
	wrong := event(9, model.AttemptWorkspaceMutationCreateDirectory, model.StarterWorkspaceEntryDirectory, "wrong", "wrong")
	wrong.Fence.EnvironmentEpoch = "different"
	source.SourceObservation = &wrong
	if _, err := workspace.ResolveObservation(ctx, source); !store.IsConflict(err) {
		t.Fatalf("obsolete epoch: %v", err)
	}
	source.SourceObservation = nil
	if _, err := workspace.ResolveMutationTarget(ctx, source); !store.IsConflict(err) {
		t.Fatalf("bound grant accepted unfenced legacy observation: %v", err)
	}
	ignored := event(9, model.AttemptWorkspaceMutationCreateDirectory, model.StarterWorkspaceEntryDirectory, "ignored", "node_modules")
	source.SourceObservation = &ignored
	target, err := workspace.ResolveObservation(ctx, source)
	requireNoError(t, err)
	if !target.Ignored || target.Processed {
		t.Fatal("ignored event was mistaken for a Workspace mutation")
	}
	target, err = workspace.RecordIgnoredObservation(ctx, source)
	requireNoError(t, err)
	if !target.Ignored || !target.Processed || target.Outcome != nil {
		t.Fatal("ignored event fabricated Workspace state")
	}
	beforeRetry, err := ss.ExecutionGrant().Current(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	_, err = workspace.RecordIgnoredObservation(ctx, source)
	requireNoError(t, err)
	afterRetry, err := ss.ExecutionGrant().Current(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	if beforeRetry.Revision != afterRetry.Revision || afterRetry.ProcessedHostSequence != 9 {
		t.Fatal("ignored replay advanced twice")
	}
	boundary := event(10, model.AttemptWorkspaceMutationMoveEntry, model.StarterWorkspaceEntryDirectory, "ignored", "node_modules")
	boundary.DestinationPath = "visible"
	source.SourceObservation = &boundary
	if _, err := workspace.RecordIgnoredObservation(ctx, source); !store.IsConflict(err) {
		t.Fatalf("ignored boundary move partially acknowledged: %v", err)
	}

}
