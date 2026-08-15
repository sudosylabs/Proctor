// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examSittingLifecycleReconciler interface {
	ReconcileExamSittingLifecycleFromJob(context.Context, model.ExamSittingID, model.JobID, model.JobAttemptID) (*store.ExamSittingLifecycleResult, error)
}

// ExamSittingLifecycleJobResultV1 is deliberately limited to safe lifecycle
// state. The immutable command, audit documents, and private manager reasons
// never enter the generic Job result.
type ExamSittingLifecycleJobResultV1 struct {
	Changed    bool                                     `json:"changed"`
	Transition store.ExamSittingLifecycleTransitionCode `json:"transition,omitempty"`
	State      model.ExamSittingState                   `json:"state"`
	Revision   int64                                    `json:"revision"`
}

type examSittingLifecycleHandler struct {
	reconciler examSittingLifecycleReconciler
}

func (handler examSittingLifecycleHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if handler.reconciler == nil || execution.Job == nil || execution.Attempt == nil ||
		!execution.Job.ID.IsValid() || !execution.Attempt.ID.IsValid() {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("Exam Sitting lifecycle Job execution is incomplete"))
	}
	command, err := model.DecodeExamSittingLifecycleCommand(execution.Job.CommandVersion, execution.Job.Command)
	if err != nil {
		return jobengine.PermanentFailure("job.command.invalid", err)
	}
	result, err := handler.reconciler.ReconcileExamSittingLifecycleFromJob(
		ctx, command.ExamSittingID, execution.Job.ID, execution.Attempt.ID,
	)
	if err != nil {
		return examSittingLifecycleFailure(err)
	}
	if err = validateExamSittingLifecycleJobResult(command.ExamSittingID, result); err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	document, err := json.Marshal(ExamSittingLifecycleJobResultV1{
		Changed: result.Changed, Transition: result.Transition,
		State: result.Value.Sitting.State, Revision: result.Value.Sitting.Revision,
	})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

func validateExamSittingLifecycleJobResult(sittingID model.ExamSittingID, result *store.ExamSittingLifecycleResult) error {
	if result == nil || result.Value == nil || result.Value.Sitting == nil || result.Value.Sitting.ID != sittingID {
		return errors.New("Exam Sitting lifecycle reconciliation returned an incomplete result")
	}
	if err := result.Value.Sitting.Validate(); err != nil {
		return err
	}
	if result.Changed != (result.Transition != "") || (result.Transition != "" && !validExamSittingLifecycleTransition(result.Transition)) {
		return errors.New("Exam Sitting lifecycle reconciliation returned an invalid transition")
	}
	return nil
}

func validExamSittingLifecycleTransition(value store.ExamSittingLifecycleTransitionCode) bool {
	switch value {
	case store.ExamSittingTransitionOpened,
		store.ExamSittingTransitionManagerPaused,
		store.ExamSittingTransitionManagerResumed,
		store.ExamSittingTransitionManagerExtended,
		store.ExamSittingTransitionManagerClosed,
		store.ExamSittingTransitionAcademicStructureInvalid,
		store.ExamSittingTransitionScheduleElapsed,
		store.ExamSittingTransitionScheduledEndReached,
		store.ExamSittingTransitionClosedNoAttempts,
		store.ExamSittingTransitionSealingCompleted:
		return true
	default:
		return false
	}
}

func examSittingLifecycleFailure(err error) jobengine.Outcome {
	var invalid *store.ErrInvalidInput
	if errors.As(err, &invalid) || store.IsNotFound(err) {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	return jobengine.RetryableFailure("dependency.unavailable", err)
}

func examSittingLifecycleDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{
		Type:            model.JobTypeExamSittingLifecycle,
		CommandVersions: []int{1}, ResultVersions: []int{1},
		PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid", "job.invariant_failed"},
		Timeout:          time.Minute, Concurrency: 4, MaximumAttempts: 8,
		LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second,
		BaseRetryDelay: time.Second, MaximumRetryDelay: 30 * time.Second,
		ExplicitRetryStatuses: []model.JobStatus{model.JobStatusFailed},
		Visibility:            jobengine.VisibilityOperator,
		SuccessRetention:      30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour,
		Handler: handler,
	}
}

type examSittingLifecycleJobFactory struct {
	now   func() time.Time
	newID func() model.JobID
}

func (factory examSittingLifecycleJobFactory) BoundaryJobs(sittingID model.ExamSittingID, resultingRevision int64, startAt, endAt time.Time) (*model.Job, *model.Job, error) {
	openJob, err := factory.job(sittingID, model.ExamSittingLifecycleJobOpen, resultingRevision, startAt)
	if err != nil {
		return nil, nil, err
	}
	deadlineJob, err := factory.job(sittingID, model.ExamSittingLifecycleJobDeadline, resultingRevision, endAt)
	if err != nil {
		return nil, nil, err
	}
	return openJob, deadlineJob, nil
}

func (factory examSittingLifecycleJobFactory) DeadlineJob(sittingID model.ExamSittingID, resultingRevision int64, deadline time.Time) (*model.Job, error) {
	return factory.job(sittingID, model.ExamSittingLifecycleJobDeadline, resultingRevision, deadline)
}

func (factory examSittingLifecycleJobFactory) FinalizeJob(sittingID model.ExamSittingID, resultingRevision int64, availableAt time.Time) (*model.Job, error) {
	return factory.jobOfType(sittingID, model.ExamSittingLifecycleJobFinalize, resultingRevision, availableAt, model.JobTypeExamSittingSealing)
}

func (factory examSittingLifecycleJobFactory) job(sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase, resultingRevision int64, availableAt time.Time) (*model.Job, error) {
	return factory.jobOfType(sittingID, phase, resultingRevision, availableAt, model.JobTypeExamSittingLifecycle)
}

func (factory examSittingLifecycleJobFactory) jobOfType(sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase,
	resultingRevision int64, availableAt time.Time, jobType model.JobType,
) (*model.Job, error) {
	if factory.now == nil || factory.newID == nil {
		return nil, errors.New("Exam Sitting lifecycle Job dependencies are required")
	}
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		return nil, err
	}
	dedupeKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, phase, resultingRevision)
	if err != nil {
		return nil, err
	}
	at := model.TimeUTC(factory.now())
	return model.NewJob(factory.newID(), jobType, 1, command, dedupeKey, at, model.TimeUTC(availableAt), 8)
}
