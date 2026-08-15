// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingLifecycleJobFactoryFencesEachBoundaryByRevision(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	startAt, endAt := at.Add(time.Hour), at.Add(2*time.Hour)
	sittingID := model.NewExamSittingID()
	factory := examSittingLifecycleJobFactory{now: func() time.Time { return at }, newID: model.NewJobID}

	openJob, deadlineJob, err := factory.BoundaryJobs(sittingID, 4, startAt, endAt)
	if err != nil {
		t.Fatal(err)
	}
	assertExamSittingLifecycleJob(t, openJob, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobOpen, 4, at, startAt)
	assertExamSittingLifecycleJob(t, deadlineJob, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobDeadline, 4, at, endAt)

	nextDeadline := endAt.Add(time.Hour)
	extended, err := factory.DeadlineJob(sittingID, 5, nextDeadline)
	if err != nil {
		t.Fatal(err)
	}
	assertExamSittingLifecycleJob(t, extended, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobDeadline, 5, at, nextDeadline)

	finalize, err := factory.FinalizeJob(sittingID, 6, nextDeadline)
	if err != nil {
		t.Fatal(err)
	}
	assertExamSittingLifecycleJob(t, finalize, model.JobTypeExamSittingSealing, sittingID,
		model.ExamSittingLifecycleJobFinalize, 6, at, nextDeadline)
}

func TestExamSittingLifecycleHandlerReconcilesAndTreatsStaleWorkAsSuccess(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	job := mustExamSittingLifecycleJob(t, sittingID, at)
	attemptID := model.NewJobAttemptID()
	reconciler := &examSittingLifecycleReconcilerFake{result: lifecycleJobResult(t, sittingID, false)}
	handler := examSittingLifecycleHandler{reconciler: reconciler}

	outcome := handler.Run(context.Background(), jobengine.NewExecution(job, &model.JobAttempt{ID: attemptID}, nil, nil))
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.ResultVersion != 1 || outcome.Err != nil {
		t.Fatalf("outcome = %#v", outcome)
	}
	if reconciler.sittingID != sittingID || reconciler.jobID != job.ID || reconciler.attemptID != attemptID {
		t.Fatalf("reconcile identity = %s / %s / %s", reconciler.sittingID, reconciler.jobID, reconciler.attemptID)
	}
	var result ExamSittingLifecycleJobResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || result.Changed || result.State != model.ExamSittingScheduled || result.Revision != 1 {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

func TestExamSittingLifecycleHandlerClassifiesFailures(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	valid := mustExamSittingLifecycleJob(t, sittingID, at)
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID()}

	tests := []struct {
		name string
		job  *model.Job
		err  error
		kind jobengine.OutcomeKind
		code string
	}{
		{name: "invalid command", job: func() *model.Job {
			copy := *valid
			copy.Command = json.RawMessage(`{"exam_sitting_id":null}`)
			return &copy
		}(), kind: jobengine.OutcomePermanentFailure, code: "job.command.invalid"},
		{name: "invalid durable request", job: valid, err: store.NewErrInvalidInput("exam_sitting", "advance_due", nil), kind: jobengine.OutcomePermanentFailure, code: "job.invariant_failed"},
		{name: "missing durable target", job: valid, err: store.NewErrNotFound("exam_sitting", sittingID.String()), kind: jobengine.OutcomePermanentFailure, code: "job.invariant_failed"},
		{name: "dependency failure", job: valid, err: errors.New("database unavailable"), kind: jobengine.OutcomeRetryableFailure, code: "dependency.unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &examSittingLifecycleReconcilerFake{err: tt.err}
			outcome := (examSittingLifecycleHandler{reconciler: reconciler}).Run(context.Background(), jobengine.NewExecution(tt.job, attempt, nil, nil))
			if outcome.Kind != tt.kind || outcome.PublicErrorCode != tt.code {
				t.Fatalf("outcome = %#v", outcome)
			}
		})
	}
}

func TestExamSittingLifecycleDescriptorIsBoundedAndNonCancelable(t *testing.T) {
	t.Parallel()
	descriptor := examSittingLifecycleDescriptor(examSittingLifecycleHandler{reconciler: &examSittingLifecycleReconcilerFake{}})
	if descriptor.Type != model.JobTypeExamSittingLifecycle || descriptor.Cancelable || descriptor.Visibility != jobengine.VisibilityOperator ||
		descriptor.MaximumAttempts != 8 || descriptor.Concurrency < 1 || len(descriptor.CommandVersions) != 1 || len(descriptor.ResultVersions) != 1 {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

type examSittingLifecycleReconcilerFake struct {
	result    *store.ExamSittingLifecycleResult
	err       error
	sittingID model.ExamSittingID
	jobID     model.JobID
	attemptID model.JobAttemptID
}

func (fake *examSittingLifecycleReconcilerFake) ReconcileExamSittingLifecycleFromJob(_ context.Context, sittingID model.ExamSittingID, jobID model.JobID, attemptID model.JobAttemptID) (*store.ExamSittingLifecycleResult, error) {
	fake.sittingID, fake.jobID, fake.attemptID = sittingID, jobID, attemptID
	return fake.result, fake.err
}

func mustExamSittingLifecycleJob(t *testing.T, sittingID model.ExamSittingID, at time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command, "exam-sitting-test", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func lifecycleJobResult(t *testing.T, sittingID model.ExamSittingID, changed bool) *store.ExamSittingLifecycleResult {
	t.Helper()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(sittingID, model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	result := &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: sitting}, Changed: changed}
	if changed {
		if err = sitting.Open(at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		result.Transition = store.ExamSittingTransitionOpened
	}
	return result
}

func assertExamSittingLifecycleJob(t *testing.T, job *model.Job, wantType model.JobType,
	sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase, revision int64,
	createdAt, availableAt time.Time,
) {
	t.Helper()
	if job == nil || job.Type != wantType || job.DedupePolicy != model.JobDedupeActive ||
		job.Status != model.JobStatusQueued || job.CommandVersion != 1 || job.MaximumAttempts != 8 || job.CreatedAt != createdAt || job.AvailableAt != availableAt {
		t.Fatalf("lifecycle Job = %#v", job)
	}
	wantKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
	if err != nil {
		t.Fatal(err)
	}
	if job.DedupeKey != wantKey {
		t.Fatalf("dedupe key = %q, want %q", job.DedupeKey, wantKey)
	}
	command, err := model.DecodeExamSittingLifecycleCommand(job.CommandVersion, job.Command)
	if err != nil || command.ExamSittingID != sittingID {
		t.Fatalf("command = %#v, %v", command, err)
	}
}
