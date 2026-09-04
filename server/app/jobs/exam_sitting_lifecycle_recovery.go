// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	maximumExamSittingLifecycleRecoveryWork = 1000
	examSittingLifecycleRecoveryPageSize    = 100
)

type ExamSittingLifecycleRecoveryCommandV1 struct {
	WorkLimit int `json:"work_limit"`
}

type ExamSittingLifecycleRecoveryCheckpointV1 struct {
	AfterDueAt     time.Time           `json:"after_due_at,omitempty"`
	AfterSittingID model.ExamSittingID `json:"after_sitting_id,omitempty"`
	Processed      int64               `json:"processed"`
	Changed        int64               `json:"changed"`
}

type ExamSittingLifecycleRecoveryResultV1 struct {
	Processed int64 `json:"processed"`
	Changed   int64 `json:"changed"`
}

func EncodeExamSittingLifecycleRecoveryCommand(value ExamSittingLifecycleRecoveryCommandV1) (json.RawMessage, error) {
	if value.WorkLimit < 1 || value.WorkLimit > maximumExamSittingLifecycleRecoveryWork {
		return nil, errors.New("Exam Sitting lifecycle recovery work limit is invalid")
	}
	return json.Marshal(value)
}

func DecodeExamSittingLifecycleRecoveryCommand(version int, document json.RawMessage) (ExamSittingLifecycleRecoveryCommandV1, error) {
	var value ExamSittingLifecycleRecoveryCommandV1
	if version != 1 {
		return value, fmt.Errorf("unsupported Exam Sitting lifecycle recovery command version %d", version)
	}
	if err := decodeStrictUniqueJobDocument(document, &value); err != nil {
		return value, err
	}
	if value.WorkLimit < 1 || value.WorkLimit > maximumExamSittingLifecycleRecoveryWork {
		return value, errors.New("Exam Sitting lifecycle recovery work limit is invalid")
	}
	return value, nil
}

func EncodeExamSittingLifecycleRecoveryCheckpoint(value ExamSittingLifecycleRecoveryCheckpointV1) (json.RawMessage, error) {
	if err := validateExamSittingLifecycleRecoveryCheckpoint(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func DecodeExamSittingLifecycleRecoveryCheckpoint(version int, document json.RawMessage) (ExamSittingLifecycleRecoveryCheckpointV1, error) {
	var value ExamSittingLifecycleRecoveryCheckpointV1
	if version != 1 {
		return value, fmt.Errorf("unsupported Exam Sitting lifecycle recovery checkpoint version %d", version)
	}
	if err := decodeStrictUniqueJobDocument(document, &value); err != nil {
		return value, err
	}
	return value, validateExamSittingLifecycleRecoveryCheckpoint(value)
}

func validateExamSittingLifecycleRecoveryCheckpoint(value ExamSittingLifecycleRecoveryCheckpointV1) error {
	hasTime, hasID := !value.AfterDueAt.IsZero(), !value.AfterSittingID.IsZero()
	if hasTime != hasID || (hasTime && (value.AfterDueAt.Location() != time.UTC || !value.AfterSittingID.IsValid())) ||
		value.Processed < 0 || value.Changed < 0 || value.Changed > value.Processed {
		return errors.New("Exam Sitting lifecycle recovery checkpoint is invalid")
	}
	return nil
}

func decodeStrictUniqueJobDocument(document json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("Job document must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return errors.New("Job document member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("Job document contains a duplicate member")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(document))
	strict.DisallowUnknownFields()
	if err = strict.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = strict.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Job document contains trailing JSON")
		}
		return err
	}
	return nil
}

type ExamSittingLifecycleRecoveryUseCases interface {
	ExamSittingLifecycleReconciler
	ListExamSittingLifecycleDueFromJob(context.Context, store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error)
}

type examSittingLifecycleRecoveryHandler struct {
	service ExamSittingLifecycleRecoveryUseCases
}

func (handler examSittingLifecycleRecoveryHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if handler.service == nil || execution.Job == nil || execution.Attempt == nil ||
		!execution.Job.ID.IsValid() || !execution.Attempt.ID.IsValid() {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Exam Sitting lifecycle recovery execution is incomplete"))
	}
	command, err := DecodeExamSittingLifecycleRecoveryCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint := ExamSittingLifecycleRecoveryCheckpointV1{}
	if len(execution.Job.Checkpoint) != 0 {
		checkpoint, err = DecodeExamSittingLifecycleRecoveryCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
		if err != nil {
			return jobengine.PermanentFailure("job.checkpoint.invalid", err)
		}
	}
	if checkpoint.Processed > int64(command.WorkLimit) || execution.Job.WorkReserved < int(checkpoint.Processed) || execution.Job.WorkReserved > command.WorkLimit {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("Exam Sitting lifecycle recovery reservation and checkpoint disagree"))
	}

	remaining := command.WorkLimit - execution.Job.WorkReserved
	for remaining > 0 {
		limit := min(remaining, examSittingLifecycleRecoveryPageSize)
		page, listErr := handler.service.ListExamSittingLifecycleDueFromJob(ctx, store.ExamSittingLifecycleDueOptions{
			AfterDueAt: checkpoint.AfterDueAt, AfterSittingID: checkpoint.AfterSittingID, Limit: limit,
		})
		if listErr != nil {
			return examSittingLifecycleFailure(listErr)
		}
		if len(page) > limit {
			return jobengine.PermanentFailure("job.invariant_failed", errors.New("Exam Sitting lifecycle recovery returned an oversized page"))
		}
		if len(page) == 0 {
			break
		}
		for _, due := range page {
			if err = validateExamSittingLifecycleDue(checkpoint, due); err != nil {
				return jobengine.PermanentFailure("job.invariant_failed", err)
			}
			reserved, reserveErr := execution.ReserveWork(ctx, 1, command.WorkLimit)
			if reserveErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", reserveErr)
			}
			if !reserved {
				return examSittingLifecycleRecoverySucceeded(checkpoint)
			}
			result, reconcileErr := handler.service.ReconcileExamSittingLifecycleFromJob(
				ctx, due.Value.Sitting.ID, execution.Job.ID, execution.Attempt.ID,
			)
			if reconcileErr != nil {
				return examSittingLifecycleFailure(reconcileErr)
			}
			if err = validateExamSittingLifecycleJobResult(due.Value.Sitting.ID, result); err != nil {
				return jobengine.PermanentFailure("job.invariant_failed", err)
			}
			checkpoint.AfterDueAt, checkpoint.AfterSittingID = due.DueAt, due.Value.Sitting.ID
			checkpoint.Processed++
			if result.Changed {
				checkpoint.Changed++
			}
			document, encodeErr := EncodeExamSittingLifecycleRecoveryCheckpoint(checkpoint)
			if encodeErr != nil {
				return jobengine.PermanentFailure("job.invariant_failed", encodeErr)
			}
			if checkpointErr := execution.Checkpoint(ctx, jobengine.CheckpointValue{
				Version:  1,
				Progress: &model.JobProgress{Current: checkpoint.Processed, Total: int64(command.WorkLimit), Stage: "reconciling"},
				Document: document,
			}); checkpointErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
			}
			remaining--
			if remaining == 0 {
				break
			}
		}
		if len(page) < limit {
			break
		}
	}
	return examSittingLifecycleRecoverySucceeded(checkpoint)
}

func validateExamSittingLifecycleDue(after ExamSittingLifecycleRecoveryCheckpointV1, due store.ExamSittingLifecycleDue) error {
	if due.Value == nil || due.Value.Sitting == nil || due.DueAt.IsZero() || due.DueAt.Location() != time.UTC ||
		due.Value.Sitting.Validate() != nil {
		return errors.New("Exam Sitting lifecycle recovery returned an invalid due Sitting")
	}
	if !after.AfterDueAt.IsZero() && (due.DueAt.Before(after.AfterDueAt) ||
		(due.DueAt.Equal(after.AfterDueAt) && due.Value.Sitting.ID.String() <= after.AfterSittingID.String())) {
		return errors.New("Exam Sitting lifecycle recovery page is not strictly ordered")
	}
	return nil
}

func examSittingLifecycleRecoverySucceeded(checkpoint ExamSittingLifecycleRecoveryCheckpointV1) jobengine.Outcome {
	document, err := json.Marshal(ExamSittingLifecycleRecoveryResultV1{Processed: checkpoint.Processed, Changed: checkpoint.Changed})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type examSittingLifecycleRecoveryProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (proposer examSittingLifecycleRecoveryProposer) Propose(ctx context.Context, occurrence time.Time) error {
	if proposer.jobs == nil || proposer.now == nil {
		return errors.New("Exam Sitting lifecycle recovery proposer dependencies are required")
	}
	command, err := EncodeExamSittingLifecycleRecoveryCommand(ExamSittingLifecycleRecoveryCommandV1{WorkLimit: maximumExamSittingLifecycleRecoveryWork})
	if err != nil {
		return err
	}
	at := model.TimeUTC(proposer.now())
	key := "exam-sitting-lifecycle-recovery:" + model.TimeUTC(occurrence).Format("2006-01-02")
	record, err := model.NewJobWithDedupePolicy(
		model.NewJobID(), model.JobTypeExamSittingLifecycleRecovery, 1, command, key,
		model.JobDedupePermanent, at, at, 5,
	)
	if err != nil {
		return err
	}
	_, _, err = proposer.jobs.Enqueue(ctx, &store.JobEnqueue{Job: record})
	return err
}

func examSittingLifecycleRecoveryDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{
		Type:            model.JobTypeExamSittingLifecycleRecovery,
		CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1},
		ProgressStages:   []string{"reconciling"},
		PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"},
		Timeout:          10 * time.Minute, Concurrency: 1, MaximumAttempts: 5,
		LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second,
		BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute,
		ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed},
		Visibility:            jobengine.VisibilityOperator,
		SuccessRetention:      30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour,
		Handler: handler,
	}
}
