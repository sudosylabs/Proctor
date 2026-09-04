//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestJobHistoryCleanupHonorsStateRetentionAndSelfPreservation(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()

	oldSuccess := insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusSucceeded, 31*24*time.Hour, true)
	oldFailure := insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusFailed, 91*24*time.Hour, true)
	recentSuccess := insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusSucceeded, 29*24*time.Hour, false)
	recentFailure := insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusFailed, 89*24*time.Hour, false)
	queued := insertQueuedJobHistoryFixture(t, ss, 120*24*time.Hour)
	self := insertTerminalJobHistoryFixture(t, ss, model.JobTypeCleanup, model.JobStatusSucceeded, 120*24*time.Hour, true)

	request := &store.JobHistoryCleanup{
		ExcludeJobID: self,
		Policies: []store.JobRetentionPolicy{
			{Type: model.JobTypeProfilePictureGenerateDefault, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour},
			{Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour},
		},
		Limit: 1,
	}
	first, err := ss.Job().DeleteTerminalHistory(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Deleted != 1 || first.Done {
		t.Fatalf("first cleanup page = %#v", first)
	}
	request.AfterCompletedAt, request.AfterJobID = first.LastCompletedAt, first.LastJobID
	second, err := ss.Job().DeleteTerminalHistory(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Deleted != 1 {
		t.Fatalf("second cleanup page = %#v", second)
	}

	for _, id := range []model.JobID{oldSuccess, oldFailure} {
		if _, err = ss.Job().Get(ctx, id); !store.IsNotFound(err) {
			t.Fatalf("expired terminal job %s remains: %v", id, err)
		}
	}
	for _, id := range []model.JobID{recentSuccess, recentFailure, queued, self} {
		if _, err = ss.Job().Get(ctx, id); err != nil {
			t.Fatalf("protected job %s was removed: %v", id, err)
		}
	}
}

func insertTerminalJobHistoryFixture(t *testing.T, ss *SQLStore, jobType model.JobType, status model.JobStatus, age time.Duration, withAttempt bool) model.JobID {
	t.Helper()
	id := model.NewJobID()
	completedAt := model.NowUTC().Add(-age)
	createdAt := completedAt.Add(-time.Minute)
	if _, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO jobs (id, type, status, created_at, updated_at, available_at, started_at, completed_at, command_version, command, dedupe_key, attempt_count, maximum_attempts, revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, '{}'::jsonb, ?, ?, 3, 1)`, id.String(), string(jobType), string(status), createdAt, completedAt, createdAt, createdAt, completedAt, id.String(), boolInt(withAttempt)); err != nil {
		t.Fatal(err)
	}
	if withAttempt {
		attemptStatus := model.JobAttemptStatusFailed
		if status == model.JobStatusSucceeded {
			attemptStatus = model.JobAttemptStatusSucceeded
		}
		if _, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO job_attempts (id, job_id, number, status, node_id, claim_token, started_at, heartbeat_at, lease_expires_at, completed_at) VALUES (?, ?, 1, ?, 'node-a', ?, ?, ?, ?, ?)`, model.NewJobAttemptID().String(), id.String(), string(attemptStatus), string(mustClaimTokenForHistory(t)), createdAt, createdAt, createdAt.Add(time.Minute), completedAt); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func insertQueuedJobHistoryFixture(t *testing.T, ss *SQLStore, age time.Duration) model.JobID {
	t.Helper()
	id := model.NewJobID()
	at := model.NowUTC().Add(-age)
	if _, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO jobs (id, type, status, created_at, updated_at, available_at, command_version, command, dedupe_key, maximum_attempts, revision) VALUES (?, ?, 'queued', ?, ?, ?, 1, '{}'::jsonb, ?, 3, 1)`, id.String(), string(model.JobTypeProfilePictureGenerateDefault), at, at, at, id.String()); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustClaimTokenForHistory(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
