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
	"strings"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type exportBuilderFake struct {
	calls int
	input store.ExamExportBuild
	err   error
}

func (f *exportBuilderFake) BuildFromJob(_ context.Context, input store.ExamExportBuild) error {
	f.calls++
	f.input = input
	return f.err
}

func exportJobExecution(t *testing.T, command []byte, reserve func(context.Context, int, int) (bool, error)) jobengine.Execution {
	t.Helper()
	now := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportBuild, 1, command, "export-test", now, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	token, err := model.NewJobClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID(), JobID: job.ID, ClaimToken: token}
	return jobengine.NewExecution(job, attempt, nil, reserve)
}

func TestExamExportBuildJobIsStrictClaimBoundedAndSafe(t *testing.T) {
	id := model.NewExamExportID()
	valid, _ := json.Marshal(examExportBuildCommandV1{ExportID: id})
	fake := &exportBuilderFake{}
	handler := examExportBuildHandler{service: fake}
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{"export_id":"invalid"}`), []byte(`{"export_id":"` + id.String() + `","snapshot":"private"}`), []byte(`{"export_id":"` + id.String() + `","export_id":"` + id.String() + `"}`)} {
		outcome := handler.Run(context.Background(), exportJobExecution(t, data, allowJobWorkReservation()))
		if outcome.Kind != jobengine.OutcomePermanentFailure || fake.calls != 0 {
			t.Fatalf("invalid command accepted: %#v", outcome)
		}
	}
	execution := exportJobExecution(t, valid, func(_ context.Context, units, limit int) (bool, error) {
		if units != 1 || limit != 3 {
			t.Fatalf("work bound=%d/%d", units, limit)
		}
		return true, nil
	})
	outcome := handler.Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeSucceeded || fake.calls != 1 || fake.input.ExportID != id || fake.input.JobID != execution.Job.ID || fake.input.AttemptID != execution.Attempt.ID || fake.input.ClaimToken != execution.Attempt.ClaimToken {
		t.Fatalf("claim not carried: %#v %#v", outcome, fake.input)
	}
	fake.err = errors.New("private answer and provider://internal/key")
	outcome = handler.Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeRetryableFailure || strings.Contains(outcome.Err.Error(), "private") || strings.Contains(outcome.Err.Error(), "provider") {
		t.Fatalf("unsafe failure=%#v", outcome)
	}
	execution = exportJobExecution(t, valid, func(context.Context, int, int) (bool, error) { return false, nil })
	previous := fake.calls
	outcome = handler.Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomePermanentFailure || fake.calls != previous {
		t.Fatal("exhausted work budget constructed more content")
	}
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
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, 1, json.RawMessage(`{"batch_size":2}`), "cleanup", now, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	handler := examExportCleanupHandler{exports: fake, content: fake}
	outcome := handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, allowJobWorkReservation()))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || fake.limit != 2 || len(fake.completed) != 1 || fake.completed[0] != second {
		t.Fatalf("cleanup=%#v complete=%#v", outcome, fake.completed)
	}
	fake.fail = ""
	fake.completed = nil
	// Only the failed key becomes due again after its durable retry delay.
	fake.artifacts = []store.ExamExportArtifact{first}
	outcome = handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, allowJobWorkReservation()))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(fake.completed) != 1 || fake.completed[0] != first {
		t.Fatalf("cleanup retry=%#v", outcome)
	}
}

func TestExamExportCleanupFailedFullBatchDoesNotStarveNextBatch(t *testing.T) {
	fake := &exportMaintenanceFake{failures: make(map[model.JobAttemptID]bool)}
	for i := range 201 {
		key := store.ExamExportArtifact{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
		fake.artifacts = append(fake.artifacts, key)
		fake.failures[key.AttemptID] = i < 100
	}
	now := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, 1, json.RawMessage(`{"batch_size":100}`), "cleanup-progress", now, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	handler := examExportCleanupHandler{exports: fake, content: fake}
	reserve := reserveRetentionWork(job)
	outcome := handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, reserve))
	if outcome.Kind != jobengine.OutcomeRetryableFailure || len(fake.completed) != 0 || len(fake.artifacts) != 101 {
		t.Fatal("failed first batch lost its error or exceeded its selection bound")
	}
	outcome = handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, reserve))
	if outcome.Kind != jobengine.OutcomeSucceeded || len(fake.completed) != 100 || len(fake.artifacts) != 1 || fake.beginCalls != 2 || job.WorkReserved != 2 {
		t.Fatalf("later healthy batch starved: %#v complete=%d reserved=%d", outcome, len(fake.completed), job.WorkReserved)
	}
}

func TestExamExportCleanupProposerUsesFixedHourlyOccurrences(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	enqueuer := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	proposer := examExportCleanupProposer{jobs: enqueuer, now: func() time.Time { return now }}
	for _, at := range []time.Time{now, now.Add(59 * time.Minute), now.Add(time.Hour)} {
		if err := proposer.Propose(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	if len(enqueuer.jobs) != 2 {
		t.Fatalf("hourly occurrences=%d", len(enqueuer.jobs))
	}
	for _, job := range enqueuer.jobs {
		if job.MaximumAttempts != 3 || job.DedupePolicy != model.JobDedupePermanent || strings.Contains(string(job.Command), "snapshot") {
			t.Fatalf("unbounded cleanup=%#v", job)
		}
	}
	for _, descriptor := range []jobengine.Descriptor{examExportBuildDescriptor(examExportBuildHandler{}), examExportCleanupDescriptor(examExportCleanupHandler{})} {
		if descriptor.MaximumAttempts != 3 || len(descriptor.ExplicitRetryStatuses) != 0 || descriptor.Concurrency != 1 {
			t.Fatal("exports allow unbounded retry or concurrent construction")
		}
	}
}

func TestExamExportPurgeSelectionUncertaintyConsumesFiniteBudget(t *testing.T) {
	now := model.NowUTC()
	job, err := model.NewJob(model.NewJobID(), model.JobTypeExamExportCleanup, 1, json.RawMessage(`{"batch_size":100}`), "cleanup-uncertainty", now, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	fake := &exportMaintenanceFake{beginError: errors.New("private commit acknowledgement lost")}
	handler := examExportCleanupHandler{exports: fake, content: fake}
	reserve := reserveRetentionWork(job)
	for range 3 {
		outcome := handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, reserve))
		if outcome.Kind != jobengine.OutcomeRetryableFailure || strings.Contains(outcome.Err.Error(), "private") {
			t.Fatalf("uncertain selection=%#v", outcome)
		}
	}
	outcome := handler.Run(context.Background(), jobengine.NewExecution(job, nil, nil, reserve))
	if outcome.Kind != jobengine.OutcomePermanentFailure || fake.beginCalls != 3 || job.WorkReserved != 3 || len(fake.completed) != 0 {
		t.Fatalf("uncertainty restarted cleanup: %#v calls=%d reserved=%d", outcome, fake.beginCalls, job.WorkReserved)
	}
}
