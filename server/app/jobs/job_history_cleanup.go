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
	jobs     JobHistoryCleaner
	policies []store.JobRetentionPolicy
}

func (h jobHistoryCleanupHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	command, err := DecodeJobHistoryCleanupCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint := JobHistoryCleanupCheckpointV1{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = DecodeJobHistoryCleanupCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
		document, marshalErr := json.Marshal(JobHistoryCleanupResultV1{Deleted: checkpoint.Deleted})
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: marshalErr}
	}
	remaining := command.BatchSize - execution.Job.WorkReserved
	if remaining <= 0 {
		document, marshalErr := json.Marshal(JobHistoryCleanupResultV1{})
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: marshalErr}
	}
	reserved, reserveErr := execution.ReserveWork(ctx, remaining, command.BatchSize)
	if reserveErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", reserveErr)
	}
	if !reserved {
		document, marshalErr := json.Marshal(JobHistoryCleanupResultV1{})
		return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: marshalErr}
	}
	page, deleteErr := h.jobs.DeleteTerminalHistory(ctx, &store.JobHistoryCleanup{ExcludeJobID: execution.Job.ID, Policies: h.policies, Limit: remaining})
	if deleteErr != nil {
		return jobengine.RetryableFailure("dependency.unavailable", deleteErr)
	}
	checkpoint.Deleted = page.Deleted
	checkpoint.AfterCompletedAt = page.LastCompletedAt
	checkpoint.AfterJobID = page.LastJobID
	if page.Deleted > 0 {
		document, encodeErr := EncodeJobHistoryCleanupCheckpoint(checkpoint)
		if encodeErr != nil {
			return jobengine.PermanentFailure("job.invariant_failed", encodeErr)
		}
		if checkpointErr := execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Progress: &model.JobProgress{Current: checkpoint.Deleted, Total: checkpoint.Deleted, Stage: "completed"}, Document: document}); checkpointErr != nil {
			return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
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
	return jobengine.Descriptor{Type: model.JobTypeCleanup, CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1}, ProgressStages: []string{"completed"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
