// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobHistoryCleanerFake struct {
	requests   []*store.JobHistoryCleanup
	results    []*store.JobHistoryCleanupResult
	enqueued   []*model.Job
	winners    map[string]*model.Job
	enqueueErr error
}

func (f *jobHistoryCleanerFake) Enqueue(_ context.Context, input *store.JobEnqueue) (*model.Job, bool, error) {
	if f.winners == nil {
		f.winners = make(map[string]*model.Job)
	}
	if winner := f.winners[input.Job.DedupeKey]; winner != nil {
		return winner, false, f.enqueueErr
	}
	copy := *input.Job
	f.enqueued = append(f.enqueued, &copy)
	f.winners[copy.DedupeKey] = &copy
	return &copy, true, f.enqueueErr
}

func historyCleanupHandlerForTest(cleaner *jobHistoryCleanerFake, policies []store.JobRetentionPolicy, at time.Time) jobHistoryCleanupHandler {
	return jobHistoryCleanupHandler{jobs: cleaner, continuations: cleaner, policies: policies, now: func() time.Time { return at }}
}

func (f *jobHistoryCleanerFake) DeleteTerminalHistory(_ context.Context, request *store.JobHistoryCleanup) (*store.JobHistoryCleanupResult, error) {
	copy := *request
	copy.Policies = append([]store.JobRetentionPolicy(nil), request.Policies...)
	f.requests = append(f.requests, &copy)
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestJobHistoryCleanupDeletesBoundedPagesAndCheckpointsSafely(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{"batch_size":2}`), "cleanup:2026-08-10", at, at, 5)
	require.NoError(t, err)
	firstCursorTime := at.Add(-100 * 24 * time.Hour)
	firstCursorID := model.NewJobID()
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{
		{Deleted: 2, LastCompletedAt: firstCursorTime, LastJobID: firstCursorID},
	}}
	policies := []store.JobRetentionPolicy{{Type: model.JobTypeProfilePictureGenerateDefault, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour}}
	handler := historyCleanupHandlerForTest(cleaner, policies, at)
	var checkpoints []jobengine.CheckpointValue
	execution := testJobExecution(job, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	})

	outcome := handler.Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v requests=%#v checkpoints", outcome, cleaner.requests)
	require.NoError(t, outcome.Err, "outcome=%#v requests=%#v checkpoints", outcome, cleaner.requests)
	require.Len(t, cleaner.requests, 1, "outcome=%#v requests=%#v checkpoints", outcome, cleaner.requests)
	require.Len(t, checkpoints, 1, "outcome=%#v requests=%#v checkpoints", outcome, cleaner.requests)
	require.Equal(t, job.ID, cleaner.requests[0].ExcludeJobID)
	require.Equal(t, 2, cleaner.requests[0].Limit)
	var final JobHistoryCleanupCheckpoint
	err = json.Unmarshal(checkpoints[0].Document, &final)
	require.NoError(t, err)
	require.Equal(t, int64(2), final.Deleted, "final")
	require.Equal(t, firstCursorID, final.AfterJobID, "final")
	require.Equal(t, int64(2), checkpoints[0].Progress.Current, "final")
	require.Equal(t, int64(2), checkpoints[0].Progress.Total, "final")
	require.Equal(t, "completed", checkpoints[0].Progress.Stage, "final")
}

func TestJobHistoryCleanupDoesNotDeleteAnotherBatchAfterCommittedCheckpoint(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	command, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommand{BatchSize: 10})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, command, "cleanup:2026-08-10", at, at, 5)
	require.NoError(t, err)
	cursorTime := at.Add(-100 * 24 * time.Hour)
	cursorID := model.NewJobID()
	checkpoint, err := json.Marshal(JobHistoryCleanupCheckpoint{AfterCompletedAt: cursorTime, AfterJobID: cursorID, Deleted: 20})
	require.NoError(t, err)
	job.Checkpoint = checkpoint
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{{Done: true}}}
	handler := historyCleanupHandlerForTest(cleaner, []store.JobRetentionPolicy{{Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour}}, at)
	outcome := handler.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v requests", outcome)
	require.Empty(t, cleaner.requests, "outcome=%#v requests", outcome)
}

func TestJobHistoryCleanupDoesNotRepeatReservedDeletionAfterCrash(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	require.NoError(t, err)
	job.WorkReserved = 2
	cleaner := &jobHistoryCleanerFake{}
	outcome := historyCleanupHandlerForTest(cleaner, nil, at).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind, "outcome=%#v requests", outcome)
	require.Empty(t, cleaner.requests, "outcome=%#v requests", outcome)
	// An immediate successor would let a deletion that keeps failing spawn an
	// unbounded chain of fresh Jobs; the next daily occurrence resumes.
	require.Empty(t, cleaner.enqueued, "uncertain reserved work created an immediate successor")
}

func TestJobHistoryCleanupEmptyIncompleteBatchDoesNotEnqueueContinuation(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	current, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	require.NoError(t, err)
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{{Deleted: 0, LastCompletedAt: at.Add(-100 * 24 * time.Hour), LastJobID: model.NewJobID()}}}
	outcome := historyCleanupHandlerForTest(cleaner, nil, at).Run(context.Background(), testJobExecution(current, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v", outcome)
	require.Empty(t, cleaner.enqueued, "empty batch created an immediate successor")
}

func TestJobHistoryCleanupContinuesBeyondDailyBatchUntilExhausted(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{
		{Deleted: 100, LastCompletedAt: at.Add(-100 * 24 * time.Hour), LastJobID: model.NewJobID()},
		{Deleted: 100, LastCompletedAt: at.Add(-99 * 24 * time.Hour), LastJobID: model.NewJobID()},
		{Deleted: 37, LastCompletedAt: at.Add(-98 * 24 * time.Hour), LastJobID: model.NewJobID(), Done: true},
	}}
	proposer := jobHistoryCleanupProposer{jobs: cleaner, now: func() time.Time { return at }}
	require.NoError(t, proposer.Propose(context.Background(), at))
	handler := historyCleanupHandlerForTest(cleaner, nil, at)
	var deleted int64
	for index := 0; index < len(cleaner.enqueued); index++ {
		require.Less(t, index, 3, "exhausted history continued creating work")
		current := cleaner.enqueued[index]
		outcome := handler.Run(context.Background(), testJobExecution(current, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
			current.Checkpoint = value.Document
			return nil
		}))
		require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "pass %d outcome = %#v", index, outcome)
		require.NoError(t, outcome.Err, "pass %d outcome = %#v", index, outcome)
		var result JobHistoryCleanupResult
		require.NoError(t, json.Unmarshal(outcome.Result, &result))
		deleted += result.Deleted
		require.Equal(t, model.JobDedupePermanent, current.DedupePolicy, "continuation changed the occurrence identity or work bound")
		require.Equal(t, 100, cleaner.requests[index].Limit, "continuation changed the occurrence identity or work bound")
	}
	require.Equal(t, int64(237), deleted, "deleted")
	require.Len(t, cleaner.requests, 3, "deleted")
	require.Len(t, cleaner.enqueued, 3, "deleted")
	require.NoError(t, proposer.Propose(context.Background(), at))
	require.Len(t, cleaner.enqueued, 3)
}

func TestJobHistoryCleanupRetriesUnknownContinuationOutcomeWithoutRepeatingDeletion(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	current, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	require.NoError(t, err)
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{{Deleted: 2, LastCompletedAt: at.Add(-100 * 24 * time.Hour), LastJobID: model.NewJobID()}}, enqueueErr: errors.New("unknown commit outcome")}
	handler := historyCleanupHandlerForTest(cleaner, nil, at)
	execution := testJobExecution(current, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		current.Checkpoint = value.Document
		return nil
	})
	require.Equal(t, jobengine.OutcomeRetryableFailure, handler.Run(context.Background(), execution).Kind)
	cleaner.enqueueErr = nil
	require.Equal(t, jobengine.OutcomeSucceeded, handler.Run(context.Background(), execution).Kind)
	require.Len(t, cleaner.requests, 1, "retry repeated work: deletions")
	require.Len(t, cleaner.enqueued, 1, "retry repeated work: deletions")
}

func TestJobHistoryCleanupExhaustedCheckpointDoesNotEnqueueContinuation(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	current, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	require.NoError(t, err)
	current.Checkpoint = json.RawMessage(`{"deleted":0,"done":true}`)
	cleaner := &jobHistoryCleanerFake{}
	outcome := historyCleanupHandlerForTest(cleaner, nil, at).Run(context.Background(), testJobExecution(current, allowJobWorkReservation(), nil))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "exhausted retry outcome=%#v requests=%d continuations", outcome, len(cleaner.requests))
	require.Empty(t, cleaner.requests, "exhausted retry outcome=%#v requests=%d continuations", outcome, len(cleaner.requests))
	require.Empty(t, cleaner.enqueued, "exhausted retry outcome=%#v requests=%d continuations", outcome, len(cleaner.requests))
}

func TestJobHistoryCleanupContractsRejectUnboundedOrUnsafeValues(t *testing.T) {
	t.Parallel()

	_, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommand{BatchSize: 0})
	require.Error(t, err, "accepted zero batch")
	_, err = DecodeJobHistoryCleanupCommand(json.RawMessage(`{"batch_size":1,"payload":"unsafe"}`))
	require.Error(t, err, "accepted unknown command field")
	err = validateJobHistoryCleanupCheckpoint(JobHistoryCleanupCheckpoint{AfterJobID: model.NewJobID()})
	require.Error(t, err, "accepted incomplete cursor")
}
