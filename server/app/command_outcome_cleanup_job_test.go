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
)

type commandOutcomeCleanerFake struct {
	limit   int
	deleted int64
	err     error
}

func (f *commandOutcomeCleanerFake) DeleteExpired(_ context.Context, limit int) (int64, error) {
	f.limit = limit
	return f.deleted, f.err
}

func TestCommandOutcomeCleanupDeletesOneBoundedPage(t *testing.T) {
	cleaner := &commandOutcomeCleanerFake{deleted: 17}
	command, _ := json.Marshal(CommandOutcomeCleanupCommandV1{BatchSize: 500})
	at := time.Now().UTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeCommandOutcomeCleanup, 1, command, "cleanup", at, at, 3)
	if err != nil {
		t.Fatal(err)
	}
	outcome := (commandOutcomeCleanupHandler{outcomes: cleaner}).Run(context.Background(), jobengine.NewExecution(record, nil, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || cleaner.limit != 500 {
		t.Fatalf("outcome = %#v, limit = %d", outcome, cleaner.limit)
	}
	var result CommandOutcomeCleanupResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || result.Deleted != 17 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}
