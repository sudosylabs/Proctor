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

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	examSittingSealingPageSize    = 100
	examSittingSealingMaximumWork = 1000
)

type ExamSittingSealingCheckpointV1 struct {
	AfterAttemptID model.ExamAttemptID `json:"after_attempt_id,omitempty"`
	Processed      int64               `json:"processed"`
	Sealed         int64               `json:"sealed"`
}

type ExamSittingSealingResultV1 struct {
	Processed int64                  `json:"processed"`
	Sealed    int64                  `json:"sealed"`
	Continued bool                   `json:"continued"`
	State     model.ExamSittingState `json:"state"`
	Revision  int64                  `json:"revision"`
}

type ExamSittingSealingService interface {
	ListExamSittingSealTargetsFromJob(context.Context, model.ExamSittingID, model.ExamAttemptID, int) ([]store.ExamSubmissionAutomaticSealTarget, error)
	SealExamAttemptForSittingCloseFromJob(context.Context, store.ExamSubmissionAutomaticSealTarget, model.JobID, model.JobAttemptID) (examattempt.AutomaticSubmissionResult, error)
	FinishExamSittingSealingFromJob(context.Context, model.ExamSittingID, model.JobID, model.JobAttemptID) (*store.ExamSittingLifecycleResult, error)
	EnqueueExamSittingSealingContinuationFromJob(context.Context, model.ExamSittingID, model.JobID) error
}

type examSittingSealingHandler struct {
	service ExamSittingSealingService
}

func (handler examSittingSealingHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if handler.service == nil || execution.Job == nil || execution.Attempt == nil || !execution.Job.ID.IsValid() ||
		!execution.Attempt.ID.IsValid() {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Exam Sitting sealing execution is incomplete"))
	}
	command, err := model.DecodeExamSittingLifecycleCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	checkpoint, err := decodeExamSittingSealingCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
	if err != nil {
		return jobengine.PermanentFailure("job.checkpoint.invalid", err)
	}
	if execution.Job.WorkReserved < 0 || execution.Job.WorkReserved > examSittingSealingMaximumWork ||
		checkpoint.Processed > examSittingSealingMaximumWork || execution.Job.WorkReserved < int(checkpoint.Processed) ||
		execution.Job.WorkReserved-int(checkpoint.Processed) > 1 {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("Exam Sitting sealing reservation and checkpoint disagree"))
	}
	// ReserveWork is durable and occurrence-wide, while a process can stop before
	// it records the corresponding per-Attempt checkpoint. Burn that one
	// uncertain unit before doing more work. This keeps the hard work cap true
	// whether the domain transaction committed or not; an unsealed Attempt will
	// simply consume a later unit (or a continuation Job) when it is seen again.
	if execution.Job.WorkReserved == int(checkpoint.Processed)+1 {
		checkpoint.Processed++
		if checkpointErr := checkpointExamSittingSealing(ctx, execution, checkpoint); checkpointErr != nil {
			return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
		}
	}
	remaining := examSittingSealingMaximumWork - execution.Job.WorkReserved
	for remaining > 0 {
		limit := min(remaining, examSittingSealingPageSize)
		page, listErr := handler.service.ListExamSittingSealTargetsFromJob(ctx, command.ExamSittingID,
			checkpoint.AfterAttemptID, limit)
		if listErr != nil {
			return examSittingSealingFailure(listErr)
		}
		if len(page) > limit {
			return jobengine.PermanentFailure("job.invariant_failed", errors.New("Exam Sitting sealing returned an oversized page"))
		}
		if len(page) == 0 {
			return handler.finishPass(ctx, execution, command, checkpoint, false)
		}
		for _, target := range page {
			if target.SittingID != command.ExamSittingID || (!checkpoint.AfterAttemptID.IsZero() &&
				target.AttemptID.String() <= checkpoint.AfterAttemptID.String()) {
				return jobengine.PermanentFailure("job.invariant_failed", errors.New("Exam Sitting sealing page is not strictly ordered"))
			}
			reserved, reserveErr := execution.ReserveWork(ctx, 1, examSittingSealingMaximumWork)
			if reserveErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", reserveErr)
			}
			if !reserved {
				return handler.finishPass(ctx, execution, command, checkpoint, true)
			}
			sealed, sealErr := handler.service.SealExamAttemptForSittingCloseFromJob(ctx, target,
				execution.Job.ID, execution.Attempt.ID)
			if sealErr != nil {
				return examSittingSealingFailure(sealErr)
			}
			checkpoint.AfterAttemptID = target.AttemptID
			checkpoint.Processed++
			if !sealed.Replayed {
				checkpoint.Sealed++
			}
			if checkpointErr := checkpointExamSittingSealing(ctx, execution, checkpoint); checkpointErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
			}
			remaining--
			if remaining == 0 {
				break
			}
		}
		if len(page) < limit {
			return handler.finishPass(ctx, execution, command, checkpoint, false)
		}
	}
	return handler.finishPass(ctx, execution, command, checkpoint, true)
}

func (handler examSittingSealingHandler) finishPass(ctx context.Context, execution jobengine.Execution,
	command model.ExamSittingLifecycleCommandV1, checkpoint ExamSittingSealingCheckpointV1, continueIfClosing bool,
) jobengine.Outcome {
	finished := handler.finish(ctx, execution, command.ExamSittingID, checkpoint)
	if finished.Kind != jobengine.OutcomeSucceeded || len(finished.Result) == 0 {
		return finished
	}
	var result ExamSittingSealingResultV1
	if err := json.Unmarshal(finished.Result, &result); err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	if result.State == model.ExamSittingClosed {
		return finished
	}
	if result.State != model.ExamSittingClosing {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("Exam Sitting sealing finished in an invalid state"))
	}
	if !continueIfClosing {
		return jobengine.RetryableFailure("dependency.unavailable",
			errors.New("Exam Sitting sealing has unfinished state without a sealable Attempt"))
	}
	return handler.continuePass(ctx, execution, command, result)
}

func decodeExamSittingSealingCheckpoint(version int, document json.RawMessage) (ExamSittingSealingCheckpointV1, error) {
	var value ExamSittingSealingCheckpointV1
	if len(document) == 0 {
		if version != 0 {
			return value, errors.New("Exam Sitting sealing checkpoint version is invalid")
		}
		return value, nil
	}
	if version != 1 {
		return value, fmt.Errorf("unsupported Exam Sitting sealing checkpoint version %d", version)
	}
	if err := decodeStrictUniqueJobDocument(document, &value); err != nil {
		return value, err
	}
	if (!value.AfterAttemptID.IsZero() && !value.AfterAttemptID.IsValid()) || value.Processed < 0 ||
		value.Sealed < 0 || value.Sealed > value.Processed || (value.AfterAttemptID.IsZero() && value.Sealed != 0) ||
		(!value.AfterAttemptID.IsZero() && value.Processed == 0) {
		return value, errors.New("Exam Sitting sealing checkpoint is invalid")
	}
	return value, nil
}

func checkpointExamSittingSealing(ctx context.Context, execution jobengine.Execution,
	checkpoint ExamSittingSealingCheckpointV1,
) error {
	document, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	return execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1,
		Progress: &model.JobProgress{Current: checkpoint.Processed, Total: examSittingSealingMaximumWork, Stage: "sealing"},
		Document: document})
}

func (handler examSittingSealingHandler) finish(ctx context.Context, execution jobengine.Execution,
	sittingID model.ExamSittingID, checkpoint ExamSittingSealingCheckpointV1,
) jobengine.Outcome {
	result, err := handler.service.FinishExamSittingSealingFromJob(ctx, sittingID, execution.Job.ID, execution.Attempt.ID)
	if err != nil {
		return examSittingSealingFailure(err)
	}
	if err = validateExamSittingLifecycleJobResult(sittingID, result); err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	document, err := json.Marshal(ExamSittingSealingResultV1{Processed: checkpoint.Processed, Sealed: checkpoint.Sealed,
		State: result.Value.Sitting.State, Revision: result.Value.Sitting.Revision})
	if err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document}
}

func (handler examSittingSealingHandler) continuePass(ctx context.Context, execution jobengine.Execution,
	command model.ExamSittingLifecycleCommandV1, result ExamSittingSealingResultV1,
) jobengine.Outcome {
	if err := handler.service.EnqueueExamSittingSealingContinuationFromJob(ctx, command.ExamSittingID,
		execution.Job.ID); err != nil {
		return examSittingSealingFailure(err)
	}
	result.Continued = true
	resultDocument, err := json.Marshal(result)
	if err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: resultDocument}
}

func examSittingSealingFailure(err error) jobengine.Outcome {
	var invalid *store.ErrInvalidInput
	if errors.As(err, &invalid) || store.IsNotFound(err) {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	return jobengine.RetryableFailure("dependency.unavailable", err)
}

func examSittingSealingDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeExamSittingSealing,
		CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1},
		ProgressStages:   []string{"sealing"},
		PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "job.invariant_failed"},
		Timeout:          10 * time.Minute, Concurrency: 4, MaximumAttempts: 8,
		LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second,
		BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute,
		ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed}, Visibility: jobengine.VisibilityOperator,
		SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
