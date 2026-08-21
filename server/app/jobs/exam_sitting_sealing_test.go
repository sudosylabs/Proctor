// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package jobs

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

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
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.Err != nil || len(service.sealed) != 2 ||
		len(checkpoints) != 2 || service.finishCalls != 1 || service.continuationCalls != 0 {
		t.Fatalf("outcome=%#v sealed=%d checkpoints=%d finish=%d continuations=%d", outcome, len(service.sealed),
			len(checkpoints), service.finishCalls, service.continuationCalls)
	}
	var result ExamSittingSealingResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || result.Processed != 2 || result.Sealed != 2 ||
		result.Continued || result.State != model.ExamSittingClosed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	checkpoint, err := decodeExamSittingSealingCheckpoint(checkpoints[1].Version, checkpoints[1].Document)
	if err != nil || checkpoint.AfterAttemptID != targets[1].AttemptID || checkpoint.Processed != 2 || checkpoint.Sealed != 2 {
		t.Fatalf("checkpoint=%#v err=%v", checkpoint, err)
	}
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
	if outcome.Kind != jobengine.OutcomeSucceeded || len(service.sealed) != examSittingSealingMaximumWork ||
		service.finishCalls != 1 || service.continuationCalls != 1 || service.continuationSittingID != sitting.ID ||
		service.continuationParentID != job.ID {
		t.Fatalf("outcome=%#v sealed=%d finish=%d continuations=%d", outcome, len(service.sealed),
			service.finishCalls, service.continuationCalls)
	}
	var result ExamSittingSealingResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || !result.Continued ||
		result.State != model.ExamSittingClosing || result.Revision != sitting.Revision ||
		result.Processed != examSittingSealingMaximumWork || result.Sealed != examSittingSealingMaximumWork {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestExamSittingSealingDescriptorIsBoundedCheckpointedAndNonCancelable(t *testing.T) {
	t.Parallel()
	descriptor := examSittingSealingDescriptor(examSittingSealingHandler{})
	if descriptor.Type != model.JobTypeExamSittingSealing || descriptor.Cancelable || descriptor.Visibility != jobengine.VisibilityOperator ||
		len(descriptor.CheckpointVersions) != 1 || len(descriptor.ProgressStages) != 1 || descriptor.ProgressStages[0] != "sealing" ||
		descriptor.MaximumAttempts != 8 {
		t.Fatalf("descriptor=%#v", descriptor)
	}
}

func TestExamSittingSealingJobLeavesUnfinishedClosingStateRetryable(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	sitting := closingSittingForSealingTest(t, at)
	service := &examSittingSealingFake{finish: sealingLifecycleResult(sitting, false)}
	job := mustExamSittingSealingJob(t, sitting.ID, at)
	outcome := (examSittingSealingHandler{service: service}).Run(context.Background(),
		jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, allowJobWorkReservation()))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "dependency.unavailable" ||
		service.finishCalls != 1 {
		t.Fatalf("outcome=%#v finish=%d", outcome, service.finishCalls)
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
	var checkpoints []ExamSittingSealingCheckpointV1
	reserved := 0
	execution := jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()},
		func(_ context.Context, value jobengine.CheckpointValue) error {
			checkpoint, err := decodeExamSittingSealingCheckpoint(value.Version, value.Document)
			if err != nil {
				t.Fatal(err)
			}
			checkpoints = append(checkpoints, checkpoint)
			return nil
		}, func(_ context.Context, units, limit int) (bool, error) {
			if units != 1 || limit != examSittingSealingMaximumWork {
				t.Fatalf("reservation units=%d limit=%d", units, limit)
			}
			reserved += units
			return true, nil
		})
	outcome := (examSittingSealingHandler{service: service}).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || reserved != 1 || len(service.sealed) != 1 || len(checkpoints) != 2 {
		t.Fatalf("outcome=%#v reserved=%d sealed=%d checkpoints=%#v", outcome, reserved, len(service.sealed), checkpoints)
	}
	if checkpoints[0].Processed != 1 || checkpoints[0].Sealed != 0 || !checkpoints[0].AfterAttemptID.IsZero() {
		t.Fatalf("reconciled checkpoint=%#v", checkpoints[0])
	}
	if checkpoints[1].Processed != 2 || checkpoints[1].Sealed != 1 ||
		checkpoints[1].AfterAttemptID != targets[0].AttemptID {
		t.Fatalf("completed checkpoint=%#v", checkpoints[1])
	}
	var result ExamSittingSealingResultV1
	if err := json.Unmarshal(outcome.Result, &result); err != nil || result.Processed != 2 || result.Sealed != 1 ||
		result.State != model.ExamSittingClosed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

type examSittingSealingFake struct {
	targets               []store.ExamSubmissionAutomaticSealTarget
	sealed                []model.ExamAttemptID
	finish                *store.ExamSittingLifecycleResult
	finishCalls           int
	continuationCalls     int
	continuationSittingID model.ExamSittingID
	continuationParentID  model.JobID
}

func (fake *examSittingSealingFake) ListExamSittingSealTargetsFromJob(_ context.Context, _ model.ExamSittingID,
	after model.ExamAttemptID, limit int,
) ([]store.ExamSubmissionAutomaticSealTarget, error) {
	start := 0
	if !after.IsZero() {
		for index := range fake.targets {
			if fake.targets[index].AttemptID == after {
				start = index + 1
				break
			}
		}
	}
	end := min(start+limit, len(fake.targets))
	return append([]store.ExamSubmissionAutomaticSealTarget(nil), fake.targets[start:end]...), nil
}

func (fake *examSittingSealingFake) SealExamAttemptForSittingCloseFromJob(_ context.Context,
	target store.ExamSubmissionAutomaticSealTarget, _ model.JobID, _ model.JobAttemptID,
) (examattempt.AutomaticSubmissionResult, error) {
	fake.sealed = append(fake.sealed, target.AttemptID)
	return examattempt.AutomaticSubmissionResult{}, nil
}

func (fake *examSittingSealingFake) FinishExamSittingSealingFromJob(context.Context, model.ExamSittingID,
	model.JobID, model.JobAttemptID,
) (*store.ExamSittingLifecycleResult, error) {
	fake.finishCalls++
	return fake.finish, nil
}

func (fake *examSittingSealingFake) EnqueueExamSittingSealingContinuationFromJob(_ context.Context,
	sittingID model.ExamSittingID, parentID model.JobID,
) error {
	fake.continuationCalls++
	fake.continuationSittingID, fake.continuationParentID = sittingID, parentID
	return nil
}

func mustExamSittingSealingJob(t *testing.T, sittingID model.ExamSittingID, at time.Time) *model.Job {
	t.Helper()
	command, err := model.EncodeExamSittingLifecycleCommand(model.ExamSittingLifecycleCommandV1{ExamSittingID: sittingID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamSittingSealing, 1, command, "sealing-test", at, at, 8)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func closingSittingForSealingTest(t *testing.T, at time.Time) *model.ExamSitting {
	t.Helper()
	sitting, err := model.NewExamSitting(model.NewExamSittingID(), model.NewExamID(), model.NewExamRevisionID(),
		model.NewClassID(), at.Add(-2*time.Hour), at.Add(-time.Hour), at.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = sitting.Open(at.Add(-2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = sitting.EnterClosing(model.ExamSittingReasonScheduledEndReached, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
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
