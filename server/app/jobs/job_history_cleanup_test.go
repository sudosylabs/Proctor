// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"batch_size":2}`), "cleanup:2026-08-10", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
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
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.Err != nil || len(cleaner.requests) != 1 || len(checkpoints) != 1 {
		t.Fatalf("outcome=%#v requests=%#v checkpoints=%#v", outcome, cleaner.requests, checkpoints)
	}
	if cleaner.requests[0].ExcludeJobID != job.ID || cleaner.requests[0].Limit != 2 {
		t.Fatalf("cleanup requests = %#v", cleaner.requests)
	}
	var final JobHistoryCleanupCheckpointV1
	if err = json.Unmarshal(checkpoints[0].Document, &final); err != nil {
		t.Fatal(err)
	}
	if final.Deleted != 2 || final.AfterJobID != firstCursorID || checkpoints[0].Progress.Current != 2 || checkpoints[0].Progress.Total != 2 || checkpoints[0].Progress.Stage != "completed" {
		t.Fatalf("final=%#v progress=%#v", final, checkpoints[0].Progress)
	}
}

func TestJobHistoryCleanupDoesNotDeleteAnotherBatchAfterCommittedCheckpoint(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	command, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommandV1{BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, command, "cleanup:2026-08-10", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	cursorTime := at.Add(-100 * 24 * time.Hour)
	cursorID := model.NewJobID()
	checkpoint, err := EncodeJobHistoryCleanupCheckpoint(JobHistoryCleanupCheckpointV1{AfterCompletedAt: cursorTime, AfterJobID: cursorID, Deleted: 20})
	if err != nil {
		t.Fatal(err)
	}
	job.CheckpointVersion = 1
	job.Checkpoint = checkpoint
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{{Done: true}}}
	handler := historyCleanupHandlerForTest(cleaner, []store.JobRetentionPolicy{{Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour}}, at)
	outcome := handler.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(cleaner.requests) != 0 {
		t.Fatalf("outcome=%#v requests=%#v", outcome, cleaner.requests)
	}
}

func TestJobHistoryCleanupDoesNotRepeatReservedDeletionAfterCrash(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	job.WorkReserved = 2
	cleaner := &jobHistoryCleanerFake{}
	outcome := historyCleanupHandlerForTest(cleaner, nil, at).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(cleaner.requests) != 0 {
		t.Fatalf("outcome=%#v requests=%#v", outcome, cleaner.requests)
	}
	if len(cleaner.enqueued) != 1 {
		t.Fatal("uncertain reserved work did not leave a continuation")
	}
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
	if err := proposer.Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	handler := historyCleanupHandlerForTest(cleaner, nil, at)
	var deleted int64
	for index := 0; index < len(cleaner.enqueued); index++ {
		if index >= 3 {
			t.Fatal("exhausted history continued creating work")
		}
		current := cleaner.enqueued[index]
		outcome := handler.Run(context.Background(), testJobExecution(current, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
			current.CheckpointVersion, current.Checkpoint = value.Version, value.Document
			return nil
		}))
		if outcome.Kind != jobengine.OutcomeSucceeded || outcome.Err != nil {
			t.Fatalf("pass %d outcome = %#v", index, outcome)
		}
		var result JobHistoryCleanupResultV1
		if err := json.Unmarshal(outcome.Result, &result); err != nil {
			t.Fatal(err)
		}
		deleted += result.Deleted
		if current.DedupePolicy != model.JobDedupePermanent || cleaner.requests[index].Limit != 100 {
			t.Fatal("continuation changed the occurrence identity or work bound")
		}
	}
	if deleted != 237 || len(cleaner.requests) != 3 || len(cleaner.enqueued) != 3 {
		t.Fatalf("deleted=%d requests=%d occurrences=%d", deleted, len(cleaner.requests), len(cleaner.enqueued))
	}
	if err := proposer.Propose(context.Background(), at); err != nil || len(cleaner.enqueued) != 3 {
		t.Fatalf("another node repeated the daily occurrence: %v", err)
	}
}

func TestJobHistoryCleanupRetriesUnknownContinuationOutcomeWithoutRepeatingDeletion(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	current, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &jobHistoryCleanerFake{results: []*store.JobHistoryCleanupResult{{Deleted: 2, LastCompletedAt: at.Add(-100 * 24 * time.Hour), LastJobID: model.NewJobID()}}, enqueueErr: errors.New("unknown commit outcome")}
	handler := historyCleanupHandlerForTest(cleaner, nil, at)
	execution := testJobExecution(current, allowJobWorkReservation(), func(_ context.Context, value jobengine.CheckpointValue) error {
		current.CheckpointVersion, current.Checkpoint = value.Version, value.Document
		return nil
	})
	if first := handler.Run(context.Background(), execution); first.Kind != jobengine.OutcomeRetryableFailure {
		t.Fatalf("first outcome = %#v", first)
	}
	cleaner.enqueueErr = nil
	if retry := handler.Run(context.Background(), execution); retry.Kind != jobengine.OutcomeSucceeded {
		t.Fatalf("retry outcome = %#v", retry)
	}
	if len(cleaner.requests) != 1 || len(cleaner.enqueued) != 1 {
		t.Fatalf("retry repeated work: deletions=%d successors=%d", len(cleaner.requests), len(cleaner.enqueued))
	}
}

func TestJobHistoryCleanupExhaustedCheckpointDoesNotEnqueueContinuation(t *testing.T) {
	t.Parallel()
	at := model.NowUTC()
	current, err := model.NewJob(model.NewJobID(), model.JobTypeCleanup, 1, json.RawMessage(`{"batch_size":2}`), "daily", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	current.CheckpointVersion = 2
	current.Checkpoint = json.RawMessage(`{"deleted":0,"done":true}`)
	cleaner := &jobHistoryCleanerFake{}
	outcome := historyCleanupHandlerForTest(cleaner, nil, at).Run(context.Background(), testJobExecution(current, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(cleaner.requests) != 0 || len(cleaner.enqueued) != 0 {
		t.Fatalf("exhausted retry outcome=%#v requests=%d continuations=%d", outcome, len(cleaner.requests), len(cleaner.enqueued))
	}
}

func TestJobHistoryCleanupContractsRejectUnboundedOrUnsafeValues(t *testing.T) {
	t.Parallel()

	if _, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommandV1{BatchSize: 0}); err == nil {
		t.Fatal("accepted zero batch")
	}
	if _, err := DecodeJobHistoryCleanupCommand(1, json.RawMessage(`{"batch_size":1,"payload":"unsafe"}`)); err == nil {
		t.Fatal("accepted unknown command field")
	}
	if _, err := EncodeJobHistoryCleanupCheckpoint(JobHistoryCleanupCheckpointV1{AfterJobID: model.NewJobID()}); err == nil {
		t.Fatal("accepted incomplete cursor")
	}
}
