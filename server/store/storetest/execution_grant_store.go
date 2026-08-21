// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExecutionGrantStore(t *testing.T, ss store.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := newExamAttemptFixture(t, ctx, ss)
	input := &store.ExamAttemptConnect{
		SittingID: fixture.sitting.ID, CandidateUserID: fixture.candidate.ID, SessionID: fixture.session.ID,
		AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
		ParticipationID: model.NewAttemptParticipationID(), ConnectionID: model.NewAttemptConnectionID(),
		ContinuityCredentialHash: model.HashToken(model.NewCredentialToken()),
		AuditEventID:             saveExamAttemptAudit(t, ctx, ss, fixture).ID.String(), AuditAt: model.GetMillis(),
	}
	connected, err := ss.ExamAttempt().Connect(ctx, input,
		examCommand(fixture.candidate.ID, store.ExamAttemptConnectOperation, "execution-grant-attempt", "execution-grant-attempt"))
	requireNoError(t, err)

	snapshot, err := ss.ExecutionGrant().WorkspaceSnapshot(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	if snapshot.Cursor != connected.Workspace.Cursor || len(snapshot.Nodes) != 2 ||
		snapshot.Nodes[0].Kind != model.StarterWorkspaceEntryDirectory || snapshot.Nodes[0].Path != "cmd" ||
		snapshot.Nodes[1].Kind != model.StarterWorkspaceEntryFile || snapshot.Nodes[1].Path != "cmd/main.go" ||
		snapshot.Nodes[1].StorageOrigin != model.AttemptWorkspaceStorageStarter ||
		!snapshot.Nodes[1].StarterObjectID.IsValid() || !snapshot.Nodes[1].AttemptObjectID.IsValid() ||
		!snapshot.Nodes[1].ContentVersion.IsValid() || snapshot.Nodes[1].SizeBytes != 13 {
		t.Fatalf("WorkspaceSnapshot() = %#v", snapshot)
	}

	now := model.NowUTC()
	reservation := store.ExecutionGrantReservation{
		ID: model.NewExecutionGrantID(), AttemptID: connected.Attempt.ID, HostID: "runner-a",
		Image: "go-1.25", Network: model.ExecutionNetworkNone, At: now,
	}
	reserved, err := ss.ExecutionGrant().Reserve(ctx, reservation)
	requireNoError(t, err)
	if reserved.ID != reservation.ID || reserved.State != model.ExecutionGrantReserved || reserved.Revision != 1 {
		t.Fatalf("Reserve() = %#v", reserved)
	}

	idempotent := reservation
	idempotent.ID = model.NewExecutionGrantID()
	replayed, err := ss.ExecutionGrant().Reserve(ctx, idempotent)
	requireNoError(t, err)
	if replayed.ID != reserved.ID || replayed.Revision != reserved.Revision {
		t.Fatalf("Reserve(idempotent) = %#v, want %#v", replayed, reserved)
	}
	conflicting := reservation
	conflicting.ID, conflicting.HostID = model.NewExecutionGrantID(), "runner-b"
	if _, err = ss.ExecutionGrant().Reserve(ctx, conflicting); !store.IsConflict(err) {
		t.Fatalf("Reserve(conflicting) error = %v, want conflict", err)
	}

	if _, err = ss.ExecutionGrant().MarkReady(ctx, reserved.ID, reserved.Revision+1, now.Add(time.Millisecond)); !store.IsConflict(err) {
		t.Fatalf("MarkReady(stale) error = %v, want conflict", err)
	}
	ready, err := ss.ExecutionGrant().MarkReady(ctx, reserved.ID, reserved.Revision, now.Add(2*time.Millisecond))
	requireNoError(t, err)
	if ready.State != model.ExecutionGrantReady || ready.Revision != reserved.Revision+1 {
		t.Fatalf("MarkReady() = %#v", ready)
	}
	current, err := ss.ExecutionGrant().Current(ctx, connected.Attempt.ID)
	requireNoError(t, err)
	if current.ID != ready.ID || current.Revision != ready.Revision {
		t.Fatalf("Current() = %#v, want %#v", current, ready)
	}
	convergence, err := ss.ExecutionGrant().ListCurrentForReconciliation(ctx, model.ExecutionGrantID(""), 10)
	requireNoError(t, err)
	if len(convergence) != 1 || convergence[0].Grant.ID != ready.ID ||
		convergence[0].AttemptState != model.ExamAttemptActive || convergence[0].SittingState != model.ExamSittingOpen ||
		convergence[0].SittingRevision < 1 || convergence[0].Grant.AppliedSittingState != model.ExamSittingOpen ||
		convergence[0].Grant.AppliedSittingRevision != convergence[0].SittingRevision {
		t.Fatalf("ListCurrentForReconciliation() = %#v", convergence)
	}
	lease, err := ss.ExecutionGrant().AcquireLifecycleLease(ctx, ready.ID)
	requireNoError(t, err)
	leased, err := ss.ExecutionGrant().CurrentForReconciliation(ctx, ready.ID)
	requireNoError(t, err)
	if leased.Grant.ID != ready.ID || leased.SittingRevision != convergence[0].SittingRevision {
		t.Fatalf("CurrentForReconciliation() = %#v", leased)
	}
	requireNoError(t, lease.Validate(ctx))
	requireNoError(t, lease.Release(ctx))
	if _, err = ss.ExecutionGrant().MarkSittingStateApplied(ctx, ready.ID, ready.Revision, model.ExamSittingOpen,
		convergence[0].SittingRevision+1, now.Add(2400*time.Microsecond)); !store.IsConflict(err) {
		t.Fatalf("MarkSittingStateApplied(stale) error = %v, want conflict", err)
	}
	prepared, err := ss.ExecutionGrant().PrepareSittingStateEffect(ctx, ready.ID, ready.Revision, model.ExamSittingOpen,
		convergence[0].SittingRevision, now.Add(2450*time.Microsecond))
	requireNoError(t, err)
	if !prepared.LifecyclePending || prepared.PendingSittingState != model.ExamSittingOpen ||
		prepared.PendingSittingRevision != convergence[0].SittingRevision {
		t.Fatalf("PrepareSittingStateEffect() = %#v", prepared)
	}
	ready, err = ss.ExecutionGrant().MarkSittingStateApplied(ctx, ready.ID, prepared.Revision, model.ExamSittingOpen,
		convergence[0].SittingRevision, now.Add(2500*time.Microsecond))
	requireNoError(t, err)
	if ready.AppliedSittingState != model.ExamSittingOpen || ready.AppliedSittingRevision != convergence[0].SittingRevision {
		t.Fatalf("MarkSittingStateApplied() = %#v", ready)
	}

	reassigned, err := ss.ExecutionGrant().Reassign(ctx, store.ExecutionGrantReassignment{
		CurrentID: ready.ID, CurrentRevision: ready.Revision,
		Replacement: store.ExecutionGrantReservation{
			ID: model.NewExecutionGrantID(), AttemptID: connected.Attempt.ID, HostID: "runner-b",
			Image: reservation.Image, Network: reservation.Network, At: now.Add(3 * time.Millisecond),
		},
	})
	requireNoError(t, err)
	if reassigned.Previous.State != model.ExecutionGrantReleased || !reassigned.Previous.ReleasedAt.Valid ||
		reassigned.Current.State != model.ExecutionGrantReserved || reassigned.Current.HostID != "runner-b" {
		t.Fatalf("Reassign() = %#v", reassigned)
	}
	pending, err := ss.ExecutionGrant().ListPendingRevocations(ctx, 10)
	requireNoError(t, err)
	if len(pending) != 1 || pending[0].ID != reassigned.Previous.ID {
		t.Fatalf("ListPendingRevocations() = %#v", pending)
	}
	_, err = ss.ExecutionGrant().MarkRevoked(ctx, reassigned.Previous.ID, reassigned.Previous.Revision, now.Add(4*time.Millisecond))
	requireNoError(t, err)

	exactlyReleased, err := ss.ExecutionGrant().ReleaseGrant(ctx, reassigned.Current.ID, now.Add(5*time.Millisecond))
	requireNoError(t, err)
	if exactlyReleased.ID != reassigned.Current.ID || exactlyReleased.State != model.ExecutionGrantReleased || !exactlyReleased.ReleasedAt.Valid {
		t.Fatalf("ReleaseGrant() = %#v", exactlyReleased)
	}
	if _, err = ss.ExecutionGrant().Current(ctx, connected.Attempt.ID); !store.IsNotFound(err) {
		t.Fatalf("Current(released) error = %v, want not found", err)
	}
	pending, err = ss.ExecutionGrant().ListPendingRevocations(ctx, 10)
	requireNoError(t, err)
	if len(pending) != 1 || pending[0].ID != exactlyReleased.ID {
		t.Fatalf("ListPendingRevocations(after release) = %#v", pending)
	}
	_, err = ss.ExecutionGrant().MarkRevoked(ctx, exactlyReleased.ID, exactlyReleased.Revision, now.Add(6*time.Millisecond))
	requireNoError(t, err)
	last, err := ss.ExecutionGrant().Reserve(ctx, store.ExecutionGrantReservation{ID: model.NewExecutionGrantID(),
		AttemptID: connected.Attempt.ID, HostID: "runner-c", Image: reservation.Image, Network: reservation.Network,
		At: now.Add(7 * time.Millisecond)})
	requireNoError(t, err)
	released, err := ss.ExecutionGrant().Release(ctx, connected.Attempt.ID, now.Add(8*time.Millisecond))
	requireNoError(t, err)
	if released.ID != last.ID {
		t.Fatalf("Release() = %#v, want grant %s", released, last.ID)
	}
	_, err = ss.ExecutionGrant().MarkRevoked(ctx, released.ID, released.Revision, now.Add(9*time.Millisecond))
	requireNoError(t, err)
	pending, err = ss.ExecutionGrant().ListPendingRevocations(ctx, 10)
	requireNoError(t, err)
	if len(pending) != 0 {
		t.Fatalf("ListPendingRevocations(after cleanup) = %#v", pending)
	}
}
