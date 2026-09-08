// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"testing"
)

func TestExecutionProjectionEffectsStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	connect := &store.ExamAttemptConnect{SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		DesktopRegistrationID: fixture.session.DesktopRegistrationID, DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(), ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()), AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}
	prepareExamAttemptConnect(t, ctx, ss, connect)
	connected, err := ss.ExamAttempt().Connect(ctx, connect, examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "control-connect", "control-connect"))
	requireNoError(t, err)
	presentationAccess := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, CandidateUserID: fixture.candidate.ID,
		SessionID: fixture.session.ID, DesktopRegistrationID: fixture.session.DesktopRegistrationID,
		DPoPKeyThumbprint: fixture.session.DPoPKeyThumbprint, ConnectionID: connected.Connection.ID,
		ContinuityCredentialHash: connect.ContinuityCredentialHash}
	assertProjection := func(epoch string, cursor int64, state store.ExecutionProjectionState) {
		t.Helper()
		presentation, err := ss.ExamAttempt().GetCandidatePresentation(ctx, presentationAccess)
		requireNoError(t, err)
		terminal := presentation.RuntimeCapabilities.Terminal
		if (epoch == "" && terminal.EnvironmentEpoch != nil) || (epoch != "" && (terminal.EnvironmentEpoch == nil || *terminal.EnvironmentEpoch != epoch)) || terminal.AppliedWorkspaceCursor != cursor || terminal.ProjectionState != state {
			t.Fatalf("terminal projection = %#v, want epoch=%q cursor=%d state=%s", terminal, epoch, cursor, state)
		}
	}
	if connected.RuntimeCapabilities.Terminal.EnvironmentEpoch != nil || connected.RuntimeCapabilities.Terminal.ProjectionState != store.ExecutionProjectionUnavailable {
		t.Fatal("connect invented an environment before creation")
	}
	assertProjection("", 0, store.ExecutionProjectionUnavailable)
	grant, err := ss.ExecutionGrant().Reserve(ctx, store.ExecutionGrantReservation{ID: model.NewExecutionGrantID(), AttemptID: connected.Attempt.ID, HostID: "host", Image: "go", Network: model.ExecutionNetworkNone, At: model.NowUTC()})
	requireNoError(t, err)

	control, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, "epoch", model.NowUTC())
	requireNoError(t, err)
	control, err = ss.ExecutionGrant().AcknowledgeControl(ctx, control.Fence(), control.DesiredControlState, model.NowUTC())
	requireNoError(t, err)

	assertProjection("epoch", 0, store.ExecutionProjectionSynchronizing)
	grants := ss.ExecutionGrant()
	initial := store.ExecutionProjectionRequest{Fence: control.Fence(), MutationID: model.NewId(), Initial: true, ThroughWorkspaceCursor: connected.Workspace.Cursor}
	snapshot, err := grants.WorkspaceSnapshot(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	for _, node := range snapshot.Nodes {
		entry := store.ExecutionProjectionEntry{EntryID: node.EntryID, Kind: node.Kind, Path: node.Path, ContentVersion: node.ContentVersion}
		if node.Kind == model.StarterWorkspaceEntryFile {
			entry.Content = &store.ExecutionProjectionContent{TransferID: model.NewId(), Size: node.SizeBytes, SHA256: node.SHA256}
		}
		initial.Entries = append(initial.Entries, entry)
	}
	effect, err := grants.PrepareProjection(ctx, initial)
	requireNoError(t, err)
	if effect.Request == nil || effect.Receipt != nil {
		t.Fatal("initial intent was not retained")
	}
	pending, err := grants.PendingProjection(ctx, grant.ID)
	requireNoError(t, err)
	if pending.MutationID != initial.MutationID || pending.Fence != initial.Fence || !pending.Initial {
		t.Fatal("retained request changed")
	}
	changed := initial
	changed.ThroughWorkspaceCursor++
	if _, err := grants.PrepareProjection(ctx, changed); !store.IsConflict(err) {
		t.Fatalf("changed exact retry: %v", err)
	}
	changed = initial
	changed.MutationID = model.NewId()
	if _, err := grants.PrepareProjection(ctx, changed); !store.IsConflict(err) {
		t.Fatalf("second pending request: %v", err)
	}
	receipt := store.ExecutionProjectionReceipt{Fence: initial.Fence, MutationID: initial.MutationID, AppliedWorkspaceCursor: initial.ThroughWorkspaceCursor}
	wrong := receipt
	wrong.HostCursor++
	if _, err := grants.CompleteProjection(ctx, wrong); !store.IsConflict(err) {
		t.Fatalf("wrong receipt: %v", err)
	}
	completed, err := grants.CompleteProjection(ctx, receipt)
	requireNoError(t, err)
	if completed.State != model.ExecutionGrantReady || completed.WorkspacePending {
		t.Fatal("initial completion did not make projection ready")
	}
	assertProjection("epoch", initial.ThroughWorkspaceCursor, store.ExecutionProjectionReady)
	again, err := grants.CompleteProjection(ctx, receipt)
	requireNoError(t, err)
	if again.Revision != completed.Revision {
		t.Fatal("duplicate completion changed grant")
	}
	effect, err = grants.PrepareProjection(ctx, initial)
	requireNoError(t, err)
	if effect.Request != nil || effect.Receipt == nil || *effect.Receipt != receipt {
		t.Fatal("completed retry lost exact receipt")
	}
	if _, err := grants.PendingProjection(ctx, grant.ID); !store.IsNotFound(err) {
		t.Fatalf("completed payload retained: %v", err)
	}

	access := store.ExamAttemptWorkspaceMutationAccess{AttemptID: connected.Attempt.ID, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation,
		CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ConnectionID: connect.ConnectionID, ContinuityCredentialHash: connect.ContinuityCredentialHash}
	create := func(path string) {
		t.Helper()
		_, err := ss.ExamAttemptWorkspace().ApplyMutation(ctx, &store.ExamAttemptWorkspaceMutation{Access: access, Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: model.NewAttemptWorkspaceEntryID(), DestinationPath: path,
			AuditEventID: saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, path, path))
		requireNoError(t, err)
	}
	requestForPage := func() store.ExecutionProjectionRequest {
		t.Helper()
		page, err := grants.WorkspaceChanges(ctx, control.Fence(), 1)
		requireNoError(t, err)
		if len(page.Changes) != 1 {
			t.Fatal("missing change")
		}
		change := page.Changes[0]
		return store.ExecutionProjectionRequest{Fence: control.Fence(), MutationID: model.NewId(), FromWorkspaceCursor: change.Change.Cursor - 1, ThroughWorkspaceCursor: change.Change.Cursor,
			Mutations: []store.ExecutionProjectionMutation{{Change: change.Change, ExpectedContentVersion: change.ExpectedContentVersion}}}
	}
	create("one")
	request := requestForPage()
	wrongRequest := request
	wrongRequest.ExpectedHostCursor = 1
	if _, err := grants.PrepareProjection(ctx, wrongRequest); !store.IsConflict(err) {
		t.Fatalf("unprocessed host cursor: %v", err)
	}
	_, err = grants.PrepareProjection(ctx, request)
	requireNoError(t, err)
	refused, err := grants.RejectProjection(ctx, request.Fence, request.MutationID)
	requireNoError(t, err)
	if refused.WorkspacePending || refused.AppliedWorkspaceCursor != 0 {
		t.Fatal("host refusal advanced projection or retained pending intent")
	}
	if _, err := grants.PendingProjection(ctx, grant.ID); !store.IsNotFound(err) {
		t.Fatalf("refused request remained pending: %v", err)
	}
	if _, err := grants.PrepareProjection(ctx, request); !store.IsConflict(err) {
		t.Fatalf("refused mutation ID reused: %v", err)
	}
	if _, err := grants.CompleteProjection(ctx, store.ExecutionProjectionReceipt{Fence: request.Fence, MutationID: request.MutationID, AppliedWorkspaceCursor: request.ThroughWorkspaceCursor}); !store.IsConflict(err) {
		t.Fatalf("refused mutation acknowledged: %v", err)
	}
	again, err = grants.RejectProjection(ctx, request.Fence, request.MutationID)
	requireNoError(t, err)
	if again.Revision != refused.Revision {
		t.Fatal("refusal retry changed grant")
	}
	request.MutationID = model.NewId()
	_, err = grants.PrepareProjection(ctx, request)
	requireNoError(t, err)
	create("two") // A later durable commit does not invalidate this exact prefix.
	_, err = grants.PrepareProjection(ctx, request)
	requireNoError(t, err)
	receipt = store.ExecutionProjectionReceipt{Fence: request.Fence, MutationID: request.MutationID, AppliedWorkspaceCursor: request.ThroughWorkspaceCursor}
	completed, err = grants.CompleteProjection(ctx, receipt)
	requireNoError(t, err)
	if completed.AppliedWorkspaceCursor != 1 || completed.WorkspacePending {
		t.Fatal("acknowledged prefix was lost")
	}
	request = requestForPage()
	_, err = grants.PrepareProjection(ctx, request)
	requireNoError(t, err)
	_, err = grants.ReleaseGrant(ctx, grant.ID, model.NowUTC())
	requireNoError(t, err)
	if _, err := grants.PendingProjection(ctx, grant.ID); !store.IsNotFound(err) {
		t.Fatalf("released private request retained: %v", err)
	}
	if _, err := grants.CompleteProjection(ctx, store.ExecutionProjectionReceipt{Fence: request.Fence, MutationID: request.MutationID, AppliedWorkspaceCursor: request.ThroughWorkspaceCursor}); !store.IsConflict(err) {
		t.Fatalf("released completion: %v", err)
	}
}
