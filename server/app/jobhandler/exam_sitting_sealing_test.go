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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamSittingSealingJobCheckpointsEachAttemptThenCloses(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	targets := automaticTargetsForSealingTest(sitting, 2)
	service := &examSittingSealingFake{targets: targets, finish: sealingLifecycleResult(sitting, true)}
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	var checkpoints []jobengine.CheckpointValue
	execution := jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()},
		func(_ context.Context, value jobengine.CheckpointValue) error {
			checkpoints = append(checkpoints, value)
			return nil
		}, allowJobWorkReservation())
	outcome := (examSittingSealingHandler{service: service}).Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	require.NoError(t, outcome.Err, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	require.Len(t, service.sealed, 2, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	require.Len(t, checkpoints, 2, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	require.Equal(t, 1, service.finishCalls, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	require.Equal(t, 0, service.continuationCalls, "outcome=%#v sealed=%d checkpoints=%d finish=%d continuations", outcome, len(service.sealed), len(checkpoints), service.finishCalls)
	var result ExamSittingSealingResult
	err := json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result=%#v err", result)
	require.Equal(t, int64(2), result.Processed, "result=%#v err", result)
	require.Equal(t, int64(2), result.Sealed, "result=%#v err", result)
	require.False(t, result.Continued, "result=%#v err", result)
	require.Equal(t, model.ExamSittingClosed, result.State, "result=%#v err", result)
	checkpoint, err := decodeExamSittingSealingCheckpoint(checkpoints[1].Document)
	require.NoError(t, err, "checkpoint=%#v err", checkpoint)
	require.Equal(t, targets[1].AttemptID, checkpoint.AfterAttemptID, "checkpoint=%#v err", checkpoint)
	require.Equal(t, int64(2), checkpoint.Processed, "checkpoint=%#v err", checkpoint)
	require.Equal(t, int64(2), checkpoint.Sealed, "checkpoint=%#v err", checkpoint)
}

func TestExamSittingSealingJobBoundsPassAndDurablyContinues(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	service := &examSittingSealingFake{targets: automaticTargetsForSealingTest(sitting, examSittingSealingMaximumWork+1),
		finish: sealingLifecycleResult(sitting, false)}
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	outcome := (examSittingSealingHandler{service: service}).Run(
		context.Background(), jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()},
			func(context.Context, jobengine.CheckpointValue) error { return nil }, allowJobWorkReservation()))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	require.Len(t, service.sealed, examSittingSealingMaximumWork, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	require.Equal(t, 1, service.finishCalls, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	require.Equal(t, 1, service.continuationCalls, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	require.Equal(t, sitting.ID, service.continuationSittingID, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	require.Equal(t, job.ID, service.continuationParentID, "outcome=%#v sealed=%d finish=%d continuations", outcome, len(service.sealed), service.finishCalls)
	var result ExamSittingSealingResult
	err := json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result=%#v err", result)
	require.True(t, result.Continued, "result=%#v err", result)
	require.Equal(t, model.ExamSittingClosing, result.State, "result=%#v err", result)
	require.Equal(t, sitting.Revision, result.Revision, "result=%#v err", result)
	require.Equal(t, int64(examSittingSealingMaximumWork), result.Processed, "result=%#v err", result)
	require.Equal(t, int64(examSittingSealingMaximumWork), result.Sealed, "result=%#v err", result)
}

func TestExamSittingSealingDescriptorIsBoundedCheckpointedAndNonCancelable(t *testing.T) {
	t.Parallel()
	descriptor := examSittingSealingDescriptor(examSittingSealingHandler{})
	require.Equal(t, model.JobTypeExamSittingSealing, descriptor.Type)
	require.False(t, descriptor.Cancelable)
	require.Equal(t, jobengine.VisibilityOperator, descriptor.Visibility)
	require.True(t, descriptor.Checkpoints)
	require.Len(t, descriptor.ProgressStages, 1)
	require.Equal(t, "sealing", descriptor.ProgressStages[0])
	require.Equal(t, 8, descriptor.MaximumAttempts)
}

func TestExamSittingSealingJobLeavesUnfinishedClosingStateRetryable(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	service := &examSittingSealingFake{finish: sealingLifecycleResult(sitting, false)}
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	outcome := (examSittingSealingHandler{service: service}).Run(context.Background(),
		jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, allowJobWorkReservation()))
	require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind, "outcome=%#v finish", outcome)
	require.Equal(t, "dependency.unavailable", outcome.PublicErrorCode, "outcome=%#v finish", outcome)
	require.Equal(t, 1, service.finishCalls, "outcome=%#v finish", outcome)
}

func TestExamSittingSealingJobRetriesDatabaseConstraintRejection(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	for _, test := range []struct {
		name string
		err  error
		kind jobengine.OutcomeKind
		code string
	}{
		{"database constraint rejection", store.NewErrConstraintViolation("exam_sitting", "exam_sittings_lifecycle_check"), jobengine.OutcomeRetryableFailure, "dependency.unavailable"},
		{"refused input", store.NewErrInvalidInput("exam_sitting", "finish", nil), jobengine.OutcomePermanentFailure, "job.invariant_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &examSittingSealingFake{finishErr: test.err}
			outcome := (examSittingSealingHandler{service: service}).Run(context.Background(),
				jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, allowJobWorkReservation()))
			require.Equal(t, test.kind, outcome.Kind, "outcome=%#v", outcome)
			require.Equal(t, test.code, outcome.PublicErrorCode, "outcome=%#v", outcome)
		})
	}
}

func TestExamSittingSealingJobRecoversReservationAheadOfCheckpoint(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	targets := automaticTargetsForSealingTest(sitting, 1)
	service := &examSittingSealingFake{targets: targets, finish: sealingLifecycleResult(sitting, true)}
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	job.WorkReserved = 1
	var checkpoints []ExamSittingSealingCheckpoint
	reserved := 0
	execution := jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()},
		func(_ context.Context, value jobengine.CheckpointValue) error {
			checkpoint, err := decodeExamSittingSealingCheckpoint(value.Document)
			require.NoError(t, err)
			checkpoints = append(checkpoints, checkpoint)
			return nil
		}, func(_ context.Context, units, limit int) (bool, error) {
			require.Equal(t, 1, units, "reservation units")
			require.Equal(t, examSittingSealingMaximumWork, limit, "reservation units")
			reserved += units
			return true, nil
		})
	outcome := (examSittingSealingHandler{service: service}).Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "outcome=%#v reserved=%d sealed=%d checkpoints", outcome, reserved, len(service.sealed))
	require.Equal(t, 1, reserved, "outcome=%#v reserved=%d sealed=%d checkpoints", outcome, reserved, len(service.sealed))
	require.Len(t, service.sealed, 1, "outcome=%#v reserved=%d sealed=%d checkpoints", outcome, reserved, len(service.sealed))
	require.Len(t, checkpoints, 2, "outcome=%#v reserved=%d sealed=%d checkpoints", outcome, reserved, len(service.sealed))
	require.Equal(t, int64(1), checkpoints[0].Processed)
	require.Equal(t, int64(0), checkpoints[0].Sealed)
	require.True(t, checkpoints[0].AfterAttemptID.IsZero())
	require.Equal(t, int64(2), checkpoints[1].Processed)
	require.Equal(t, int64(1), checkpoints[1].Sealed)
	require.Equal(t, targets[0].AttemptID, checkpoints[1].AfterAttemptID)
	var result ExamSittingSealingResult
	err := json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result=%#v err", result)
	require.Equal(t, int64(2), result.Processed, "result=%#v err", result)
	require.Equal(t, int64(1), result.Sealed, "result=%#v err", result)
	require.Equal(t, model.ExamSittingClosed, result.State, "result=%#v err", result)
}

type examSittingSealingFake struct {
	targets               []store.ExamSubmissionAutomaticSealTarget
	sealed                []model.ExamAttemptID
	finish                *store.ExamSittingLifecycleResult
	finishCalls           int
	finishErr             error
	continuationCalls     int
	continuationSittingID model.ExamSittingID
	continuationParentID  model.JobID
}

func (f *examSittingSealingFake) ListExamSittingSealTargetsFromJob(_ context.Context, _ model.ExamSittingID,
	after model.ExamAttemptID, limit int,
) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	start := 0
	if !after.IsZero() {
		for index := range f.targets {
			if f.targets[index].AttemptID == after {
				start = index + 1
				break
			}
		}
	}
	end := min(start+limit, len(f.targets))
	return append([]store.ExamSubmissionAutomaticSealTarget(nil), f.targets[start:end]...), nil
}

func (f *examSittingSealingFake) SealExamAttemptForSittingCloseFromJob(_ context.Context,
	target store.ExamSubmissionAutomaticSealTarget, _ model.JobID, _ model.JobAttemptID,
) (examattempt.AutomaticSubmissionResult, error) {
	f.sealed = append(f.sealed, target.AttemptID)
	return examattempt.AutomaticSubmissionResult{}, nil
}

func (f *examSittingSealingFake) FinishExamSittingSealingFromJob(context.Context, model.ExamSittingID,
	model.JobID, model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	f.finishCalls++
	return f.finish, f.finishErr
}

func (f *examSittingSealingFake) EnqueueExamSittingSealingContinuationFromJob(_ context.Context,
	sittingID model.ExamSittingID, parentID model.JobID,
) error {
	f.continuationCalls++
	f.continuationSittingID, f.continuationParentID = sittingID, parentID
	return nil
}

func mustExamSittingSealingJob(t *testing.T, sittingID model.ExamSittingID, at time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommand{ExamSittingID: sittingID})
	require.NoError(t, err)
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamSittingSealing, command, "sealing-test", at, at, 8)
	require.NoError(t, err)
	return job
}

func closingSittingForSealingTest(t *testing.T, at time.Time) *model.ExamSitting {
	t.Helper()
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(),
		model.NewClassID(), at.Add(-2*time.Hour), at.Add(-time.Hour), at.Add(-3*time.Hour))
	require.NoError(t, err)
	err = sitting.Open(at.Add(-2 * time.Hour))
	require.NoError(t, err)
	err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, at.Add(-time.Hour))
	require.NoError(t, err)
	return sitting
}

func sealingLifecycleResult(sitting *model.ExamSitting, closed bool) *store.ExamSittingLifecycleResult {
	copy := *sitting
	result := &store.ExamSittingLifecycleResult{Value: &store.ExamSittingSnapshot{Sitting: &copy}}
	if closed {
		_ = copy.Close(copy.ClosingAt.Time.Add(time.Second))
		result.Changed, result.Transition = true, store.ExamSittingTransitionSealingCompleted
	}
	return result
}

func automaticTargetsForSealingTest(sitting *model.ExamSitting, count int) []store.ExamSubmissionAutomaticSealTarget {
	items := make([]store.ExamSubmissionAutomaticSealTarget, count)
	for index := range items {
		items[index] = store.ExamSubmissionAutomaticSealTarget{ExamID: sitting.ExamID, SittingID: sitting.ID,
			ClassID: sitting.ClassID, AcademicUnitID: model.NewAcademicUnitID(), CandidateUserID: model.NewUserID(),
			AttemptID: model.NewExamAttemptID(), WorkspaceID: model.NewExamAttemptWorkspaceID(),
			ParticipationID: model.NewAttemptParticipationID(), Generation: 1, ConnectionID: model.NewAttemptConnectionID()}
	}
	slices.SortFunc(items, func(left, right store.ExamSubmissionAutomaticSealTarget) int {
		return strings.Compare(left.AttemptID.String(), right.AttemptID.String())
	})
	return items
}
