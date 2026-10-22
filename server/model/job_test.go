// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestJobLifecyclePreservesTypedIntentAndBoundedProgress(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{"user_id":"abc"}`), "user:abc", at, at, 8)
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, job.Status)
	require.Equal(t, 0, job.AttemptCount)
	running, err := job.Start(at.Add(time.Second))
	require.NoError(t, err)
	progressed, err := running.UpdateProgress(&model.JobProgress{Current: 2, Total: 3, Stage: "rendering"}, json.RawMessage(`{"completed":2}`), at.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(2), progressed.Progress.Current)
	require.Equal(t, 1, progressed.AttemptCount)
	require.Equal(t, `{"completed":2}`, string(progressed.Checkpoint))
	_, err = progressed.UpdateProgress(&model.JobProgress{Current: 4, Total: 3, Stage: "rendering"}, nil, at)
	require.Error(t, err, "UpdateProgress() accepted current greater than total")
	finished, err := progressed.Succeed(json.RawMessage(`{"file_entry_id":"entry"}`), at.Add(3*time.Second))
	require.NoError(t, err)
	require.Equal(t, model.JobStatusSucceeded, finished.Status)
	require.True(t, finished.CompletedAt.Valid)
	_, err = finished.Start(at.Add(4 * time.Second))
	require.Error(t, err, "Start() restarted a terminal job")
}

func TestJobRejectsUnboundedOrUnsupportedIntent(t *testing.T) {
	t.Parallel()
	at := time.Now()
	_, err := model.NewJob(model.NewJobID(), model.JobType("arbitrary"), json.RawMessage(`{}`), "key", at, at, 1)
	require.Error(t, err, "NewJob() accepted an unknown type")
	_, err = model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{"secret":"`+strings.Repeat("x", model.JobMaximumDocumentBytes)+`"}`), "key", at, at, 1)
	require.Error(t, err, "NewJob() accepted an unbounded command")
}

func TestCredentialMailUsesItsOwnKnownJobType(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	job, err := model.NewJob(
		model.NewJobID(), model.JobTypeMailDeliverCredential,
		json.RawMessage(`{"delivery_id":"01HZZZZZZZZZZZZZZZZZZZZZZZ"}`),
		"credential-mail", at, at, model.MailMaximumAttempts,
	)
	require.NoError(t, err)
	require.Equal(t, model.JobTypeMailDeliverCredential, job.Type)
}

func TestJobRequiresAnExplicitKnownDedupePolicy(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{}`), "cleanup:2026-08-10", model.JobDedupePermanent, at, at, 3)
	require.NoError(t, err)
	require.Equal(t, model.JobDedupePermanent, job.DedupePolicy)
	_, err = model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{}`), "cleanup:2026-08-10", model.JobDedupePolicy("unknown"), at, at, 3)
	require.Error(t, err, "NewJobWithDedupePolicy() accepted an unknown policy")
}

func TestJobAttemptTracksFencedExecutionOutcome(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	attempt, err := model.NewJobAttempt(model.NewJobAttemptID(), model.NewJobID(), 2, "node-a", token, at, at.Add(time.Minute))
	require.NoError(t, err)
	heartbeat, err := attempt.Heartbeat(at.Add(15*time.Second), at.Add(75*time.Second))
	require.NoError(t, err)
	completed, err := heartbeat.Complete(model.JobAttemptStatusFailed, "dependency.unavailable", at.Add(20*time.Second))
	require.NoError(t, err)
	require.Equal(t, model.JobAttemptStatusFailed, completed.Status)
	require.Equal(t, "dependency.unavailable", completed.PublicErrorCode)
	require.True(t, completed.CompletedAt.Valid)
	_, err = completed.Heartbeat(at.Add(30*time.Second), at.Add(90*time.Second))
	require.Error(t, err, "Heartbeat() revived a terminal attempt")
}

func TestJobCancellationAndExplicitRetryLifecycle(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	queued, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, json.RawMessage(`{"user_id":"abc"}`), "cancel", at, at, 2)
	require.NoError(t, err)
	canceled, err := queued.RequestCancellation(at.Add(time.Second))
	require.NoError(t, err, "queued cancellation = %#v", canceled)
	require.Equal(t, model.JobStatusCanceled, canceled.Status, "queued cancellation = %#v", canceled)
	require.True(t, canceled.CompletedAt.Valid, "queued cancellation = %#v", canceled)

	running, err := queued.Start(at.Add(time.Second))
	require.NoError(t, err)
	requested, err := running.RequestCancellation(at.Add(2 * time.Second))
	require.NoError(t, err, "running cancellation = %#v", requested)
	require.Equal(t, model.JobStatusCancelRequested, requested.Status, "running cancellation = %#v", requested)
	require.False(t, requested.CompletedAt.Valid, "running cancellation = %#v", requested)

	failed, err := running.Fail("job.failed", at.Add(2*time.Second))
	require.NoError(t, err)
	retried, err := failed.ExplicitRetry(at.Add(3 * time.Second))
	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, retried.Status)
	require.False(t, retried.CompletedAt.Valid)
	require.Equal(t, failed.AttemptCount, retried.AttemptCount)
	require.Equal(t, failed.AttemptCount+1, retried.MaximumAttempts)
	_, err = queued.ExplicitRetry(at.Add(time.Second))
	require.Error(t, err, "non-terminal job accepted explicit retry")
}

func TestJobRelinquishPreservesTheFailureAttemptBudget(t *testing.T) {
	at := time.Now().UTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, json.RawMessage(`{"primary_key_id":"22222222222222222222222222222222","retiring_key_id":"11111111111111111111111111111111"}`), "mail-rekey:test", at, at, 3)
	require.NoError(t, err)
	running, err := job.Start(at.Add(time.Second))
	require.NoError(t, err)

	relinquished, err := running.Relinquish("worker.capability_mismatch", at.Add(3*time.Second), at.Add(2*time.Second))

	require.NoError(t, err)
	require.Equal(t, model.JobStatusQueued, relinquished.Status)
	require.Equal(t, 1, relinquished.AttemptCount)
	require.Equal(t, 4, relinquished.MaximumAttempts)
	require.Equal(t, job.MaximumAttempts-job.AttemptCount, relinquished.MaximumAttempts-relinquished.AttemptCount)
	require.Equal(t, "worker.capability_mismatch", relinquished.PublicErrorCode)
}
