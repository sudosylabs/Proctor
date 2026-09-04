// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestJobStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	at := model.NowUTC().Add(-time.Minute)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), "user:first", at, at, 3)
	requireNoError(t, err)
	created, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: job})
	requireNoError(t, err)
	if !inserted || created.ID != job.ID {
		t.Fatalf("Enqueue() = %#v, %v", created, inserted)
	}
	duplicate, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: mustJob(t, "user:first", at)})
	requireNoError(t, err)
	if inserted || duplicate.ID != job.ID {
		t.Fatalf("deduplicated Enqueue() = %#v, %v", duplicate, inserted)
	}

	if _, err = ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeCleanup}, NodeID: "node-a", ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute}); !store.IsNotFound(err) {
		t.Fatalf("ClaimNext(other type) error = %v", err)
	}
	firstToken := mustClaimToken(t)
	claim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "node-a", ClaimToken: firstToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if claim.Job.Status != model.JobStatusRunning || claim.Job.AttemptCount != 1 || claim.Attempt.Number != 1 || claim.Attempt.ClaimToken != firstToken {
		t.Fatalf("ClaimNext() = %#v", claim)
	}
	reservation, err := ss.Job().ReserveWork(ctx, &store.JobWorkReservation{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Units: 2, Limit: 3})
	requireNoError(t, err)
	if !reservation.Reserved || reservation.Consumed != 2 {
		t.Fatalf("ReserveWork(2 of 3) = %#v", reservation)
	}
	reservation, err = ss.Job().ReserveWork(ctx, &store.JobWorkReservation{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Units: 2, Limit: 3})
	requireNoError(t, err)
	if reservation.Reserved || reservation.Consumed != 2 {
		t.Fatalf("ReserveWork(over budget) = %#v", reservation)
	}
	if _, err = ss.Job().Heartbeat(ctx, &store.JobHeartbeat{AttemptID: claim.Attempt.ID, ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute}); !store.IsConflict(err) {
		t.Fatalf("Heartbeat(stale token) error = %v", err)
	}
	if _, err = ss.Job().Heartbeat(ctx, &store.JobHeartbeat{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, LeaseDuration: time.Minute}); err != nil {
		t.Fatal(err)
	}
	checkpointed, err := ss.Job().Checkpoint(ctx, &store.JobCheckpoint{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Progress: &model.JobProgress{Current: 1, Total: 3, Stage: "rendering"}, CheckpointVersion: 1, Checkpoint: json.RawMessage(`{"completed":1}`)})
	requireNoError(t, err)
	if checkpointed.Progress.Current != 1 || checkpointed.CheckpointVersion != 1 || string(checkpointed.Checkpoint) != `{"completed":1}` {
		t.Fatalf("Checkpoint() = %#v", checkpointed)
	}
	retried, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Kind: store.JobCompletionRetryableFailure, RetryDelay: time.Millisecond, PublicErrorCode: "dependency.unavailable"})
	requireNoError(t, err)
	if retried.Status != model.JobStatusQueued || !retried.AvailableAt.After(retried.UpdatedAt) {
		t.Fatalf("retry completion = %#v", retried)
	}
	secondToken := mustClaimToken(t)
	time.Sleep(3 * time.Millisecond)
	second, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{job.Type}, NodeID: "node-b", ClaimToken: secondToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if second.Attempt.Number != 2 {
		t.Fatalf("second attempt = %#v", second.Attempt)
	}
	if second.Job.WorkReserved != 2 {
		t.Fatalf("reserved work did not survive retry: %#v", second.Job)
	}
	if _, err = ss.Job().ReserveWork(ctx, &store.JobWorkReservation{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Units: 1, Limit: 3}); !store.IsConflict(err) {
		t.Fatalf("ReserveWork(stale attempt) error = %v", err)
	}
	reservation, err = ss.Job().ReserveWork(ctx, &store.JobWorkReservation{AttemptID: second.Attempt.ID, ClaimToken: secondToken, Units: 1, Limit: 3})
	requireNoError(t, err)
	if !reservation.Reserved || reservation.Consumed != 3 {
		t.Fatalf("ReserveWork(after retry) = %#v", reservation)
	}
	reservation, err = ss.Job().ReserveWork(ctx, &store.JobWorkReservation{AttemptID: second.Attempt.ID, ClaimToken: secondToken, Units: 1, Limit: 3})
	requireNoError(t, err)
	if reservation.Reserved || reservation.Consumed != 3 {
		t.Fatalf("ReserveWork(exhausted retry) = %#v", reservation)
	}
	if _, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: claim.Attempt.ID, ClaimToken: firstToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}); !store.IsConflict(err) {
		t.Fatalf("Complete(stale worker) error = %v", err)
	}
	succeeded, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: second.Attempt.ID, ClaimToken: secondToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{"file_entry_id":"done"}`)})
	requireNoError(t, err)
	if succeeded.Status != model.JobStatusSucceeded || !succeeded.CompletedAt.Valid {
		t.Fatalf("success completion = %#v", succeeded)
	}
	attempts, err := ss.Job().ListAttempts(ctx, job.ID)
	requireNoError(t, err)
	if len(attempts) != 2 || attempts[0].Status != model.JobAttemptStatusFailed || attempts[1].Status != model.JobAttemptStatusSucceeded {
		t.Fatalf("ListAttempts() = %#v", attempts)
	}

	relinquishable := mustJob(t, "worker:incompatible", model.NowUTC().Add(-time.Minute))
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: relinquishable})
	requireNoError(t, err)
	relinquishToken := mustClaimToken(t)
	relinquishClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{relinquishable.Type}, NodeID: "old-primary-node", ClaimToken: relinquishToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	relinquished, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: relinquishClaim.Attempt.ID, ClaimToken: relinquishToken, Kind: store.JobCompletionRelinquished, RetryDelay: time.Millisecond, PublicErrorCode: "worker.capability_mismatch"})
	requireNoError(t, err)
	if relinquished.Status != model.JobStatusQueued || relinquished.AttemptCount != 1 ||
		relinquished.MaximumAttempts != relinquishable.MaximumAttempts+1 ||
		relinquished.MaximumAttempts-relinquished.AttemptCount != relinquishable.MaximumAttempts {
		t.Fatalf("relinquished completion = %#v", relinquished)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err = ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{relinquishable.Type}, NodeID: "old-primary-node", ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute}); !store.IsNotFound(err) {
		t.Fatalf("incompatible node reclaimed relinquished Job: %v", err)
	}
	compatibleToken := mustClaimToken(t)
	compatibleClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{relinquishable.Type}, NodeID: "new-primary-node", ClaimToken: compatibleToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if compatibleClaim.Job.ID != relinquishable.ID {
		t.Fatalf("compatible node claimed Job %s, want %s", compatibleClaim.Job.ID, relinquishable.ID)
	}
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: compatibleClaim.Attempt.ID, ClaimToken: compatibleToken,
		Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	requireNoError(t, err)
	incompatibleAttempts, err := ss.Job().ListAttempts(ctx, relinquishable.ID)
	requireNoError(t, err)
	if len(incompatibleAttempts) != 2 || incompatibleAttempts[0].Status != model.JobAttemptStatusIncompatible ||
		incompatibleAttempts[1].Status != model.JobAttemptStatusSucceeded {
		t.Fatalf("relinquished attempt history = %#v", incompatibleAttempts)
	}

	crashed := mustJob(t, "user:crashed", model.NowUTC().Add(-time.Minute))
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: crashed})
	requireNoError(t, err)
	deadToken := mustClaimToken(t)
	dead, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{crashed.Type}, NodeID: "dead-node", ClaimToken: deadToken, LeaseDuration: 10 * time.Millisecond})
	requireNoError(t, err)
	time.Sleep(20 * time.Millisecond)
	recoveredToken := mustClaimToken(t)
	recovered, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{crashed.Type}, NodeID: "live-node", ClaimToken: recoveredToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	if recovered.Job.ID != crashed.ID || recovered.Attempt.Number != 2 {
		t.Fatalf("recovered claim = %#v", recovered)
	}
	if _, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: dead.Attempt.ID, ClaimToken: deadToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)}); !store.IsConflict(err) {
		t.Fatalf("expired worker completion error = %v", err)
	}
	attempts, err = ss.Job().ListAttempts(ctx, crashed.ID)
	requireNoError(t, err)
	if attempts[0].Status != model.JobAttemptStatusLeaseExpired {
		t.Fatalf("expired attempt = %#v", attempts[0])
	}
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: recovered.Attempt.ID, ClaimToken: recoveredToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	requireNoError(t, err)

	exhaustedAt := model.NowUTC().Add(-time.Minute)
	exhausted, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), "user:exhausted", exhaustedAt, exhaustedAt, 1)
	requireNoError(t, err)
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: exhausted})
	requireNoError(t, err)
	_, err = ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{exhausted.Type}, NodeID: "last-node", ClaimToken: mustClaimToken(t), LeaseDuration: 10 * time.Millisecond})
	requireNoError(t, err)
	time.Sleep(20 * time.Millisecond)
	if _, err = ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{exhausted.Type}, NodeID: "next-node", ClaimToken: mustClaimToken(t), LeaseDuration: time.Minute}); !store.IsNotFound(err) {
		t.Fatalf("ClaimNext(exhausted) error = %v", err)
	}
	exhausted, err = ss.Job().Get(ctx, exhausted.ID)
	requireNoError(t, err)
	if exhausted.Status != model.JobStatusFailed || exhausted.PublicErrorCode != "job.lease_expired" || !exhausted.CompletedAt.Valid {
		t.Fatalf("exhausted crash = %#v", exhausted)
	}
	repairAt := model.NowUTC().Add(-time.Second)
	repair := mustJob(t, "user:exhausted", repairAt)
	createdRepair, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: repair})
	requireNoError(t, err)
	if !inserted || createdRepair.ID != repair.ID {
		t.Fatalf("terminal repair Enqueue() = %#v, %v", createdRepair, inserted)
	}
	duplicateRepair, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: mustJob(t, "user:exhausted", repairAt)})
	requireNoError(t, err)
	if inserted || duplicateRepair.ID != repair.ID {
		t.Fatalf("active repaired-job dedupe = %#v, %v", duplicateRepair, inserted)
	}
	repairToken := mustClaimToken(t)
	repairClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{repair.Type}, NodeID: "repair-node", ClaimToken: repairToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: repairClaim.Attempt.ID, ClaimToken: repairToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	requireNoError(t, err)

	permanentAt := model.NowUTC().Add(-time.Second)
	permanent := mustPermanentJob(t, "cleanup:2026-08-10", permanentAt)
	_, inserted, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: permanent})
	requireNoError(t, err)
	if !inserted {
		t.Fatal("first permanent occurrence was deduplicated")
	}
	permanentToken := mustClaimToken(t)
	permanentClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{model.JobTypeCleanup}, NodeID: "cleanup-node", ClaimToken: permanentToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	terminalPermanent, err := ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: permanentClaim.Attempt.ID, ClaimToken: permanentToken, Kind: store.JobCompletionSucceeded, ResultVersion: 1, Result: json.RawMessage(`{}`)})
	requireNoError(t, err)
	duplicatePermanent, inserted, err := ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: mustPermanentJob(t, "cleanup:2026-08-10", permanentAt)})
	requireNoError(t, err)
	if inserted || duplicatePermanent.ID != terminalPermanent.ID || duplicatePermanent.Status != model.JobStatusSucceeded {
		t.Fatalf("terminal permanent-occurrence dedupe = %#v, %v", duplicatePermanent, inserted)
	}

	listed, err := ss.Job().List(ctx, store.JobListOptions{Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, Limit: 2})
	requireNoError(t, err)
	if len(listed) != 2 || listed[0].CreatedAt.Before(listed[1].CreatedAt) {
		t.Fatalf("keyset List() = %#v", listed)
	}
	nextPage, err := ss.Job().List(ctx, store.JobListOptions{Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, BeforeCreatedAt: listed[1].CreatedAt, BeforeID: listed[1].ID, Limit: 20})
	requireNoError(t, err)
	for _, item := range nextPage {
		if item.ID == listed[0].ID || item.ID == listed[1].ID {
			t.Fatalf("keyset repeated item %s", item.ID)
		}
	}

	control := mustJob(t, "user:control", model.NowUTC().Add(-time.Second))
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: control})
	requireNoError(t, err)
	controlToken := mustClaimToken(t)
	controlClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{control.Type}, NodeID: "control-node", ClaimToken: controlToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	audit := saveJobMutationAudit(t, ctx, ss, control.ID, "cancel")
	cancelRequested, err := ss.Job().CancelWithAudit(ctx, &store.JobMutation{ID: control.ID, ExpectedRevision: controlClaim.Job.Revision, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	if cancelRequested.Status != model.JobStatusCancelRequested {
		t.Fatalf("CancelWithAudit() = %#v", cancelRequested)
	}
	observed, err := ss.Job().CancellationRequested(ctx, controlClaim.Attempt.ID, controlToken)
	requireNoError(t, err)
	if !observed {
		t.Fatal("fenced worker did not observe cancellation")
	}
	_, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: controlClaim.Attempt.ID, ClaimToken: controlToken, Kind: store.JobCompletionCanceled, PublicErrorCode: "job.canceled"})
	requireNoError(t, err)

	failedJob := mustJob(t, "user:explicit-retry", model.NowUTC().Add(-time.Second))
	_, _, err = ss.Job().Enqueue(ctx, &store.JobEnqueue{Job: failedJob})
	requireNoError(t, err)
	failedToken := mustClaimToken(t)
	failedClaim, err := ss.Job().ClaimNext(ctx, &store.JobClaimRequest{Types: []model.JobType{failedJob.Type}, NodeID: "retry-node", ClaimToken: failedToken, LeaseDuration: time.Minute})
	requireNoError(t, err)
	failedJob, err = ss.Job().Complete(ctx, &store.JobCompletion{AttemptID: failedClaim.Attempt.ID, ClaimToken: failedToken, Kind: store.JobCompletionPermanentFailure, PublicErrorCode: "job.failed"})
	requireNoError(t, err)
	beforeAttempts, err := ss.Job().ListAttempts(ctx, failedJob.ID)
	requireNoError(t, err)
	audit = saveJobMutationAudit(t, ctx, ss, failedJob.ID, "retry")
	requeued, err := ss.Job().RetryWithAudit(ctx, &store.JobMutation{ID: failedJob.ID, ExpectedRevision: failedJob.Revision, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis()})
	requireNoError(t, err)
	afterAttempts, err := ss.Job().ListAttempts(ctx, failedJob.ID)
	requireNoError(t, err)
	if requeued.Status != model.JobStatusQueued || len(afterAttempts) != len(beforeAttempts) {
		t.Fatalf("RetryWithAudit() = %#v, attempts %d -> %d", requeued, len(beforeAttempts), len(afterAttempts))
	}
}

func saveJobMutationAudit(t *testing.T, ctx context.Context, ss store.Store, jobID model.JobID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionJobManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: model.NewId()}, ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(), Status: model.AuditStatusAttempt, NodeID: "test-node", Parameters: []byte(`{"operation":"` + operation + `","job_id":"` + jobID.String() + `"}`)})
	requireNoError(t, err)
	return event
}

func mustJob(t *testing.T, dedupe string, at time.Time) *model.Job {
	t.Helper()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), dedupe, at, at, 3)
	requireNoError(t, err)
	return job
}

func mustPermanentJob(t *testing.T, dedupe string, at time.Time) *model.Job {
	t.Helper()
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{}`), dedupe, model.JobDedupePermanent, at, at, 3)
	requireNoError(t, err)
	return job
}

func mustClaimToken(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	requireNoError(t, err)
	return token
}
