// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobHistoryCleanerFake struct {
	requests []*store.JobHistoryCleanup
	results  []*store.JobHistoryCleanupResult
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
	handler := jobHistoryCleanupHandler{jobs: cleaner, policies: policies}
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
	handler := jobHistoryCleanupHandler{jobs: cleaner, policies: []store.JobRetentionPolicy{{Type: model.JobTypeCleanup, SucceededCanceledAge: 30 * 24 * time.Hour, FailedAge: 90 * 24 * time.Hour}}}
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
	outcome := (jobHistoryCleanupHandler{jobs: cleaner}).Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(cleaner.requests) != 0 {
		t.Fatalf("outcome=%#v requests=%#v", outcome, cleaner.requests)
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
