// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumCommandOutcomeCleanupBatch = 500

type CommandOutcomeCleanupCommandV1 struct {
	BatchSize int `json:"batch_size"`
}
type CommandOutcomeCleanupResultV1 struct {
	Deleted int64 `json:"deleted"`
}

type CommandOutcomeCleaner interface {
	DeleteExpired(context.Context, int) (int64, error)
}
type commandOutcomeCleanupHandler struct{ outcomes CommandOutcomeCleaner }

func (h commandOutcomeCleanupHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	if execution.Job.CommandVersion != 1 {
		return jobengine.PermanentFailure("job.command.invalid", fmt.Errorf("unsupported command version %d", execution.Job.CommandVersion))
	}
	var command CommandOutcomeCleanupCommandV1
	if err := decodeStrictJobDocument(execution.Job.Command, &command); err != nil || command.BatchSize < 1 || command.BatchSize > maximumCommandOutcomeCleanupBatch {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("command outcome cleanup command is invalid"))
	}
	deleted, err := h.outcomes.DeleteExpired(ctx, command.BatchSize)
	if err != nil {
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	document, err := json.Marshal(CommandOutcomeCleanupResultV1{Deleted: deleted})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type commandOutcomeCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p commandOutcomeCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	command, err := json.Marshal(CommandOutcomeCleanupCommandV1{BatchSize: maximumCommandOutcomeCleanupBatch})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "command-outcome-cleanup:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCommandOutcomeCleanup, 1, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func commandOutcomeCleanupDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeCommandOutcomeCleanup, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
