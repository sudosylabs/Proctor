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

func TestExamSittingLifecycleRecoveryDocumentsAreStrictAndBounded(t *testing.T) {
	t.Parallel()
	command, err := EncodeExamSittingLifecycleRecoveryCommand(ExamSittingLifecycleRecoveryCommandV1{WorkLimit: 12})
	require.NoError(t, err)
	decoded, err := DecodeExamSittingLifecycleRecoveryCommand(1, command)
	require.NoError(t, err, "command = %#v", decoded)
	require.Equal(t, 12, decoded.WorkLimit, "command = %#v", decoded)
	for _, malformed := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"work_limit":0}`), json.RawMessage(`{"work_limit":1001}`), json.RawMessage(`{"work_limit":1,"extra":true}`), json.RawMessage(`{"work_limit":1,"work_limit":2}`)} {
		_, err = DecodeExamSittingLifecycleRecoveryCommand(1, malformed)
		require.Error(t, err, "accepted %s", malformed)
	}

	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	checkpoint := ExamSittingLifecycleRecoveryCheckpointV1{AfterDueAt: at, AfterSittingID: model.NewExamSittingID(), Processed: 2, Changed: 1}
	document, err := EncodeExamSittingLifecycleRecoveryCheckpoint(checkpoint)
	require.NoError(t, err)
	decodedCheckpoint, err := DecodeExamSittingLifecycleRecoveryCheckpoint(1, document)
	require.NoError(t, err, "checkpoint")
	require.Equal(t, checkpoint, decodedCheckpoint, "checkpoint")
}

func TestExamSittingLifecycleRecoveryResumesCheckpointAndHonorsWorkLimit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	firstID, secondID := model.NewExamSittingID(), model.NewExamSittingID()
	priorID := model.NewExamSittingID()
	checkpoint := ExamSittingLifecycleRecoveryCheckpointV1{AfterDueAt: at.Add(-time.Minute), AfterSittingID: priorID, Processed: 1}
	checkpointDocument, err := EncodeExamSittingLifecycleRecoveryCheckpoint(checkpoint)
	require.NoError(t, err)
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
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	require.NoError(t, outcome.Err)
	require.Len(t, service.listOptions, 1)
	require.Equal(t, checkpoint.AfterDueAt, service.listOptions[0].AfterDueAt)
	require.Equal(t, priorID, service.listOptions[0].AfterSittingID)
	require.Equal(t, 2, service.listOptions[0].Limit)
	require.Len(t, service.reconciled, 2, "reconciled/checkpoints")
	require.Len(t, saved, 2, "reconciled/checkpoints")
	var result ExamSittingLifecycleRecoveryResultV1
	err = json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result = %#v", result)
	require.Equal(t, int64(3), result.Processed, "result = %#v", result)
	require.Equal(t, int64(1), result.Changed, "result = %#v", result)
	last, err := DecodeExamSittingLifecycleRecoveryCheckpoint(saved[len(saved)-1].Version, saved[len(saved)-1].Document)
	require.NoError(t, err, "last checkpoint = %#v", last)
	require.Equal(t, secondID, last.AfterSittingID, "last checkpoint = %#v", last)
	require.Equal(t, int64(3), last.Processed, "last checkpoint = %#v", last)
	require.Equal(t, int64(1), last.Changed, "last checkpoint = %#v", last)
}

func TestExamSittingLifecycleRecoveryClassifiesListAndCheckpointFailures(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	job := mustRecoveryJob(t, at, 1)
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID()}
	service := &examSittingLifecycleRecoveryFake{err: errors.New("database unavailable")}
	outcome := (examSittingLifecycleRecoveryHandler{service: service}).Run(context.Background(), jobengine.NewExecution(job, attempt, nil, allowJobWorkReservation()))
	require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "dependency.unavailable", outcome.PublicErrorCode)

	bad := *job
	bad.Command = json.RawMessage(`{"work_limit":0}`)
	outcome = (examSittingLifecycleRecoveryHandler{service: service}).Run(context.Background(), jobengine.NewExecution(&bad, attempt, nil, nil))
	require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind)
	require.Equal(t, "job.command.invalid", outcome.PublicErrorCode)

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
	require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "dependency.unavailable", outcome.PublicErrorCode)
}

func TestExamSittingLifecycleRecoveryProposerUsesPermanentUTCDateOccurrence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	proposer := examSittingLifecycleRecoveryProposer{jobs: jobs, now: func() time.Time { return at }}
	occurrence := time.Date(2026, time.August, 17, 1, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	require.NoError(t, proposer.Propose(context.Background(), occurrence))
	var queued *model.Job
	for _, value := range jobs.jobs {
		queued = value
	}
	require.NotNil(t, queued)
	require.Equal(t, model.JobTypeExamSittingLifecycleRecovery, queued.Type)
	require.Equal(t, model.JobDedupePermanent, queued.DedupePolicy)
	require.Equal(t, "exam-sitting-lifecycle-recovery:2026-08-16", queued.DedupeKey)
}

func TestExamSittingLifecycleRecoveryDescriptorDeclaresCheckpointContract(t *testing.T) {
	t.Parallel()
	descriptor := examSittingLifecycleRecoveryDescriptor(examSittingLifecycleRecoveryHandler{service: &examSittingLifecycleRecoveryFake{}})
	require.Equal(t, model.JobTypeExamSittingLifecycleRecovery, descriptor.Type)
	require.False(t, descriptor.Cancelable)
	require.Equal(t, jobengine.VisibilityOperator, descriptor.Visibility)
	require.Len(t, descriptor.CheckpointVersions, 1)
	require.Len(t, descriptor.ProgressStages, 1)
	require.Equal(t, "reconciling", descriptor.ProgressStages[0])
}

type examSittingLifecycleRecoveryFake struct {
	pages       [][]store.ExamSittingLifecycleDue
	listOptions []store.ExamSittingLifecycleDueOptions
	results     map[model.ExamSittingID]*store.ExamSittingLifecycleResult
	reconciled  []model.ExamSittingID
	err         error
}

func (f *examSittingLifecycleRecoveryFake) ListExamSittingLifecycleDueFromJob(_ context.Context, options store.ExamSittingLifecycleDueOptions) ([]store.ExamSittingLifecycleDue, error) {
	f.listOptions = append(f.listOptions, options)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return nil, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *examSittingLifecycleRecoveryFake) ReconcileExamSittingLifecycleFromJob(_ context.Context, sittingID model.ExamSittingID, _ model.JobID, _ model.JobAttemptID) (*store.ExamSittingLifecycleResult, error) {
	f.reconciled = append(f.reconciled, sittingID)
	if f.err != nil {
		return nil, f.err
	}
	return f.results[sittingID], nil
}

func mustRecoveryJob(t *testing.T, at time.Time, limit int) *model.Job {
	t.Helper()
	command, err := EncodeExamSittingLifecycleRecoveryCommand(ExamSittingLifecycleRecoveryCommandV1{WorkLimit: limit})
	require.NoError(t, err)
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeExamSittingLifecycleRecovery, 1, command, "recovery-test", model.JobDedupePermanent, at, at, 5)
	require.NoError(t, err)
	return job
}
