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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestJobHistoryCleanupContinuesAcrossNodesPastOneDailyBatch(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	for range 237 {
		insertTerminalJobHistoryFixture(t, ss, model.JobTypeProfilePictureGenerateDefault, model.JobStatusSucceeded, 31*24*time.Hour, true)
	}
	catalog := jobhandler.NewCatalog(jobhandler.CatalogDependencies{JobStore: ss.Job(), Now: time.Now})
	var descriptor job.Descriptor
	var proposer job.OccurrenceProposer
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
	require.NotNil(t, descriptor.Handler, "history cleanup catalog is incomplete")
	require.NotNil(t, proposer, "history cleanup catalog is incomplete")
	occurrence := model.NowUTC()
	for range 2 {
		require.NoError(t, proposer.Propose(ctx, occurrence))
	}
	for _, nodeID := range []string{"cleanup-node-a", "cleanup-node-b"} {
		engine, err := job.New(job.Dependencies{Store: ss.Job(), Descriptors: []job.Descriptor{descriptor}, NodeID: nodeID,
			Diagnostics: historyCleanupDiagnostics{t: t}, Policy: job.Policy{PollInterval: 5 * time.Millisecond, ShutdownTimeout: time.Second}})
		require.NoError(t, err)
		err = engine.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			assert.NoError(t, engine.Close())
		})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var remaining, completed int
		require.NoError(t, ss.GetMaster().Get(ctx, &remaining, `SELECT COUNT(*) FROM jobs WHERE type=?`, string(model.JobTypeProfilePictureGenerateDefault)))
		require.NoError(t, ss.GetMaster().Get(ctx, &completed, `SELECT COUNT(*) FROM jobs WHERE type=? AND status='succeeded' AND work_reserved=100`, string(model.JobTypeCleanup)))
		if remaining == 0 && completed == 3 {
			break
		}
		require.False(t, time.Now().After(deadline), "cleanup did not converge: retained Jobs=%d succeeded bounded passes=%d", remaining, completed)
		time.Sleep(10 * time.Millisecond)
	}
	var attempts, occurrences int
	require.NoError(t, ss.GetMaster().Get(ctx, &attempts, `SELECT COUNT(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.type=?`, string(model.JobTypeProfilePictureGenerateDefault)))
	require.NoError(t, ss.GetMaster().Get(ctx, &occurrences, `SELECT COUNT(*) FROM job_permanent_occurrences WHERE type=?`, string(model.JobTypeCleanup)))
	require.Equal(t, 0, attempts, "cleanup left attempts or forked occurrences: attempts")
	require.Equal(t, 3, occurrences, "cleanup left attempts or forked occurrences: attempts")
}

type historyCleanupDiagnostics struct{ t *testing.T }

func (d historyCleanupDiagnostics) ErrorContext(ctx context.Context, message string, err error) {
	if ctx.Err() != nil {
		return
	}
	assert.Fail(d.t, fmt.Sprintf("%s: %v", message, err))
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
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Deleted)
	require.False(t, first.Done)
	request.AfterCompletedAt, request.AfterJobID = first.LastCompletedAt, first.LastJobID
	second, err := ss.Job().DeleteTerminalHistory(ctx, request)
	require.NoError(t, err)
	require.Equal(t, int64(1), second.Deleted)

	for _, id := range []model.JobID{oldSuccess, oldFailure} {
		_, err = ss.Job().Get(ctx, id)
		require.True(t, store.IsNotFound(err), "expired terminal job %s remains", id)
	}
	for _, id := range []model.JobID{recentSuccess, recentFailure, queued, self} {
		_, err = ss.Job().Get(ctx, id)
		require.NoError(t, err, "protected job %s was removed", id)
	}
}

// A terminal Job that a mail delivery still references must survive cleanup:
// its NO ACTION foreign key would fail every page that reached it, and the
// cleanup chain would retry forever without deleting any history.
func TestJobHistoryCleanupSkipsReferencedJobs(t *testing.T) {
	ss := openTestStore(t)
	resetTestStore(t, ss)
	ctx := context.Background()
	institution, err := ss.Institution().Save(ctx, &model.Institution{Name: "history-reference", DisplayName: "History Reference"})
	require.NoError(t, err)
	user := saveIntegrationUser(t, ctx, ss, &model.User{Username: "history-reference", Email: "history-reference@example.edu", DisplayName: "History Reference"})
	enqueue := storetest.MailTestEnqueueFixtureForSQLTest(t, user, institution, model.NowUTC())
	delivery, err := ss.Mail().EnqueueTest(ctx, enqueue)
	require.NoError(t, err)
	_, err = ss.GetMaster().Exec(ctx, `UPDATE jobs SET status='canceled', public_error_code='job.canceled', created_at=clock_timestamp()-interval '32 days',
		updated_at=clock_timestamp()-interval '31 days', available_at=clock_timestamp()-interval '32 days', completed_at=clock_timestamp()-interval '31 days' WHERE id=?`, delivery.JobID.String())
	require.NoError(t, err)
	expired := insertTerminalJobHistoryFixture(t, ss, model.JobTypeMailDeliver, model.JobStatusSucceeded, 40*24*time.Hour, true)
	self := insertTerminalJobHistoryFixture(t, ss, model.JobTypeCleanup, model.JobStatusSucceeded, time.Hour, false)
	result, err := ss.Job().DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: self, Limit: 10,
		Policies: []store.JobRetentionPolicy{{Type: model.JobTypeMailDeliver, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour}}})
	require.NoError(t, err, "cleanup reached a referenced Job")
	require.Equal(t, int64(1), result.Deleted)
	require.True(t, result.Done)
	_, err = ss.Job().Get(ctx, expired)
	require.True(t, store.IsNotFound(err), "unreferenced expired Job remains")
	_, err = ss.Job().Get(ctx, delivery.JobID)
	require.NoError(t, err, "referenced Job was removed")
}

func insertTerminalJobHistoryFixture(t *testing.T, ss *SQLStore, jobType model.JobType, status model.JobStatus, age time.Duration, withAttempt bool) model.JobID {
	t.Helper()
	id := model.NewJobID()
	completedAt := model.NowUTC().Add(-age)
	createdAt := completedAt.Add(-time.Minute)
	_, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO jobs (id, type, status, created_at, updated_at, available_at, started_at, completed_at, command, dedupe_key, attempt_count, maximum_attempts, revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '{}'::jsonb, ?, ?, 3, 1)`, id.String(), string(jobType), string(status), createdAt, completedAt, createdAt, createdAt, completedAt, id.String(), boolInt(withAttempt))
	require.NoError(t, err)
	if withAttempt {
		attemptStatus := model.JobAttemptStatusFailed
		if status == model.JobStatusSucceeded {
			attemptStatus = model.JobAttemptStatusSucceeded
		}
		_, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO job_attempts (id, job_id, number, status, node_id, claim_token, started_at, heartbeat_at, lease_expires_at, completed_at) VALUES (?, ?, 1, ?, 'node-a', ?, ?, ?, ?, ?)`, model.NewJobAttemptID().String(), id.String(), string(attemptStatus), string(mustClaimTokenForHistory(t)), createdAt, createdAt, createdAt.Add(time.Minute), completedAt)
		require.NoError(t, err)
	}
	return id
}

func insertQueuedJobHistoryFixture(t *testing.T, ss *SQLStore, age time.Duration) model.JobID {
	t.Helper()
	id := model.NewJobID()
	at := model.NowUTC().Add(-age)
	_, err := ss.GetMaster().Exec(context.Background(), `INSERT INTO jobs (id, type, status, created_at, updated_at, available_at, command, dedupe_key, maximum_attempts, revision) VALUES (?, ?, 'queued', ?, ?, ?, '{}'::jsonb, ?, 3, 1)`, id.String(), string(model.JobTypeProfilePictureGenerateDefault), at, at, at, id.String())
	require.NoError(t, err)
	return id
}

func mustClaimTokenForHistory(t *testing.T) model.JobClaimToken {
	t.Helper()
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	return token
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
