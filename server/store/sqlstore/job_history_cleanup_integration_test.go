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

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestJobHistoryCleanupContinuesAcrossNodesPastOneDailyBatch(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	for range 237 {
		insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusSucceeded, 31*24*time.Hour, true)
	}
	catalog := appjobs.NewCatalog(appjobs.CatalogDependencies{JobStore: ss.Job(), Now: time.Now})
	var descriptor jobengine.Descriptor
	var proposer jobengine.OccurrenceProposer
	for _, value := range catalog.Descriptors {
		if value.Type == model.JobTypeCleanup {
			descriptor = value
		}
	}
	for _, value := range catalog.Recurrences {
		if value.Name == "job-history-cleanup" {
			proposer = value.Proposer
		}
	}
	if descriptor.Handler == nil || proposer == nil {
		t.Fatal("history cleanup catalog is incomplete")
	}
	occurrence := model.NowUTC()
	for range 2 {
		if err := proposer.Propose(ctx, occurrence); err != nil {
			t.Fatal(err)
		}
	}
	for _, nodeID := range []string{"cleanup-node-a", "cleanup-node-b"} {
		engine, err := jobengine.New(jobengine.Config{Store: ss.Job(), Descriptors: []jobengine.Descriptor{descriptor}, NodeID: nodeID,
			Diagnostics: historyCleanupDiagnostics{t: t}, Policy: jobengine.Policy{PollInterval: 5 * time.Millisecond, ShutdownTimeout: time.Second}})
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := engine.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var remaining, completed int
		if err := ss.GetMaster().Get(ctx, &remaining, `SELECT COUNT(*) FROM jobs WHERE type=?`, string(model.JobTypeProfilePictureGenerateDefault)); err != nil {
			t.Fatal(err)
		}
		if err := ss.GetMaster().Get(ctx, &completed, `SELECT COUNT(*) FROM jobs WHERE type=? AND status='succeeded' AND work_reserved=100`, string(model.JobTypeCleanup)); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 && completed == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup did not converge: retained Jobs=%d succeeded bounded passes=%d", remaining, completed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var attempts, occurrences int
	if err := ss.GetMaster().Get(ctx, &attempts, `SELECT COUNT(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.type=?`, string(model.JobTypeProfilePictureGenerateDefault)); err != nil {
		t.Fatal(err)
	}
	if err := ss.GetMaster().Get(ctx, &occurrences, `SELECT COUNT(*) FROM job_permanent_occurrences WHERE type=?`, string(model.JobTypeCleanup)); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || occurrences != 3 {
		t.Fatalf("cleanup left attempts or forked occurrences: attempts=%d occurrences=%d", attempts, occurrences)
	}
}

type historyCleanupDiagnostics struct{ t *testing.T }

func (d historyCleanupDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if ctx.Err() != nil {
		return
	}
	d.t.Errorf("%s: %v", message, err)
}

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
