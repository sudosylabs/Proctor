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
	"fmt"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const maximumJobHistoryCleanupBatch = 200

type JobHistoryCleanupCommandV1 struct {
	BatchSize int `json:"batch_size"`
}

type JobHistoryCleanupCheckpointV1 struct {
	AfterCompletedAt time.Time   `json:"after_completed_at,omitempty"`
	AfterJobID       model.JobID `json:"after_job_id,omitempty"`
	Deleted          int64       `json:"deleted"`
}

// Version 2 records whether the bounded pass exhausted the eligible history.
// Version 1 remains readable; its unknown remainder gets a conservative pass.
type JobHistoryCleanupCheckpointV2 struct {
	JobHistoryCleanupCheckpointV1
	Done bool `json:"done"`
}

type JobHistoryCleanupResultV1 struct {
	Deleted int64 `json:"deleted"`
}

func EncodeJobHistoryCleanupCommand(value JobHistoryCleanupCommandV1) (json.RawMessage, error) {
	if value.BatchSize < 1 || value.BatchSize > maximumJobHistoryCleanupBatch {
		return nil, errors.New("job history cleanup batch size is invalid")
	}
	return json.Marshal(value)
}

func DecodeJobHistoryCleanupCommand(version int, document json.RawMessage) (JobHistoryCleanupCommandV1, error) {
	var value JobHistoryCleanupCommandV1
	if version != 1 {
		return value, fmt.Errorf("unsupported job history cleanup command version %d", version)
	}
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	if value.BatchSize < 1 || value.BatchSize > maximumJobHistoryCleanupBatch {
		return value, errors.New("job history cleanup batch size is invalid")
	}
	return value, nil
}

func EncodeJobHistoryCleanupCheckpoint(value JobHistoryCleanupCheckpointV1) (json.RawMessage, error) {
	if err := validateJobHistoryCleanupCheckpoint(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func DecodeJobHistoryCleanupCheckpoint(version int, document json.RawMessage) (JobHistoryCleanupCheckpointV1, error) {
	var value JobHistoryCleanupCheckpointV1
	if version != 1 {
		return value, fmt.Errorf("unsupported job history cleanup checkpoint version %d", version)
	}
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	return value, validateJobHistoryCleanupCheckpoint(value)
}

func validateJobHistoryCleanupCheckpoint(value JobHistoryCleanupCheckpointV1) error {
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

func (h jobHistoryCleanupHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || !execution.Job.ID.IsValid() || h.jobs == nil || h.continuations == nil || h.now == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job history cleanup dependencies or execution are missing"))
	}
	command, err := DecodeJobHistoryCleanupCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	if execution.Job.WorkReserved < 0 || execution.Job.WorkReserved > command.BatchSize {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("job history cleanup reservation exceeds its work bound"))
	}
	checkpoint := JobHistoryCleanupCheckpointV2{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = decodeHistoryCleanupCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
		return h.finish(ctx, execution.Job, command, checkpoint)
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if remaining <= 0 {
		// A reservation survives a crash before deletion or checkpoint. Never
		// reuse it; the successor starts from the oldest remaining history.
		return h.finish(ctx, execution.Job, command, checkpoint)
	}
	reserved, reserveErr := execution.ReserveWork(ctx, remaining, command.BatchSize)
	if reserveErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", reserveErr)
	}
	if !reserved {
		return h.finish(ctx, execution.Job, command, checkpoint)
	}
	page, deleteErr := h.jobs.DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: execution.Job.ID, Policies: h.policies, Limit: remaining})
	if deleteErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", deleteErr)
	}
	if page == nil || page.Deleted < 0 || page.Deleted > int64(remaining) {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("job history cleanup returned an invalid page"))
	}
	checkpoint.Deleted = page.Deleted
	checkpoint.AfterCompletedAt = page.LastCompletedAt
	checkpoint.AfterJobID = page.LastJobID
	checkpoint.Done = page.Done
	if err := validateJobHistoryCleanupCheckpoint(checkpoint.JobHistoryCleanupCheckpointV1); err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	document, encodeErr := json.Marshal(checkpoint)
	if encodeErr != nil {
		return jobengine.PermanentFailure("job.invariant_failed", encodeErr)
	}
	var progress *model.JobProgress
	if checkpoint.Deleted > 0 {
		progress = &model.JobProgress{Current: checkpoint.Deleted, Total: checkpoint.Deleted, Stage: "completed"}
	}
	if checkpointErr := execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 2, Progress: progress, Document: document}); checkpointErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
	}
	return h.finish(ctx, execution.Job, command, checkpoint)
}

func decodeHistoryCleanupCheckpoint(version int, document json.RawMessage) (JobHistoryCleanupCheckpointV2, error) {
	if version == 1 {
		previous, err := DecodeJobHistoryCleanupCheckpoint(version, document)
		return JobHistoryCleanupCheckpointV2{JobHistoryCleanupCheckpointV1: previous}, err
	}
	var value JobHistoryCleanupCheckpointV2
	if version != 2 {
		return value, fmt.Errorf("unsupported job history cleanup checkpoint version %d", version)
	}
	if err := decodeStrictJobDocument(document, &value); err != nil {
		return value, err
	}
	return value, validateJobHistoryCleanupCheckpoint(value.JobHistoryCleanupCheckpointV1)
}

func (h jobHistoryCleanupHandler) finish(ctx context.Context, parent *model.Job, command JobHistoryCleanupCommandV1, checkpoint JobHistoryCleanupCheckpointV2) jobengine.Outcome {
	if !checkpoint.Done {
		document, err := EncodeJobHistoryCleanupCommand(command)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		at := model.TimeUTC(h.now())
		successor, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, 1, document,
			"job-history-cleanup:after:"+parent.ID.String(), model.JobDedupePermanent, at, at, 5)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		if _, _, err = h.continuations.Enqueue(ctx, &store.JobEnqueue{Job: successor}); err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
	}
	document, marshalErr := json.Marshal(JobHistoryCleanupResultV1{Deleted: checkpoint.Deleted})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: marshalErr}
}

type jobHistoryCleanupProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p jobHistoryCleanupProposer) Propose(ctx context.Context, occurrence time.Time) error {
	at := model.TimeUTC(p.now())
	command, err := EncodeJobHistoryCleanupCommand(JobHistoryCleanupCommandV1{BatchSize: 100})
	if err != nil {
		return err
	}
	key := "job-history-cleanup:" + model.TimeUTC(occurrence).Format("2006-01-02")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeCleanup, 1, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func jobHistoryCleanupDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeCleanup, CommandVersions: []int{1}, CheckpointVersions: []int{1, 2}, ResultVersions: []int{1}, ProgressStages: []string{"completed"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
