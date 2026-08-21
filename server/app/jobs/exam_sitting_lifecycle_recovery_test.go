// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package jobs

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

func TestExamSittingLifecycleRecoveryDocumentsAreStrictAndBounded(t *testing.T) {
	t.Parallel()
	command, err := EncodeExamSittingLifecycleRecoveryCommand(ExamSittingLifecycleRecoveryCommandV1{WorkLimit: 12})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeExamSittingLifecycleRecoveryCommand(1, command)
	if err != nil || decoded.WorkLimit != 12 {
		t.Fatalf("command = %#v, %v", decoded, err)
	}
	for _, malformed := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"work_limit":0}`), json.RawMessage(`{"work_limit":1001}`), json.RawMessage(`{"work_limit":1,"extra":true}`), json.RawMessage(`{"work_limit":1,"work_limit":2}`)} {
		if _, err = DecodeExamSittingLifecycleRecoveryCommand(1, malformed); err == nil {
			t.Fatalf("accepted %s", malformed)
		}
	}

	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	checkpoint := ExamSittingLifecycleRecoveryCheckpointV1{AfterDueAt: at, AfterSittingID: model.NewExamSittingID(), Processed: 2, Changed: 1}
	document, err := EncodeExamSittingLifecycleRecoveryCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	decodedCheckpoint, err := DecodeExamSittingLifecycleRecoveryCheckpoint(1, document)
	if err != nil || decodedCheckpoint != checkpoint {
		t.Fatalf("checkpoint = %#v, %v", decodedCheckpoint, err)
	}
}

func TestExamSittingLifecycleRecoveryResumesCheckpointAndHonorsWorkLimit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	firstID, secondID := model.NewExamSittingID(), model.NewExamSittingID()
	priorID := model.NewExamSittingID()
	checkpoint := ExamSittingLifecycleRecoveryCheckpointV1{AfterDueAt: at.Add(-time.Minute), AfterSittingID: priorID, Processed: 1}
	checkpointDocument, err := EncodeExamSittingLifecycleRecoveryCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	job := mustRecoveryJob(t, at, 3)
	job.CheckpointVersion, job.Checkpoint, job.WorkReserved = 1, checkpointDocument, 1
	service := &examSittingLifecycleRecoveryFake{pages: [][]store.ExamSittingLifecycleDue{{
		{Value: lifecycleJobResult(t, firstID, false).Value, DueAt: at},
		{Value: lifecycleJobResult(t, secondID, false).Value, DueAt: at.Add(time.Second)},
	}}}
	service.results = map[model.ExamSittingID]*store.ExamSittingLifecycleResult{
		firstID: lifecycleJobResult(t, firstID, false), secondID: lifecycleJobResult(t, secondID, true),
	}
	var saved []jobengine.CheckpointValue
	reserved := 0
	execution := jobengine.NewExecution(job, &model.JobAttempt{ID: model.NewJobAttemptID()}, func(_ context.Context, value jobengine.CheckpointValue) error {
		saved = append(saved, value)
		return nil
	}, func(_ context.Context, units, limit int) (bool, error) {
		if limit != 3 || reserved+units > 2 {
			return false, nil
		}
		reserved += units
		return true, nil
	})
	outcome := (examSittingLifecycleRecoveryHandler{service: service}).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.Err != nil {
		t.Fatalf("outcome = %#v", outcome)
	}
	if len(service.listOptions) != 1 || service.listOptions[0].AfterDueAt != checkpoint.AfterDueAt || service.listOptions[0].AfterSittingID != priorID || service.listOptions[0].Limit != 2 {
		t.Fatalf("list options = %#v", service.listOptions)
	}
	if len(service.reconciled) != 2 || len(saved) != 2 {
		t.Fatalf("reconciled/checkpoints = %d/%d", len(service.reconciled), len(saved))
	}
	var result ExamSittingLifecycleRecoveryResultV1
	if err = json.Unmarshal(outcome.Result, &result); err != nil || result.Processed != 3 || result.Changed != 1 {
		t.Fatalf("result = %#v, %v", result, err)
	}
	last, err := DecodeExamSittingLifecycleRecoveryCheckpoint(saved[len(saved)-1].Version, saved[len(saved)-1].Document)
	if err != nil || last.AfterSittingID != secondID || last.Processed != 3 || last.Changed != 1 {
		t.Fatalf("last checkpoint = %#v, %v", last, err)
	}
}

func TestExamSittingLifecycleRecoveryClassifiesListAndCheckpointFailures(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	job := mustRecoveryJob(t, at, 1)
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID()}
	service := &examSittingLifecycleRecoveryFake{err: errors.New("database unavailable")}
	outcome := (examSittingLifecycleRecoveryHandler{service: service}).Run(context.Background(), jobengine.NewExecution(job, attempt, nil, allowJobWorkReservation()))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "dependency.unavailable" {
		t.Fatalf("list outcome = %#v", outcome)
	}

	bad := *job
	bad.Command = json.RawMessage(`{"work_limit":0}`)
	outcome = (examSittingLifecycleRecoveryHandler{service: service}).Run(context.Background(), jobengine.NewExecution(&bad, attempt, nil, nil))
	if outcome.Kind != jobengine.OutcomePermanentFailure || outcome.PublicErrorCode != "job.command.invalid" {
		t.Fatalf("command outcome = %#v", outcome)
	}

	sittingID := model.NewExamSittingID()
	checkpointService := &examSittingLifecycleRecoveryFake{
		pages:   [][]store.ExamSittingLifecycleDue{{{Value: lifecycleJobResult(t, sittingID, false).Value, DueAt: at}}},
		results: map[model.ExamSittingID]*store.ExamSittingLifecycleResult{sittingID: lifecycleJobResult(t, sittingID, false)},
	}
	outcome = (examSittingLifecycleRecoveryHandler{service: checkpointService}).Run(context.Background(), jobengine.NewExecution(
		job, attempt,
		func(context.Context, jobengine.CheckpointValue) error { return errors.New("checkpoint unavailable") },
		allowJobWorkReservation(),
	))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "dependency.unavailable" {
		t.Fatalf("checkpoint outcome = %#v", outcome)
	}
}

func TestExamSittingLifecycleRecoveryProposerUsesPermanentUTCDateOccurrence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	proposer := examSittingLifecycleRecoveryProposer{jobs: jobs, now: func() time.Time { return at }}
	occurrence := time.Date(2026, time.August, 17, 1, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	if err := proposer.Propose(context.Background(), occurrence); err != nil {
		t.Fatal(err)
	}
	var queued *model.Job
	for _, value := range jobs.jobs {
		queued = value
	}
	if queued == nil || queued.Type != model.JobTypeExamSittingLifecycleRecovery || queued.DedupePolicy != model.JobDedupePermanent || queued.DedupeKey != "exam-sitting-lifecycle-recovery:2026-08-16" {
		t.Fatalf("queued = %#v", queued)
	}
}

func TestExamSittingLifecycleRecoveryDescriptorDeclaresCheckpointContract(t *testing.T) {
	t.Parallel()
	descriptor := examSittingLifecycleRecoveryDescriptor(examSittingLifecycleRecoveryHandler{service: &examSittingLifecycleRecoveryFake{}})
	if descriptor.Type != model.JobTypeExamSittingLifecycleRecovery || descriptor.Cancelable || descriptor.Visibility != jobengine.VisibilityOperator ||
		len(descriptor.CheckpointVersions) != 1 || len(descriptor.ProgressStages) != 1 || descriptor.ProgressStages[0] != "reconciling" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

type examSittingLifecycleRecoveryFake struct {
	pages       [][]store.ExamSittingLifecycleDue
	listOptions []store.ExamSittingLifecycleDueOptions
	results     map[model.ExamSittingID]*store.ExamSittingLifecycleResult
	reconciled  []model.ExamSittingID
	err         error
}

func (fake *examSittingLifecycleRecoveryFake) ListExamSittingLifecycleDueFromJob(_ context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	fake.listOptions = append(fake.listOptions, options)
	if fake.err != nil {
		return nil, fake.err
	}
	if len(fake.pages) == 0 {
		return nil, nil
	}
	page := fake.pages[0]
	fake.pages = fake.pages[1:]
	return page, nil
}

func (fake *examSittingLifecycleRecoveryFake) ReconcileExamSittingLifecycleFromJob(_ context.Context, sittingID model.ExamSittingID, _ model.JobID, _ model.JobAttemptID) (*store.ExamSittingLifecycleResult, error) {
	fake.reconciled = append(fake.reconciled, sittingID)
	if fake.err != nil {
		return nil, fake.err
	}
	return fake.results[sittingID], nil
}

func mustRecoveryJob(t *testing.T, at time.Time, limit int) *model.Job {
	t.Helper()
	command, err := EncodeExamSittingLifecycleRecoveryCommand(ExamSittingLifecycleRecoveryCommandV1{WorkLimit: limit})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycleRecovery, 1, command, "recovery-test", model.JobDedupePermanent, at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
