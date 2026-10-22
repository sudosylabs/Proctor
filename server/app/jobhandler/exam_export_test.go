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

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type exportBuilderFake struct {
	calls int
	input store.ExamExportBuild
	err   error
}

func (f *exportBuilderFake) BuildFromJob(_ context.Context, _ invocation.System, input store.ExamExportBuild) error {
	f.calls++
	f.input = input
	return f.err
}

func exportJobExecution(t *testing.T, command []byte, reserve func(context.Context, int, int) (bool, error)) job.Execution {
	t.Helper()
	now := model.NowUTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportBuild, command, "export-test", now, now, 3)
	require.NoError(t, err)
	token, err := model.NewJobClaimToken()
	require.NoError(t, err)
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID(), JobID: record.ID, ClaimToken: token}
	return job.NewExecution(record, attempt, nil, reserve)
}

func TestExamExportBuildJobIsStrictClaimBoundedAndSafe(t *testing.T) {
	id := model.NewExamExportID()
	valid, _ := json.Marshal(examExportBuildCommand{ExportID: id})
	fake := &exportBuilderFake{}
	handler := examExportBuildHandler{service: fake}
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{"export_id":"invalid"}`), []byte(`{"export_id":"` + id.String() + `","snapshot":"private"}`), []byte(`{"export_id":"` + id.String() + `","export_id":"` + id.String() + `"}`)} {
		outcome := handler.Run(context.Background(), exportJobExecution(t, data, allowJobWorkReservation()))
		require.Equal(t, job.OutcomePermanentFailure, outcome.Kind)
		require.Equal(t, 0, fake.calls)
	}
	execution := exportJobExecution(t, valid, func(_ context.Context, units, limit int) (bool, error) {
		require.Equal(t, 1, units, "work bound")
		require.Equal(t, 3, limit, "work bound")
		return true, nil
	})
	outcome := handler.Run(context.Background(), execution)
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "claim not carried: %#v %#v", outcome, fake.input)
	require.Equal(t, 1, fake.calls, "claim not carried: %#v %#v", outcome, fake.input)
	require.Equal(t, id, fake.input.ExportID, "claim not carried: %#v %#v", outcome, fake.input)
	require.Equal(t, execution.Job.ID, fake.input.JobID, "claim not carried: %#v %#v", outcome, fake.input)
	require.Equal(t, execution.Attempt.ID, fake.input.AttemptID, "claim not carried: %#v %#v", outcome, fake.input)
	require.Equal(t, execution.Attempt.ClaimToken, fake.input.ClaimToken, "claim not carried: %#v %#v", outcome, fake.input)
	fake.err = errors.New("private answer and provider://internal/key")
	outcome = handler.Run(context.Background(), execution)
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.NotContains(t, outcome.Err.Error(), "private")
	require.NotContains(t, outcome.Err.Error(), "provider")
	execution = exportJobExecution(t, valid, func(context.Context, int, int) (bool, error) { return false, nil })
	previous := fake.calls
	outcome = handler.Run(context.Background(), execution)
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "exhausted work budget constructed more content")
	require.Equal(t, previous, fake.calls, "exhausted work budget constructed more content")
}

type exportMaintenanceFake struct {
	artifacts  []store.ExamExportArtifact
	completed  []store.ExamExportArtifact
	limit      int
	fail       model.JobAttemptID
	failures   map[model.JobAttemptID]bool
	beginCalls int
	beginError error
}

func (f *exportMaintenanceFake) Reconcile(_ context.Context, limit int) (int, error) {
	f.limit = limit
	return 0, nil
}
func (f *exportMaintenanceFake) BeginPurgeBatch(_ context.Context, limit int) ([]store.ExamExportArtifact, error) {
	f.beginCalls++
	if limit != f.limit {
		return nil, errors.New("inconsistent batch")
	}
	count := min(limit, len(f.artifacts))
	selected := append([]store.ExamExportArtifact(nil), f.artifacts[:count]...)
	f.artifacts = f.artifacts[count:]
	return selected, f.beginError
}
func (f *exportMaintenanceFake) CompletePurge(_ context.Context, a store.ExamExportArtifact) error {
	f.completed = append(f.completed, a)
	return nil
}
func (f *exportMaintenanceFake) PurgeExamExport(_ context.Context, _ model.ExamExportID, attempt model.JobAttemptID) error {
	if attempt == f.fail || f.failures[attempt] {
		return errors.New("uncertain removal")
	}
	return nil
}

func TestExamExportCleanupCompletesOnlyVerifiedKeysAndDoesNotStarveOtherKeys(t *testing.T) {
	first := store.ExamExportArtifact{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
	second := store.ExamExportArtifact{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
	fake := &exportMaintenanceFake{artifacts: []store.ExamExportArtifact{first, second}, fail: first.AttemptID}
	now := model.NowUTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, json.RawMessage(`{"batch_size":2}`), "cleanup", now, now, 3)
	require.NoError(t, err)
	handler := examExportCleanupHandler{exports: fake, content: fake}
	outcome := handler.Run(context.Background(), job.NewExecution(record, nil, nil, allowJobWorkReservation()))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind, "cleanup=%#v complete", outcome)
	require.Equal(t, 2, fake.limit, "cleanup=%#v complete", outcome)
	require.Len(t, fake.completed, 1, "cleanup=%#v complete", outcome)
	require.Equal(t, second, fake.completed[0], "cleanup=%#v complete", outcome)
	fake.fail = ""
	fake.completed = nil
	// Only the failed key becomes due again after its durable retry delay.
	fake.artifacts = []store.ExamExportArtifact{first}
	outcome = handler.Run(context.Background(), job.NewExecution(record, nil, nil, allowJobWorkReservation()))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind)
	require.Len(t, fake.completed, 1)
	require.Equal(t, first, fake.completed[0])
}

func TestExamExportCleanupFailedFullBatchDoesNotStarveNextBatch(t *testing.T) {
	fake := &exportMaintenanceFake{failures: make(map[model.JobAttemptID]bool)}
	for i := range 201 {
		key := store.ExamExportArtifact{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
		fake.artifacts = append(fake.artifacts, key)
		fake.failures[key.AttemptID] = i < 100
	}
	now := model.NowUTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, json.RawMessage(`{"batch_size":100}`), "cleanup-progress", now, now, 3)
	require.NoError(t, err)
	handler := examExportCleanupHandler{exports: fake, content: fake}
	reserve := reserveRetentionWork(record)
	outcome := handler.Run(context.Background(), job.NewExecution(record, nil, nil, reserve))
	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind, "failed first batch lost its error or exceeded its selection bound")
	require.Empty(t, fake.completed, "failed first batch lost its error or exceeded its selection bound")
	require.Len(t, fake.artifacts, 101, "failed first batch lost its error or exceeded its selection bound")
	outcome = handler.Run(context.Background(), job.NewExecution(record, nil, nil, reserve))
	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "later healthy batch starved: %#v complete=%d reserved", outcome, len(fake.completed))
	require.Len(t, fake.completed, 100, "later healthy batch starved: %#v complete=%d reserved", outcome, len(fake.completed))
	require.Len(t, fake.artifacts, 1, "later healthy batch starved: %#v complete=%d reserved", outcome, len(fake.completed))
	require.Equal(t, 2, fake.beginCalls, "later healthy batch starved: %#v complete=%d reserved", outcome, len(fake.completed))
	require.Equal(t, 2, record.WorkReserved, "later healthy batch starved: %#v complete=%d reserved", outcome, len(fake.completed))
}

func TestExamExportCleanupProposerUsesFixedHourlyOccurrences(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	enqueuer := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	proposer := examExportCleanupProposer{jobs: enqueuer, now: func() time.Time { return now }}
	for _, at := range []time.Time{now, now.Add(59 * time.Minute), now.Add(time.Hour)} {
		require.NoError(t, proposer.Propose(context.Background(), at))
	}
	require.Len(t, enqueuer.jobs, 2, "hourly occurrences")
	for _, job := range enqueuer.jobs {
		require.Equal(t, 3, job.MaximumAttempts)
		require.Equal(t, model.JobDedupePermanent, job.DedupePolicy)
		require.NotContains(t, string(job.Command), "snapshot")
	}
	for _, descriptor := range []job.Descriptor{examExportBuildDescriptor(examExportBuildHandler{}), examExportCleanupDescriptor(examExportCleanupHandler{})} {
		require.Equal(t, 3, descriptor.MaximumAttempts, "exports allow unbounded retry or concurrent construction")
		require.Empty(t, descriptor.ExplicitRetryStatuses, "exports allow unbounded retry or concurrent construction")
		require.Equal(t, 1, descriptor.Concurrency, "exports allow unbounded retry or concurrent construction")
	}
}

func TestExamExportPurgeSelectionUncertaintyConsumesFiniteBudget(t *testing.T) {
	now := model.NowUTC()
	record, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, json.RawMessage(`{"batch_size":100}`), "cleanup-uncertainty", now, now, 3)
	require.NoError(t, err)
	fake := &exportMaintenanceFake{beginError: errors.New("private commit acknowledgement lost")}
	handler := examExportCleanupHandler{exports: fake, content: fake}
	reserve := reserveRetentionWork(record)
	for range 3 {
		outcome := handler.Run(context.Background(), job.NewExecution(record, nil, nil, reserve))
		require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
		require.NotContains(t, outcome.Err.Error(), "private")
	}
	outcome := handler.Run(context.Background(), job.NewExecution(record, nil, nil, reserve))
	require.Equal(t, job.OutcomePermanentFailure, outcome.Kind, "uncertainty restarted cleanup: %#v calls=%d reserved", outcome, fake.beginCalls)
	require.Equal(t, 3, fake.beginCalls, "uncertainty restarted cleanup: %#v calls=%d reserved", outcome, fake.beginCalls)
	require.Equal(t, 3, record.WorkReserved, "uncertainty restarted cleanup: %#v calls=%d reserved", outcome, fake.beginCalls)
	require.Empty(t, fake.completed, "uncertainty restarted cleanup: %#v calls=%d reserved", outcome, fake.beginCalls)
}
