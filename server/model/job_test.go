// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestJobLifecyclePreservesTypedIntentAndBoundedProgress(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"abc"}`), "user:abc", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != model.JobStatusQueued || job.AttemptCount != 0 || job.CommandVersion != 1 {
		t.Fatalf("new job = %#v", job)
	}
	running, err := job.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	progressed, err := running.UpdateProgress(&model.JobProgress{Current: 2, Total: 3, Stage: "rendering"}, 1, json.RawMessage(`{"completed":2}`), at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if progressed.Progress.Current != 2 || progressed.AttemptCount != 1 || progressed.CheckpointVersion != 1 || string(progressed.Checkpoint) != `{"completed":2}` {
		t.Fatalf("progressed job = %#v", progressed)
	}
	if _, err = progressed.UpdateProgress(&model.JobProgress{Current: 4, Total: 3, Stage: "rendering"}, 0, nil, at); err == nil {
		t.Fatal("UpdateProgress() accepted current greater than total")
	}
	finished, err := progressed.Succeed(1, json.RawMessage(`{"file_entry_id":"entry"}`), at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != model.JobStatusSucceeded || !finished.CompletedAt.Valid || finished.ResultVersion != 1 {
		t.Fatalf("finished job = %#v", finished)
	}
	if _, err = finished.Start(at.Add(4 * time.Second)); err == nil {
		t.Fatal("Start() restarted a terminal job")
	}
}

func TestJobRejectsUnboundedOrUnsupportedIntent(t *testing.T) {
	t.Parallel()
	at := time.Now()
	if _, err := model.NewJob(model.NewJobID(), model.JobType("arbitrary"), 1, json.RawMessage(`{}`), "key", at, at, 1); err == nil {
		t.Fatal("NewJob() accepted an unknown type")
	}
	if _, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 0, json.RawMessage(`{}`), "key", at, at, 1); err == nil {
		t.Fatal("NewJob() accepted an unversioned command")
	}
	if _, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"secret":"`+strings.Repeat("x", model.JobMaximumDocumentBytes)+`"}`), "key", at, at, 1); err == nil {
		t.Fatal("NewJob() accepted an unbounded command")
	}
}

func TestCredentialMailUsesItsOwnKnownJobType(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	job, err := model.NewJob(
		model.NewJobID(), model.JobTypeMailDeliverCredential, 1,
		json.RawMessage(`{"delivery_id":"01HZZZZZZZZZZZZZZZZZZZZZZZ"}`),
		"credential-mail", at, at, model.MailMaximumAttempts,
	)
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != model.JobTypeMailDeliverCredential {
		t.Fatalf("job type = %q", job.Type)
	}
}

func TestJobRequiresAnExplicitKnownDedupePolicy(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{}`), "cleanup:2026-08-10", model.JobDedupePermanent, at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	if job.DedupePolicy != model.JobDedupePermanent {
		t.Fatalf("DedupePolicy = %q", job.DedupePolicy)
	}
	if _, err = model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{}`), "cleanup:2026-08-10", model.JobDedupePolicy("unknown"), at, at, 3); err == nil {
		t.Fatal("NewJobWithDedupePolicy() accepted an unknown policy")
	}
}

func TestJobAttemptTracksFencedExecutionOutcome(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), model.NewJobID(), 2, "node-a", token, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	heartbeat, err := attempt.Heartbeat(at.Add(15*time.Second), at.Add(75*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := heartbeat.Complete(model.JobAttemptStatusFailed, "dependency.unavailable", at.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.JobAttemptStatusFailed || completed.PublicErrorCode != "dependency.unavailable" || !completed.CompletedAt.Valid {
		t.Fatalf("completed attempt = %#v", completed)
	}
	if _, err = completed.Heartbeat(at.Add(30*time.Second), at.Add(90*time.Second)); err == nil {
		t.Fatal("Heartbeat() revived a terminal attempt")
	}
}

func TestJobCancellationAndExplicitRetryLifecycle(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	queued, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, json.RawMessage(`{"user_id":"abc"}`), "cancel", at, at, 2)
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := queued.RequestCancellation(at.Add(time.Second))
	if err != nil || canceled.Status != model.JobStatusCanceled || !canceled.CompletedAt.Valid {
		t.Fatalf("queued cancellation = %#v, %v", canceled, err)
	}

	running, err := queued.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	requested, err := running.RequestCancellation(at.Add(2 * time.Second))
	if err != nil || requested.Status != model.JobStatusCancelRequested || requested.CompletedAt.Valid {
		t.Fatalf("running cancellation = %#v, %v", requested, err)
	}

	failed, err := running.Fail("job.failed", at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	retried, err := failed.ExplicitRetry(at.Add(3 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != model.JobStatusQueued || retried.CompletedAt.Valid ||
		retried.AttemptCount != failed.AttemptCount || retried.MaximumAttempts != failed.AttemptCount+1 {
		t.Fatalf("explicit retry = %#v", retried)
	}
	if _, err := queued.ExplicitRetry(at.Add(time.Second)); err == nil {
		t.Fatal("non-terminal job accepted explicit retry")
	}
}

func TestJobRelinquishPreservesTheFailureAttemptBudget(t *testing.T) {
	at := time.Now().UTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, json.RawMessage(`{"primary_key_id":"22222222222222222222222222222222","retiring_key_id":"11111111111111111111111111111111"}`), "mail-rekey:test", at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	running, err := job.Start(at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	relinquished, err := running.Relinquish("worker.capability_mismatch", at.Add(3*time.Second), at.Add(2*time.Second))

	if err != nil {
		t.Fatal(err)
	}
	if relinquished.Status != model.JobStatusQueued || relinquished.AttemptCount != 1 || relinquished.MaximumAttempts != 4 ||
		relinquished.MaximumAttempts-relinquished.AttemptCount != job.MaximumAttempts-job.AttemptCount ||
		relinquished.PublicErrorCode != "worker.capability_mismatch" {
		t.Fatalf("Relinquish() = %#v", relinquished)
	}
}
