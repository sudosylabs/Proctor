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
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumCommandOutcomeCleanupBatch = 500

// CommandOutcomeCleanupCommand is the immutable command of the command outcome cleanup Job.
type CommandOutcomeCleanupCommand struct {
	BatchSize int `json:"batch_size"`
}

// CommandOutcomeCleanupResult is the safe result of the command outcome cleanup Job.
type CommandOutcomeCleanupResult struct {
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
	var command CommandOutcomeCleanupCommand
	if err := decodeStrictJobDocument(execution.Job.Command, &command); err != nil || command.BatchSize < 1 || command.BatchSize > maximumCommandOutcomeCleanupBatch {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("command outcome cleanup command is invalid"))
	}
	deleted, err := h.outcomes.DeleteExpired(ctx, command.BatchSize)
	if err != nil {
		return jobengine.RetryableFailure("dependency.unavailable", err)
	}
	document, err := json.Marshal(CommandOutcomeCleanupResult{Deleted: deleted})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, Result: document, Err: err}
}

type commandOutcomeCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p commandOutcomeCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	command, err := json.Marshal(CommandOutcomeCleanupCommand{BatchSize: maximumCommandOutcomeCleanupBatch})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "command-outcome-cleanup:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCommandOutcomeCleanup, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func commandOutcomeCleanupDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeCommandOutcomeCleanup, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
