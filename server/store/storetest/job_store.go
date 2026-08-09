// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
}

func mustJob(t *testing.T, dedupe string, at time.Time) *model.Job {
	t.Helper()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"`+model.NewUserID().String()+`"}`), dedupe, at, at, 3)
	requireNoError(t, err)
	return job
}

func mustClaimToken(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	requireNoError(t, err)
	return token
}
