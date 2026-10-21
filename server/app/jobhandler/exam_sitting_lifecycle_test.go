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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingLifecycleJobFactoryFencesEachBoundaryByRevision(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	startAt, endAt := at.Add(time.Hour), at.Add(2*time.Hour)
	sittingID := model.NewExamSittingID()
	factory := NewExamSittingLifecycleJobFactory(func() time.Time { return at }, model.NewJobID)

	openJob, deadlineJob, err := factory.BoundaryJobs(sittingID, 4, startAt, endAt)
	require.NoError(t, err)
	assertExamSittingLifecycleJob(t, openJob, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobOpen, 4, at, startAt)
	assertExamSittingLifecycleJob(t, deadlineJob, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobDeadline, 4, at, endAt)

	nextDeadline := endAt.Add(time.Hour)
	extended, err := factory.DeadlineJob(sittingID, 5, nextDeadline)
	require.NoError(t, err)
	assertExamSittingLifecycleJob(t, extended, model.JobTypeExamSittingLifecycle, sittingID,
		model.ExamSittingLifecycleJobDeadline, 5, at, nextDeadline)

	finalize, err := factory.FinalizeJob(sittingID, 6, nextDeadline)
	require.NoError(t, err)
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
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	require.Equal(t, 1, outcome.ResultVersion)
	require.NoError(t, outcome.Err)
	require.Equal(t, sittingID, reconciler.sittingID, "reconcile identity")
	require.Equal(t, job.ID, reconciler.jobID, "reconcile identity")
	require.Equal(t, attemptID, reconciler.attemptID, "reconcile identity")
	var result ExamSittingLifecycleJobResultV1
	require.NoError(t, json.Unmarshal(outcome.Result, &result), "result = %#v", result)
	require.False(t, result.Changed, "result = %#v", result)
	require.Equal(t, model.ExamSittingScheduled, result.State, "result = %#v", result)
	require.Equal(t, int64(1), result.Revision, "result = %#v", result)
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
		{name: "database constraint rejection", job: valid, err: store.NewErrConstraintViolation("exam_sitting", "exam_sittings_lifecycle_check"), kind: jobengine.OutcomeRetryableFailure, code: "dependency.unavailable"},
		{name: "missing durable target", job: valid, err: store.NewErrNotFound("exam_sitting", sittingID.String()), kind: jobengine.OutcomePermanentFailure, code: "job.invariant_failed"},
		{name: "dependency failure", job: valid, err: errors.New("database unavailable"), kind: jobengine.OutcomeRetryableFailure, code: "dependency.unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &examSittingLifecycleReconcilerFake{err: tt.err}
			outcome := (examSittingLifecycleHandler{reconciler: reconciler}).Run(context.Background(), jobengine.NewExecution(tt.job, attempt, nil, nil))
			require.Equal(t, tt.kind, outcome.Kind)
			require.Equal(t, tt.code, outcome.PublicErrorCode)
		})
	}
}

func TestExamSittingLifecycleDescriptorIsBoundedAndNonCancelable(t *testing.T) {
	t.Parallel()
	descriptor := examSittingLifecycleDescriptor(examSittingLifecycleHandler{reconciler: &examSittingLifecycleReconcilerFake{}})
	require.Equal(t, model.JobTypeExamSittingLifecycle, descriptor.Type)
	require.False(t, descriptor.Cancelable)
	require.Equal(t, jobengine.VisibilityOperator, descriptor.Visibility)
	require.Equal(t, 8, descriptor.MaximumAttempts)
	require.GreaterOrEqual(t, descriptor.Concurrency, 1)
	require.Len(t, descriptor.CommandVersions, 1)
	require.Len(t, descriptor.ResultVersions, 1)
}

type examSittingLifecycleReconcilerFake struct {
	result    *store.ExamSittingLifecycleResult
	err       error
	sittingID model.ExamSittingID
	jobID     model.JobID
	attemptID model.JobAttemptID
}

func (f *examSittingLifecycleReconcilerFake) ReconcileExamSittingLifecycleFromJob(_ context.Context, sittingID model.ExamSittingID, jobID model.JobID, attemptID model.JobAttemptID) (*store.ExamSittingLifecycleResult, error) {
	f.sittingID, f.jobID, f.attemptID = sittingID, jobID, attemptID
	return f.result, f.err
}

func mustExamSittingLifecycleJob(t *testing.T, sittingID model.ExamSittingID, at time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamSittingLifecycle, 1, command, "exam-sitting-test", at, at, 8)
	require.NoError(t, err)
	return job
}

func lifecycleJobResult(t *testing.T, sittingID model.ExamSittingID, changed bool) *store.ExamSittingLifecycleResult {
	t.Helper()
	at := time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC)
	sitting, err := model.NewExamSitting(sittingID, model.NewExamID(), model.NewExamRevisionID(), model.NewClassID(), at.Add(time.Hour), at.Add(2*time.Hour), at)
	require.NoError(t, err)
	result := &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: sitting}, Changed: changed}
	if changed {
		err = sitting.Open(at.Add(time.Hour))
		require.NoError(t, err)
		result.Transition = store.ExamSittingTransitionOpened
	}
	return result
}

func assertExamSittingLifecycleJob(t *testing.T, job *model.Job, wantType model.JobType,
	sittingID model.ExamSittingID, phase model.ExamSittingLifecycleJobPhase, revision int64,
	createdAt, availableAt time.Time,
) {
	t.Helper()
	require.NotNil(t, job)
	require.Equal(t, wantType, job.Type)
	require.Equal(t, model.JobDedupeActive, job.DedupePolicy)
	require.Equal(t, model.JobStatusQueued, job.Status)
	require.Equal(t, 1, job.CommandVersion)
	require.Equal(t, 8, job.MaximumAttempts)
	require.Equal(t, createdAt, job.CreatedAt)
	require.Equal(t, availableAt, job.AvailableAt)
	wantKey, err := model.ExamSittingLifecycleDedupeKey(sittingID, phase, revision)
	require.NoError(t, err)
	require.Equal(t, wantKey, job.DedupeKey, "dedupe key")
	command, err := model.DecodeExamSittingLifecycleCommand(job.CommandVersion, job.Command)
	require.NoError(t, err, "command = %#v", command)
	require.Equal(t, sittingID, command.ExamSittingID, "command = %#v", command)
}
