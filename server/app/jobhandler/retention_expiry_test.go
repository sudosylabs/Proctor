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
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
func (f *expiryWorkerFake) BeginRetentionExpiry(context.Context, model.InstitutionID, model.RetentionExpiryKind, string) (model.AuditEventID, error) {
	return model.NewAuditEventID(), nil
}
func (f *expiryWorkerFake) FailRetention(context.Context, model.AuditEventID) error {
	f.failedAudits++
	return nil
}

func expiryWorkerFixture(t *testing.T) (retentionExpiryHandler, *expiryWorkerFake, *jobHistoryCleanerFake, *model.Job) {
	t.Helper()
	at := model.NowUTC()
	institution := model.NewInstitutionID()
	f := &expiryWorkerFake{control: model.RetentionControl{InstitutionID: institution, Revision: 1, State: model.RetentionControlEnabled, ApprovedPolicyRevision: 1, ApprovedPreviewID: model.NewRetentionPreviewID(), ChangedByUserID: model.NewUserID(), UpdatedAt: at}, page: store.RetentionExpiryPage{InstitutionID: institution, Before: at, HasMore: true}}
	ids := []string{model.NewID(), model.NewID()}
	sort.Strings(ids)
	for _, id := range ids {
		f.page.Items = append(f.page.Items, model.RetentionExpiryRecord{Kind: model.RetentionExpiryAudit, ID: id, EventAt: at.Add(-48 * time.Hour), EligibleAt: model.OptionalTimeFrom(at.Add(-24 * time.Hour))})
	}
	queue := &jobHistoryCleanerFake{}
	document, _ := json.Marshal(RetentionExpiryCommand{Kind: model.RetentionExpiryAudit, BatchSize: 2})
	job, err := model.NewJob(model.NewJobID(), model.JobTypeRetentionExpire, document, "expiry-test", at, at, 5)
	require.NoError(t, err)
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
		job.Checkpoint = cp.Document
		return nil
	})
	first := h.Run(context.Background(), e)
	require.Equal(t, jobengine.OutcomeRetryableFailure, first.Kind, "first=%#v calls=%d budget", first, len(f.reconciliations))
	require.Len(t, f.reconciliations, 2, "first=%#v calls=%d budget", first, len(f.reconciliations))
	require.Equal(t, 2, job.WorkReserved, "first=%#v calls=%d budget", first, len(f.reconciliations))
	retry := h.Run(context.Background(), e)
	require.Equal(t, jobengine.OutcomeSucceeded, retry.Kind, "retry=%#v calls=%d continuations", retry, len(f.reconciliations))
	require.Len(t, f.reconciliations, 2, "retry=%#v calls=%d continuations", retry, len(f.reconciliations))
	require.Len(t, queue.enqueued, 1, "retry=%#v calls=%d continuations", retry, len(f.reconciliations))
	var command RetentionExpiryCommand
	require.NoError(t, json.Unmarshal(queue.enqueued[0].Command, &command))
	require.Equal(t, f.page.Items[0].ID, command.AfterID)
	require.True(t, command.Before.Equal(f.page.Before))
	require.Equal(t, model.RetentionExpiryAudit, command.Kind)
	require.Equal(t, 2, command.BatchSize)
	// Another node receiving the same unknown enqueue result cannot create a
	// second continuation or spend a fresh budget in the exhausted parent.
	retry = h.Run(context.Background(), e)
	require.Equal(t, jobengine.OutcomeSucceeded, retry.Kind, "unknown continuation repeated effects")
	require.Len(t, queue.enqueued, 1, "unknown continuation repeated effects")
	require.Len(t, f.reconciliations, 2, "unknown continuation repeated effects")
}

func TestRetentionExpiryWorkerLeaseLossStopsBeforeAudit(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	o := h.Run(context.Background(), testJobExecution(job, func(context.Context, int, int) (bool, error) { return false, errors.New("lease lost") }, nil))
	require.Equal(t, jobengine.OutcomeRetryableFailure, o.Kind)
	require.Empty(t, f.reconciliations)
	require.Empty(t, queue.enqueued)
}

func TestRetentionExpiryWorkerPausedAndFailure(t *testing.T) {
	t.Parallel()
	h, f, queue, job := expiryWorkerFixture(t)
	f.control.State = model.RetentionControlPaused
	o := h.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), nil))
	require.Equal(t, jobengine.OutcomeSucceeded, o.Kind, "paused workflow did work")
	require.Empty(t, f.options, "paused workflow did work")
	require.Empty(t, f.reconciliations, "paused workflow did work")
	require.Empty(t, queue.enqueued, "paused workflow did work")
	f.control.State = model.RetentionControlEnabled
	f.fail = true
	o = h.Run(context.Background(), testJobExecution(job, allowJobWorkReservation(), func(context.Context, jobengine.CheckpointValue) error { return nil }))
	require.Equal(t, jobengine.OutcomeRetryableFailure, o.Kind, "failed mutation lost audit or continued")
	require.Equal(t, 1, f.failedAudits, "failed mutation lost audit or continued")
	require.Empty(t, queue.enqueued, "failed mutation lost audit or continued")
}

func TestRetentionExpiryProposerDeduplicatesEachKindAndHour(t *testing.T) {
	t.Parallel()
	queue := &jobHistoryCleanerFake{}
	at := model.NowUTC()
	p := retentionExpiryProposer{jobs: queue, now: func() time.Time { return at }}
	for range 2 {
		require.NoError(t, p.Propose(context.Background(), at))
	}
	require.Len(t, queue.enqueued, 3, "duplicate or missing kind occurrence")
	for _, j := range queue.enqueued {
		var c RetentionExpiryCommand
		require.NoError(t, json.Unmarshal(j.Command, &c), "invalid occurrence")
		require.True(t, c.Kind.IsValid(), "invalid occurrence")
		require.Equal(t, retentionJobMaximumBatch, c.BatchSize, "invalid occurrence")
		require.Equal(t, model.JobDedupePermanent, j.DedupePolicy, "invalid occurrence")
	}
	require.NoError(t, p.Propose(context.Background(), at.Add(time.Hour)))
	require.Len(t, queue.enqueued, 6, "next hour was suppressed")
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
	job.Command, _ = json.Marshal(RetentionExpiryCommand{Kind: model.RetentionExpiryAudit, CleanupAudit: true, BatchSize: 2})
	f.cleanupErr = errors.New("commit outcome unknown")
	units := 0
	e := testJobExecution(job, func(_ context.Context, n, limit int) (bool, error) {
		units += n
		job.WorkReserved += n
		return true, nil
	}, func(_ context.Context, cp jobengine.CheckpointValue) error {
		job.Checkpoint = cp.Document
		return nil
	})
	o := h.Run(context.Background(), e)
	require.Equal(t, jobengine.OutcomeRetryableFailure, o.Kind, "batch outcome=%#v calls=%d reserved", o, f.cleanupCalls)
	require.Equal(t, 1, f.cleanupCalls, "batch outcome=%#v calls=%d reserved", o, f.cleanupCalls)
	require.Equal(t, 2, units, "batch outcome=%#v calls=%d reserved", o, f.cleanupCalls)
	require.Empty(t, f.reconciliations, "batch outcome=%#v calls=%d reserved", o, f.cleanupCalls)
	o = h.Run(context.Background(), e)
	require.Equal(t, jobengine.OutcomeSucceeded, o.Kind, "uncertain batch reset its budget")
	require.Equal(t, 1, f.cleanupCalls, "uncertain batch reset its budget")
	require.Len(t, queue.enqueued, 1, "uncertain batch reset its budget")
	var next RetentionExpiryCommand
	require.NoError(t, json.Unmarshal(queue.enqueued[0].Command, &next), "batch continuation widened its snapshot or skipped uncertain records")
	require.True(t, next.CleanupAudit, "batch continuation widened its snapshot or skipped uncertain records")
	require.True(t, next.Before.Equal(f.page.Before), "batch continuation widened its snapshot or skipped uncertain records")
	require.Empty(t, next.AfterID, "batch continuation widened its snapshot or skipped uncertain records")
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
	require.Equal(t, jobengine.OutcomeSucceeded, o.Kind, "future grace created repeated no-op audit")
	require.Empty(t, f.reconciliations, "future grace created repeated no-op audit")
	require.Empty(t, queue.enqueued, "future grace created repeated no-op audit")
}
