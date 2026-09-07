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
	"sort"
	"testing"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type expiryWorkerFake struct {
	cleanupCalls    int
	cleanupErr      error
	control         model.RetentionControl
	page            store.RetentionExpiryPage
	options         []store.RetentionExpiryListOptions
	reconciliations []store.RetentionExpiryReconciliation
	fail            bool
	failedAudits    int
}

func (f *expiryWorkerFake) GetControl(context.Context) (*model.RetentionControl, error) {
	return &f.control, nil
}
func (f *expiryWorkerFake) ListExpiryRecords(_ context.Context, o store.RetentionExpiryListOptions) (*store.RetentionExpiryPage, error) {
	f.options = append(f.options, o)
	return &f.page, nil
}
func (f *expiryWorkerFake) ReconcileExpiry(_ context.Context, i *store.RetentionExpiryReconciliation) (*store.RetentionExpiryResult, error) {
	f.reconciliations = append(f.reconciliations, *i)
	if f.fail {
		return nil, errors.New("store unavailable")
	}
	return &store.RetentionExpiryResult{Scheduled: true}, nil
}
func (f *expiryWorkerFake) BeginRetentionExpiry(context.Context, model.InstitutionID, model.RetentionExpiryKind, string) (string, error) {
	return model.NewId(), nil
}
func (f *expiryWorkerFake) FailRetention(context.Context, string) error { f.failedAudits++; return nil }

func expiryWorkerFixture(t *testing.T) (retentionExpiryHandler, *expiryWorkerFake, *jobHistoryCleanerFake, *model.Job) {
	t.Helper()
	at := model.NowUTC()
	institution := model.NewInstitutionID()
	f := &expiryWorkerFake{control: model.RetentionControl{InstitutionID: institution, Revision: 1, State: model.RetentionControlEnabled, ApprovedPolicyRevision: 1, ApprovedPreviewID: model.NewRetentionPreviewID(), ChangedByUserID: model.NewUserID(), UpdatedAt: at}, page: store.RetentionExpiryPage{InstitutionID: institution, Before: at, HasMore: true}}
	ids := []string{model.NewId(), model.NewId()}
	sort.Strings(ids)
	for _, id := range ids {
		f.page.Items = append(f.page.Items, model.RetentionExpiryRecord{Kind: model.RetentionExpiryAudit, ID: id, EventAt: at.Add(-48 * time.Hour), EligibleAt: model.OptionalTimeFrom(at.Add(-24 * time.Hour))})
	}
	queue := &jobHistoryCleanerFake{}
	document, _ := json.Marshal(RetentionExpiryCommandV1{Kind: model.RetentionExpiryAudit, BatchSize: 2})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeRetentionExpire, 1, document, "expiry-test", at, at, 5)
	if err != nil {
		t.Fatal(err)
	}
	return retentionExpiryHandler{records: f, audit: f, jobs: queue, now: func() time.Time { return at }}, f, queue, job
}

func TestRetentionExpiryWorkerBudgetContinuationAndUnknownCheckpoint(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	checkpointCount := 0
	e := testJobExecution(job, func(_ context.Context, n, limit int) (bool, error) {
		if job.WorkReserved+n > limit {
			return false, nil
		}
		job.WorkReserved += n
		return true, nil
	}, func(_ context.Context, cp jobengine.CheckpointValue) error {
		checkpointCount++
		if checkpointCount == 3 {
			return errors.New("checkpoint outcome unknown")
		}
		job.CheckpointVersion = cp.Version
		job.Checkpoint = cp.Document
		return nil
	})
	first := h.Run(context.Background(), e)
	if first.Kind != jobengine.OutcomeRetryableFailure || len(f.reconciliations) != 2 || job.WorkReserved != 2 {
		t.Fatalf("first=%#v calls=%d budget=%d", first, len(f.reconciliations), job.WorkReserved)
	}
	retry := h.Run(context.Background(), e)
	if retry.Kind != jobengine.OutcomeSucceeded || len(f.reconciliations) != 2 || len(queue.enqueued) != 1 {
		t.Fatalf("retry=%#v calls=%d continuations=%d", retry, len(f.reconciliations), len(queue.enqueued))
	}
	var command RetentionExpiryCommandV1
	if err := json.Unmarshal(queue.enqueued[0].Command, &command); err != nil {
		t.Fatal(err)
	}
	if command.AfterID != f.page.Items[0].ID || !command.Before.Equal(f.page.Before) || command.Kind != model.RetentionExpiryAudit || command.BatchSize != 2 {
		t.Fatalf("continuation lost safe cursor/snapshot=%#v", command)
	}
	// Another node receiving the same unknown enqueue result cannot create a
	// second continuation or spend a fresh budget in the exhausted parent.
	retry = h.Run(context.Background(), e)
	if retry.Kind != jobengine.OutcomeSucceeded || len(queue.enqueued) != 1 || len(f.reconciliations) != 2 {
		t.Fatal("unknown continuation repeated effects")
	}
}

func TestRetentionExpiryWorkerLeaseLossStopsBeforeAudit(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	o := h.Run(context.Background(), testJobExecution(job, func(context.Context, int, int) (bool, error) { return false, errors.New("lease lost") }, nil))
	if o.Kind != jobengine.OutcomeRetryableFailure || len(f.reconciliations) != 0 || len(queue.enqueued) != 0 {
		t.Fatalf("lease loss continued work=%#v", o)
	}
}

func TestRetentionExpiryWorkerPausedAndFailure(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	f.control.State = model.RetentionControlPaused
	o := h.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	if o.Kind != jobengine.OutcomeSucceeded || len(f.options) != 0 || len(f.reconciliations) != 0 || len(queue.enqueued) != 0 {
		t.Fatal("paused workflow did work")
	}
	f.control.State = model.RetentionControlEnabled
	f.fail = true
	o = h.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if o.Kind != jobengine.OutcomeRetryableFailure || f.failedAudits != 1 || len(queue.enqueued) != 0 {
		t.Fatal("failed mutation lost audit or continued")
	}
}

func TestRetentionExpiryProposerDeduplicatesEachKindAndHour(t *testing.T) {
	t.Parallel()
	queue := &jobHistoryCleanerFake{}
	at := model.NowUTC()
	p := retentionExpiryProposer{jobs: queue, now: func() time.Time { return at }}
	for range 2 {
		if err := p.Propose(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	if len(queue.enqueued) != 3 {
		t.Fatal("duplicate or missing kind occurrence")
	}
	for _, j := range queue.enqueued {
		var c RetentionExpiryCommandV1
		if json.Unmarshal(j.Command, &c) != nil || !c.Kind.IsValid() || c.BatchSize != retentionJobMaximumBatch || j.DedupePolicy != model.JobDedupePermanent {
			t.Fatal("invalid occurrence")
		}
	}
	if err := p.Propose(context.Background(), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(queue.enqueued) != 6 {
		t.Fatal("next hour was suppressed")
	}
}

func (f *expiryWorkerFake) PrepareRetentionCleanupAudit(context.Context, model.InstitutionID) (*model.AuditEvent, error) {
	return &model.AuditEvent{}, nil
}
func (f *expiryWorkerFake) ReconcileCleanupAuditExpiry(_ context.Context, input *store.RetentionCleanupAuditReconciliation) (*store.RetentionCleanupAuditResult, error) {
	f.cleanupCalls++
	return &store.RetentionCleanupAuditResult{Before: input.Before, AfterID: input.AfterID}, f.cleanupErr
}

func TestRetentionExpiryCleanupBatchUnknownOutcomeKeepsBudgetAndSnapshot(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	job.Command, _ = json.Marshal(RetentionExpiryCommandV1{Kind: model.RetentionExpiryAudit, CleanupAudit: true, BatchSize: 2})
	f.cleanupErr = errors.New("commit outcome unknown")
	units := 0
	e := testJobExecution(job, func(_ context.Context, n, limit int) (bool, error) {
		units += n
		job.WorkReserved += n
		return true, nil
	}, func(_ context.Context, cp jobengine.CheckpointValue) error {
		job.CheckpointVersion = cp.Version
		job.Checkpoint = cp.Document
		return nil
	})
	o := h.Run(context.Background(), e)
	if o.Kind != jobengine.OutcomeRetryableFailure || f.cleanupCalls != 1 || units != 2 || len(f.reconciliations) != 0 {
		t.Fatalf("batch outcome=%#v calls=%d reserved=%d", o, f.cleanupCalls, units)
	}
	o = h.Run(context.Background(), e)
	if o.Kind != jobengine.OutcomeSucceeded || f.cleanupCalls != 1 || len(queue.enqueued) != 1 {
		t.Fatal("uncertain batch reset its budget")
	}
	var next RetentionExpiryCommandV1
	if json.Unmarshal(queue.enqueued[0].Command, &next) != nil || !next.CleanupAudit || !next.Before.Equal(f.page.Before) || next.AfterID != "" {
		t.Fatal("batch continuation widened its snapshot or skipped uncertain records")
	}
}

func TestRetentionExpiryFutureGraceDoesNotCreateAnAudit(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	for i := range f.page.Items {
		f.page.Items[i].ScheduledAt = model.OptionalTimeFrom(f.page.Before)
		f.page.Items[i].ExpiresAfter = model.OptionalTimeFrom(f.page.Before.Add(24 * time.Hour))
	}
	f.page.HasMore = false
	o := h.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	if o.Kind != jobengine.OutcomeSucceeded || len(f.reconciliations) != 0 || len(queue.enqueued) != 0 {
		t.Fatal("future grace created repeated no-op audit")
	}
}
