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

func TestExecutionControlStore(t *testing.T, ss store.Store) {
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
	epoch := "original_opaque_epoch"
	initial, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, epoch, model.NowUTC())
	requireNoError(t, err)
	if initial.ControlRevision != 1 || initial.ControlAcknowledgedRevision != 0 || initial.DesiredControlState != model.ExecutionControlRunning {
		t.Fatalf("initial intent: %#v", initial)
	}
	repeated, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, epoch, model.NowUTC())
	requireNoError(t, err)
	if repeated.Revision != initial.Revision || repeated.Fence() != initial.Fence() {
		t.Fatal("uncertain effect retry changed intent")
	}
	// Independent callers retrying an uncertain intent cannot mint distinct fences.
	type retryResult struct {
		grant *model.ExecutionGrant
		err   error
	}
	retries := make(chan retryResult, 8)
	for range 8 {
		go func() {
			value, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, epoch, model.NowUTC())
			retries <- retryResult{value, err}
		}()
	}
	for range 8 {
		result := <-retries
		requireNoError(t, result.err)
		if result.grant.Fence() != initial.Fence() || result.grant.Revision != initial.Revision {
			t.Fatal("concurrent retry advanced intent")
		}
	}

	if _, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, "replacement_epoch", model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("epoch replacement: %v", err)
	}
	if _, err := ss.ExecutionGrant().AcknowledgeControl(ctx, initial.Fence(), model.ExecutionControlFrozen, model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("wrong state: %v", err)
	}
	applied, err := ss.ExecutionGrant().AcknowledgeControl(ctx, initial.Fence(), initial.DesiredControlState, model.NowUTC())
	requireNoError(t, err)
	repeated, err = ss.ExecutionGrant().AcknowledgeControl(ctx, initial.Fence(), initial.DesiredControlState, model.NowUTC())
	requireNoError(t, err)
	if repeated.Revision != applied.Revision {
		t.Fatal("completion retry wrote another revision")
	}
	// Bind the snapshot to stable logical identities before declaring it ready.
	snapshot, err := ss.ExecutionGrant().WorkspaceSnapshot(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	for _, node := range snapshot.Nodes {
		if !node.EntryID.IsValid() {
			t.Fatal("snapshot omitted stable Entry ID")
		}
	}
	prepared, err := ss.ExecutionGrant().PrepareWorkspaceEffect(ctx, grant.ID, applied.Revision, snapshot.Cursor, model.NowUTC())
	requireNoError(t, err)
	_, err = ss.ExecutionGrant().MarkWorkspaceApplied(ctx, grant.ID, prepared.Revision, snapshot.Cursor, model.NowUTC())
	requireNoError(t, err)
	recovery, err := ss.ExamAttempt().RecoverSecurityPolicy(ctx, store.SecurityPreflightAccess{CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision}, connected.Attempt.ID)
	requireNoError(t, err)
	access := store.CandidateAttemptAccess{AttemptID: connected.Attempt.ID, ConnectionID: connected.Connection.ID, CandidateUserID: connect.CandidateUserID, SessionID: connect.SessionID, DesktopRegistrationID: connect.DesktopRegistrationID, DPoPKeyThumbprint: connect.DPoPKeyThumbprint, ContinuityCredentialHash: connect.ContinuityCredentialHash}
	control := model.SecurityCoverageRenewal{ControlSequence: 9, PolicyDigest: connected.Security.Policy.Digest, SecuritySessionID: connected.Security.SecuritySessionID, StreamID: connected.Security.DeliveryStreamID, Posture: "compliant", Sources: recovery.CurrentSources, Coverage: recovery.CurrentCoverage, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
	update := func(body model.SecurityCoverageRenewal) model.SecurityCoverageResult {
		t.Helper()
		result, err := ss.ExamAttempt().UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: access, ParticipationID: connected.Participation.ID, Generation: connected.Participation.Generation, DesktopBuild: connect.DesktopBuild, DesktopCompatibilityPolicyRevision: connect.DesktopCompatibilityPolicyRevision, Coverage: body})
		requireNoError(t, err)
		return result
	}
	prepare := func() *model.ExecutionGrant {
		t.Helper()
		value, err := ss.ExecutionGrant().PrepareControl(ctx, grant.ID, epoch, model.NowUTC())
		requireNoError(t, err)
		return value
	}
	acknowledge := func(value *model.ExecutionGrant) {
		t.Helper()
		_, err := ss.ExecutionGrant().AcknowledgeControl(ctx, value.Fence(), value.DesiredControlState, model.NowUTC())
		requireNoError(t, err)
	}
	update(control)
	healthy := prepare()
	acknowledge(healthy)
	if value := update(control); value.ExecutionState != "ready" {
		t.Fatalf("confirmed running: %#v", value)
	}
	fault := control
	fault.ControlSequence = 11
	fault.Sources = append([]model.NativeSourceCoverage{}, control.Sources...)
	fault.Sources[0].SourceInstanceID = model.NewId()
	if value := update(fault); value.ExecutionState != "freeze_pending" {
		t.Fatalf("unconfirmed fault: %#v", value)
	}
	// The old acknowledgement fails even before a reconciler prepares the freeze.
	if _, err := ss.ExecutionGrant().AcknowledgeControl(ctx, healthy.Fence(), healthy.DesiredControlState, model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("ack after fault: %v", err)
	}
	frozen := prepare()
	if frozen.ControlRevision <= healthy.ControlRevision || frozen.DesiredControlState != model.ExecutionControlFrozen {
		t.Fatalf("freeze intent: %#v", frozen)
	}
	acknowledge(frozen)
	if value := update(fault); value.ExecutionState != "frozen" {
		t.Fatalf("confirmed frozen: %#v", value)
	}
	control.ControlSequence = 12
	if value := update(control); value.ExecutionState != "thaw_pending" {
		t.Fatalf("unconfirmed recovery: %#v", value)
	}
	if _, err := ss.ExecutionGrant().AcknowledgeControl(ctx, healthy.Fence(), healthy.DesiredControlState, model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("healthy/fault/healthy accepted old recovery: %v", err)
	}
	recovered := prepare()
	if recovered.ControlRevision <= frozen.ControlRevision || recovered.EnvironmentEpoch != epoch {
		t.Fatal("recovery lost monotonic fence or original occupancy")
	}
	acknowledge(recovered)
	if value := update(control); value.ExecutionState != "ready" {
		t.Fatalf("recovered running: %#v", value)
	}
	// Sitting pause is an independent gate; native recovery cannot reopen it.
	pauseAudit := saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID)
	paused, err := ss.ExamSitting().Pause(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: fixture.sitting.Revision, PrivateReason: "control pause test", ChangedAt: model.NowUTC(), AuditEventID: pauseAudit.ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, "exam.sitting.pause.v1", "control-pause", "control-pause"))
	requireNoError(t, err)
	pausedIntent := prepare()
	if pausedIntent.DesiredControlState != model.ExecutionControlFrozen {
		t.Fatal("healthy native control bypassed paused Sitting")
	}
	acknowledge(pausedIntent)
	resumeAudit := saveExamSittingAudit(t, ctx, ss, fixture.manager.ID, fixture.examID, fixture.unitID)
	_, err = ss.ExamSitting().Resume(ctx, &store.ExamSittingManagerTransition{ExamID: fixture.examID, SittingID: fixture.sitting.ID, ActorUserID: fixture.manager.ID, ExpectedRevision: paused.Value.Sitting.Revision, PrivateReason: "control recovery test", ChangedAt: model.NowUTC(), AuditEventID: resumeAudit.ID.String(), AuditAt: model.GetMillis()}, examCommand(fixture.manager.ID, "exam.sitting.resume.v1", "control-resume", "control-resume"))
	requireNoError(t, err)
	if _, err := ss.ExecutionGrant().AcknowledgeControl(ctx, recovered.Fence(), recovered.DesiredControlState, model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("Sitting ABA accepted old recovery: %v", err)
	}
	acknowledge(prepare())
	_, err = ss.ExecutionGrant().ReleaseGrant(ctx, grant.ID, model.NowUTC())
	requireNoError(t, err)
	revoked := prepare()
	if revoked.DesiredControlState != model.ExecutionControlRevoked {
		t.Fatal("released grant can still execute")
	}
	acknowledge(revoked)
	if _, err := ss.ExecutionGrant().AcknowledgeControl(ctx, recovered.Fence(), recovered.DesiredControlState, model.NowUTC()); !store.IsConflict(err) {
		t.Fatalf("revoked grant accepted recovery: %v", err)
	}
}
