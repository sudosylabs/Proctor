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
	"strings"
	"testing"
)

type ExecutionProjectionSQLProbe struct {
	ExpireObjectProtection func(*testing.T, context.Context, model.ExamAttemptWorkspaceID)
	RemoveJournalPosition  func(*testing.T, context.Context, model.ExamAttemptWorkspaceID, int64)
}

func TestExecutionProjectionStore(t *testing.T, ss store.Store, probe ExecutionProjectionSQLProbe) {
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
	ready := func(label string) *model.AttemptWorkspaceObject {
		t.Helper()
		object, err := workspace.ReserveObject(ctx, &store.ExamAttemptWorkspaceObjectReservation{Access: access, ObjectID: model.NewAttemptWorkspaceObjectID()})
		requireNoError(t, err)
		object, err = workspace.MarkObjectReady(ctx, &store.ExamAttemptWorkspaceObjectReady{Access: access, ObjectID: object.ID, ContentVersion: model.NewWorkspaceContentVersion(), Content: model.AttemptWorkspaceContent{MediaType: "text/plain", SizeBytes: int64(len(label)), SHA256: strings.Repeat("a", 64)}})
		requireNoError(t, err)
		return object
	}
	apply := func(label string, mutation store.ExamAttemptWorkspaceMutation) *store.ExamAttemptWorkspaceMutationResult {
		t.Helper()
		mutation.Access = access
		mutation.AuditEventID = saveExamAttemptAudit(t, ctx, ss, fixture).ID.String()
		mutation.AuditAt = model.GetMillis()
		value, err := workspace.ApplyMutation(ctx, &mutation, examCommand(fixture.candidate.ID, store.ExamAttemptWorkspaceMutationOperation, label, label))
		requireNoError(t, err)
		return value
	}
	empty, err := ss.ExecutionGrant().WorkspaceChanges(ctx, control.Fence(), 128)
	requireNoError(t, err)
	if len(empty.Changes) != 0 || empty.RefreshRequired || empty.HasMore {
		t.Fatalf("initial page: %#v", empty)
	}
	first, second := ready("original"), ready("later bytes")
	entryID := model.NewAttemptWorkspaceEntryID()
	apply("projection-create", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationCreateFile, EntryID: entryID, DestinationPath: "a.txt", ObjectID: first.ID})
	apply("projection-write", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationReplaceFile, EntryID: entryID, ExpectedPath: "a.txt", ExpectedContentVersion: first.ContentVersion, ObjectID: second.ID})
	apply("projection-move", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationMoveEntry, EntryID: entryID, ExpectedPath: "a.txt", DestinationPath: "b.txt"})
	apply("projection-delete", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationDeleteEntry, EntryID: entryID, ExpectedPath: "b.txt", ExpectedContentVersion: second.ContentVersion})
	directoryID := model.NewAttemptWorkspaceEntryID()
	created := apply("projection-directory", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationCreateDirectory, EntryID: directoryID, DestinationPath: "tree"})
	apply("projection-recursive", store.ExamAttemptWorkspaceMutation{Operation: model.AttemptWorkspaceMutationDeleteEntry, EntryID: directoryID, ExpectedPath: "tree", Recursive: true, ExpectedWorkspaceCursor: &created.Change.Cursor})
	page, err := ss.ExecutionGrant().WorkspaceChanges(ctx, control.Fence(), 128)
	requireNoError(t, err)
	if page.RefreshRequired || page.HasMore || len(page.Changes) != 6 || page.CurrentCursor != 6 {
		t.Fatalf("page: %#v", page)
	}
	for i, change := range page.Changes {
		if change.Change.Cursor != int64(i+1) || change.SourceGrantID.IsValid() {
			t.Fatal("journal order/origin changed")
		}
	}
	if page.Changes[0].Content == nil || page.Changes[0].Content.AttemptObjectID != first.ID || page.Changes[0].Content.ContentVersion != first.ContentVersion || page.Changes[0].ExpectedContentVersion.IsValid() {
		t.Fatal("creation read a later live file version")
	}
	if page.Changes[1].Content == nil || page.Changes[1].Content.AttemptObjectID != second.ID || page.Changes[1].ExpectedContentVersion != first.ContentVersion || page.Changes[1].Change.ContentVersion != second.ContentVersion {
		t.Fatal("replacement lost before/after versions")
	}
	if page.Changes[2].ExpectedContentVersion != second.ContentVersion || page.Changes[2].Content != nil || page.Changes[3].ExpectedContentVersion != second.ContentVersion || page.Changes[3].Content != nil || !page.Changes[5].Change.Recursive {
		t.Fatal("move/delete topology metadata changed")
	}
	short, err := ss.ExecutionGrant().WorkspaceChanges(ctx, control.Fence(), 1)
	requireNoError(t, err)
	if !short.HasMore || len(short.Changes) != 1 || short.RefreshRequired {
		t.Fatal("page bound ignored")
	}
	wrong := control.Fence()
	wrong.EnvironmentEpoch = "wrong"
	if _, err := ss.ExecutionGrant().WorkspaceChanges(ctx, wrong, 1); !store.IsConflict(err) {
		t.Fatalf("foreign epoch read: %v", err)
	}
	if probe.ExpireObjectProtection != nil {
		probe.ExpireObjectProtection(t, ctx, connected.Workspace.ID)
		claimed, err := workspace.ClaimObjectsForCleanup(ctx, 10, "projection-cleanup")
		requireNoError(t, err)
		if len(claimed) != 0 {
			t.Fatal("unapplied active projection lost its exact saved content")
		}
	}
	if probe.RemoveJournalPosition != nil {
		probe.RemoveJournalPosition(t, ctx, connected.Workspace.ID, 3)
		page, err = ss.ExecutionGrant().WorkspaceChanges(ctx, control.Fence(), 128)
		requireNoError(t, err)
		if !page.RefreshRequired || len(page.Changes) != 0 || page.HasMore {
			t.Fatalf("gap returned partial projection: %#v", page)
		}
	}
	_, err = ss.ExecutionGrant().ReleaseGrant(ctx, grant.ID, model.NowUTC())
	requireNoError(t, err)
	if probe.ExpireObjectProtection != nil {
		claimed, err := workspace.ClaimObjectsForCleanup(ctx, 10, "retired-projection-cleanup")
		requireNoError(t, err)
		if len(claimed) != 2 {
			t.Fatalf("released grant kept old bodies pinned: %d", len(claimed))
		}
	}
}
