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

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumJobHistoryCleanupBatch = 200

// JobHistoryCleanupCommand is the immutable command of the Job history cleanup Job.
type JobHistoryCleanupCommand struct {
	BatchSize int `json:"batch_size"`
}

// JobHistoryCleanupCheckpoint records one bounded pass and whether it
// exhausted the eligible history.
type JobHistoryCleanupCheckpoint struct {
	AfterCompletedAt time.Time   `json:"after_completed_at,omitempty"`
	AfterJobID       model.JobID `json:"after_job_id,omitempty"`
	Deleted          int64       `json:"deleted"`
	Done             bool        `json:"done"`
}

// JobHistoryCleanupResult is the safe result of the Job history cleanup Job.
type JobHistoryCleanupResult struct {
	Deleted int64 `json:"deleted"`
}

// EncodeJobHistoryCleanupCommand validates and encodes a Job history cleanup Job command.
func EncodeJobHistoryCleanupCommand(value JobHistoryCleanupCommand) (json.RawMessage, error) {
	if value.BatchSize < 1 || value.BatchSize > maximumJobHistoryCleanupBatch {
		return nil, errors.New("job history cleanup batch size is invalid")
	}
	return json.Marshal(value)
}

// DecodeJobHistoryCleanupCommand strictly decodes and validates a Job history cleanup Job command.
func DecodeJobHistoryCleanupCommand(document json.RawMessage) (JobHistoryCleanupCommand, error) {
	var value JobHistoryCleanupCommand
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	if value.BatchSize < 1 || value.BatchSize > maximumJobHistoryCleanupBatch {
		return value, errors.New("job history cleanup batch size is invalid")
	}
	return value, nil
}

func decodeJobHistoryCleanupCheckpoint(document json.RawMessage) (JobHistoryCleanupCheckpoint, error) {
	var value JobHistoryCleanupCheckpoint
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	return value, validateJobHistoryCleanupCheckpoint(value)
}

func validateJobHistoryCleanupCheckpoint(value JobHistoryCleanupCheckpoint) error {
	if value.Deleted < 0 || value.AfterCompletedAt.IsZero() != value.AfterJobID.IsZero() || (!value.AfterJobID.IsZero() && !value.AfterJobID.IsValid()) {
		return errors.New("job history cleanup checkpoint is invalid")
	}
	return nil
}

type JobHistoryCleaner interface {
	DeleteTerminalHistory(context.Context, *store.JobHistoryCleanup) (*store.JobHistoryCleanupResult, error)
}

type jobHistoryCleanupHandler struct {
	jobs          JobHistoryCleaner
	continuations JobEnqueuer
	policies      []store.JobRetentionPolicy
	now           func() time.Time
}

func (h jobHistoryCleanupHandler) Run(ctx context.Context, execution job.Execution) job.Outcome {
	if execution.Job == nil || !execution.Job.ID.IsValid() || h.jobs == nil || h.continuations == nil || h.now == nil {
		return job.PermanentFailure("job.command.invalid", errors.New("job history cleanup dependencies or execution are missing"))
	}
	command, err := DecodeJobHistoryCleanupCommand(execution.Job.Command)
	if err != nil {
		return job.PermanentFailure("job.command.invalid", err)
	}
	if execution.Job.WorkReserved < 0 || execution.Job.WorkReserved > command.BatchSize {
		return job.PermanentFailure("job.invariant_failed", errors.New("job history cleanup reservation exceeds its work bound"))
	}
	checkpoint := JobHistoryCleanupCheckpoint{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = decodeJobHistoryCleanupCheckpoint(execution.Job.Checkpoint)
		if err != nil {
			return job.PermanentFailure("job.checkpoint.invalid", err)
		}
		return h.finish(ctx, execution.Job, command, checkpoint)
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if remaining <= 0 {
		// A reservation survives a crash or failure before deletion or
		// checkpoint. Never reuse it, and never answer it with an immediate
		// successor: a deletion that keeps failing would otherwise spawn an
		// unbounded chain of Jobs with fresh budgets. The next daily
		// occurrence starts from the oldest remaining history.
		return job.PermanentFailure("dependency.unavailable", errors.New("job history cleanup batch did not complete"))
	}
	reserved, reserveErr := execution.ReserveWork(ctx, remaining, command.BatchSize)
	if reserveErr != nil {
		return job.RetryableFailure("dependency.unavailable", reserveErr)
	}
	if !reserved {
		return job.PermanentFailure("dependency.unavailable", errors.New("job history cleanup batch did not complete"))
	}
	page, deleteErr := h.jobs.DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: execution.Job.ID, Policies: h.policies, Limit: remaining})
	if deleteErr != nil {
		return job.RetryableFailure("dependency.unavailable", deleteErr)
	}
	if page == nil || page.Deleted < 0 || page.Deleted > int64(remaining) {
		return job.PermanentFailure("job.invariant_failed", errors.New("job history cleanup returned an invalid page"))
	}
	checkpoint.Deleted = page.Deleted
	checkpoint.AfterCompletedAt = page.LastCompletedAt
	checkpoint.AfterJobID = page.LastJobID
	checkpoint.Done = page.Done
	if err := validateJobHistoryCleanupCheckpoint(checkpoint); err != nil {
		return job.PermanentFailure("job.invariant_failed", err)
	}
	document, encodeErr := json.Marshal(checkpoint)
	if encodeErr != nil {
		return job.PermanentFailure("job.invariant_failed", encodeErr)
	}
	var progress *model.JobProgress
	if checkpoint.Deleted > 0 {
		progress = &model.JobProgress{Current: checkpoint.Deleted, Total: checkpoint.Deleted, Stage: "completed"}
	}
	if checkpointErr := execution.Checkpoint(ctx, job.CheckpointValue{Progress: progress, Document: document}); checkpointErr != nil {
		return job.RetryableFailure("dependency.unavailable", checkpointErr)
	}
	return h.finish(ctx, execution.Job, command, checkpoint)
}

func (h jobHistoryCleanupHandler) finish(ctx context.Context, parent *model.Job, command JobHistoryCleanupCommand, checkpoint JobHistoryCleanupCheckpoint) job.Outcome {
	// Only a completed, nonempty batch creates an immediate successor.
	if !checkpoint.Done && checkpoint.Deleted > 0 {
		document, err := EncodeJobHistoryCleanupCommand(command)
		if err != nil {
			return job.PermanentFailure("job.invariant_failed", err)
		}
		at := model.TimeUTC(h.now())
		successor, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, document,
			"job-history-cleanup:after:"+parent.ID.String(), model.JobDedupePermanent, at, at, 5)
		if err != nil {
			return job.PermanentFailure("job.invariant_failed", err)
		}
		if _, _, err = h.continuations.Enqueue(ctx, &store.JobEnqueue{Job: successor}); err != nil {
			return job.RetryableFailure("dependency.unavailable", err)
		}
	}
	document, marshalErr := json.Marshal(JobHistoryCleanupResult{Deleted: checkpoint.Deleted})
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: document, Err: marshalErr}
}

type jobHistoryCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p jobHistoryCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	at := model.TimeUTC(p.now())
	command, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommand{BatchSize: 100})
	if err != nil {
		return err
	}
	key := "job-history-cleanup:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func jobHistoryCleanupDescriptor(handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: model.JobTypeCleanup, Checkpoints: true, ProgressStages: []string{"completed"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: job.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
