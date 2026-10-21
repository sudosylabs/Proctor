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

type retentionJobStoreFake struct {
	control                       *model.RetentionControl
	page                          *store.RetentionRecordPage
	purges                        []store.RetentionPurgeObject
	listed, reconciled, completed int
	fail                          error
	completeFailFor               model.AttemptWorkspaceObjectID
	completedObjects              []model.AttemptWorkspaceObjectID
	beginPurge                    func(context.Context, int) ([]store.RetentionPurgeObject, error)
}

func (s *retentionJobStoreFake) GetControl(context.Context) (*model.RetentionControl, error) {
	return s.control, nil
}
func (s *retentionJobStoreFake) ListRecords(context.Context, store.RetentionRecordListOptions) (*store.RetentionRecordPage, error) {
	s.listed++
	return s.page, nil
}
func (s *retentionJobStoreFake) ReconcileSubmission(context.Context, *store.RetentionReconciliation) (*store.RetentionReconciliationResult, error) {
	s.reconciled++
	return &store.RetentionReconciliationResult{Scheduled: 2}, s.fail
}
func (s *retentionJobStoreFake) BeginPurgeBatch(ctx context.Context, limit int) ([]store.RetentionPurgeObject, error) {
	s.listed++
	if s.beginPurge != nil {
		return s.beginPurge(ctx, limit)
	}
	return s.purges, nil
}
func (s *retentionJobStoreFake) CompletePurge(_ context.Context, input *store.RetentionPurgeCompletion) error {
	s.completed++
	if input.ObjectID == s.completeFailFor {
		return errors.New("completion outcome unavailable")
	}
	s.completedObjects = append(s.completedObjects, input.ObjectID)
	return s.fail
}

type retentionJobAuditFake struct {
	begun, failed int
	err           error
}

func (a *retentionJobAuditFake) BeginRetention(context.Context, model.InstitutionID, model.SubmissionID) (model.AuditEventID, error) {
	a.begun++
	return model.NewAuditEventID(), a.err
}
func (a *retentionJobAuditFake) FailRetention(context.Context, model.AuditEventID) error {
	a.failed++
	return nil
}

type retentionJobPurgerFake struct {
	calls int
	err   error
}

func (p *retentionJobPurgerFake) PurgeRetiredAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) error {
	p.calls++
	return p.err
}

func retentionJobFixture(t *testing.T, kind model.JobType, batch int) (*model.Job, *retentionJobStoreFake, *jobHistoryCleanerFake) {
	t.Helper()
	at := model.NowUTC()
	command, _ := json.Marshal(RetentionCommandV1{BatchSize: batch})
	record, err := model.NewJob(model.NewJobID(), kind, 1, command, "test-retention", at, at, 5)
	require.NoError(t, err)
	scope := model.RetentionHoldScope{ExamID: model.NewExamID(), SittingID: model.NewExamSittingID(), SubmissionID: model.NewSubmissionID()}
	return record, &retentionJobStoreFake{control: &model.RetentionControl{InstitutionID: model.NewInstitutionID(), Revision: 1, State: model.RetentionControlEnabled, ApprovedPolicyRevision: 1, ApprovedPreviewID: model.NewRetentionPreviewID(), ChangedByUserID: model.NewUserID(), UpdatedAt: at}, page: &store.RetentionRecordPage{Items: []store.RetentionRecordItem{{Record: model.RetentionRecord{Scope: scope, Category: model.RetentionCategoryWork}}, {Record: model.RetentionRecord{Scope: scope, Category: model.RetentionCategoryIntegrity}}, {Record: model.RetentionRecord{Scope: scope, Category: model.RetentionCategoryBrowserActivity}}, {Record: model.RetentionRecord{Scope: scope, Category: model.RetentionCategorySecurityOperational}}}}, purges: []store.RetentionPurgeObject{{RetirementID: model.NewRetentionRetirementID(), ObjectID: model.NewAttemptWorkspaceObjectID()}}}, &jobHistoryCleanerFake{}
}
func keepRetentionCheckpoint(record *model.Job) func(context.Context, jobengine.CheckpointValue) error {
	return func(_ context.Context, cp jobengine.CheckpointValue) error {
		record.CheckpointVersion = cp.Version
		record.Checkpoint = append(json.RawMessage(nil), cp.Document...)
		return nil
	}
}

func TestRetentionMaintenanceReservesBeforeAuditOrDeletion(t *testing.T) {
	t.Parallel()
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconcile", true: "purge"}[purge], func(t *testing.T) {
			record, s, jobs := retentionJobFixture(t, model.JobTypeRetentionReconcile, 2)
			audit := &retentionJobAuditFake{}
			content := &retentionJobPurgerFake{}
			handler := retentionHandler{records: s, content: content, audit: audit, jobs: jobs, now: model.NowUTC, purge: purge}
			reserve := func(context.Context, int, int) (bool, error) {
				return false, errors.New("lost lease with private payload")
			}
			result := handler.Run(context.Background(), testJobExecution(record, reserve, keepRetentionCheckpoint(record)))
			require.Equal(t, jobengine.OutcomeRetryableFailure, result.Kind)
			require.Equal(t, 0, s.reconciled)
			require.Equal(t, 0, s.completed)
			require.Equal(t, 0, audit.begun)
			require.Equal(t, 0, content.calls)
			require.NotContains(t, result.Err.Error(), "private payload", "unsafe storage error escaped")
			if purge {
				require.Equal(t, 0, s.listed, "purge batch mutation preceded its work reservation")
			}
		})
	}
}
func TestRetentionRequiresAuditAndIndependentStorageObservation(t *testing.T) {
	t.Parallel()
	record, s, jobs := retentionJobFixture(t, model.JobTypeRetentionReconcile, 2)
	audit := &retentionJobAuditFake{err: errors.New("audit unavailable")}
	handler := retentionHandler{records: s, audit: audit, jobs: jobs, now: model.NowUTC}
	result := handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomeRetryableFailure, result.Kind, "retired without audit")
	require.Equal(t, 0, s.reconciled, "retired without audit")
	audit.err = nil
	s.fail = errors.New("store failed")
	result = handler.Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomeRetryableFailure, result.Kind, "failed mutation audit not closed")
	require.Equal(t, 1, audit.failed, "failed mutation audit not closed")
	record, s, jobs = retentionJobFixture(t, model.JobTypeRetentionPurge, 2)
	content := &retentionJobPurgerFake{err: errors.New("acknowledged but still present")}
	handler = retentionHandler{records: s, content: content, jobs: jobs, now: model.NowUTC, purge: true}
	result = handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomeRetryableFailure, result.Kind, "uncertain storage marked complete")
	require.Equal(t, 0, s.completed, "uncertain storage marked complete")
	result = handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomePermanentFailure, result.Kind, "failed purge lost visibility or created an immediate successor")
	require.Equal(t, 0, s.completed, "failed purge lost visibility or created an immediate successor")
	require.Empty(t, jobs.enqueued, "failed purge lost visibility or created an immediate successor")
	// The exact key remains eligible for a later recurring Job after backoff.
	record, _, _ = retentionJobFixture(t, model.JobTypeRetentionPurge, 2)
	content.err = nil
	result = handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomeSucceeded, result.Kind)
	require.Equal(t, 1, s.completed)
}
func TestRetentionCrashConsumesBudgetAndEnqueuesOnlyOneSuccessor(t *testing.T) {
	t.Parallel()
	record, s, jobs := retentionJobFixture(t, model.JobTypeRetentionReconcile, 1)
	audit := &retentionJobAuditFake{}
	handler := retentionHandler{records: s, audit: audit, jobs: jobs, now: model.NowUTC}
	reserve := func(_ context.Context, units, limit int) (bool, error) {
		record.WorkReserved += units
		return true, nil
	}
	result := handler.Run(context.Background(), testJobExecution(record, reserve, func(context.Context, jobengine.CheckpointValue) error { return errors.New("crash after commit") }))
	require.Equal(t, jobengine.OutcomeRetryableFailure, result.Kind, "did not model committed but uncheckpointed unit")
	require.Equal(t, 1, s.reconciled, "did not model committed but uncheckpointed unit")
	require.Equal(t, 1, record.WorkReserved, "did not model committed but uncheckpointed unit")
	for range 2 {
		result = handler.Run(context.Background(), testJobExecution(record, reserve, keepRetentionCheckpoint(record)))
		require.Equal(t, jobengine.OutcomeSucceeded, result.Kind)
	}
	require.Equal(t, 1, s.reconciled, "retry exceeded reserved budget or duplicated successor")
	require.Len(t, jobs.enqueued, 1, "retry exceeded reserved budget or duplicated successor")
	var command RetentionCommandV1
	require.NoError(t, json.Unmarshal(jobs.enqueued[0].Command, &command))
	require.True(t, command.AfterSubmissionID.IsZero(), "successor skipped uncertain unit")
	require.Equal(t, model.JobDedupePermanent, jobs.enqueued[0].DedupePolicy, "successor skipped uncertain unit")
}
func TestRetentionDisabledControlStopsBeforeInventory(t *testing.T) {
	t.Parallel()
	record, s, jobs := retentionJobFixture(t, model.JobTypeRetentionReconcile, 2)
	s.control.State = model.RetentionControlPaused
	result := (retentionHandler{records: s, audit: &retentionJobAuditFake{}, jobs: jobs, now: model.NowUTC}).Run(context.Background(), testJobExecution(record, allowJobWorkReservation(), keepRetentionCheckpoint(record)))
	require.Equal(t, jobengine.OutcomeSucceeded, result.Kind)
	require.Equal(t, 0, s.listed)
	require.Empty(t, jobs.enqueued)
}
func TestRetentionProposersDeduplicateWithinCorrectInterval(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 7, 12, 1, 0, 0, time.UTC)
	for _, kind := range []model.JobType{model.JobTypeRetentionReconcile, model.JobTypeRetentionPurge, model.JobTypeRetentionNotices} {
		jobs := &jobHistoryCleanerFake{}
		p := retentionProposer{jobs: jobs, now: func() time.Time { return at }, jobType: kind}
		for _, offset := range []time.Duration{0, 3 * time.Minute, 10 * time.Minute, time.Hour} {
			require.NoError(t, p.Propose(context.Background(), at.Add(offset)))
		}
		want := 2
		if kind == model.JobTypeRetentionNotices {
			want = 3
		}
		require.Len(t, jobs.enqueued, want, "%s occurrences", kind)
	}
}

func reserveRetentionWork(record *model.Job) func(context.Context, int, int) (bool, error) {
	return func(_ context.Context, units, limit int) (bool, error) {
		if record.WorkReserved+units > limit {
			return false, nil
		}
		record.WorkReserved += units
		return true, nil
	}
}

type selectiveRetentionPurger struct {
	fail model.AttemptWorkspaceObjectID
	seen []model.AttemptWorkspaceObjectID
}

func (p *selectiveRetentionPurger) PurgeRetiredAttemptWorkspaceObject(_ context.Context, id model.AttemptWorkspaceObjectID) error {
	p.seen = append(p.seen, id)
	if id == p.fail {
		return errors.New("private backend object unavailable")
	}
	return nil
}

func TestRetentionPurgeFailureDoesNotStarveFollowingObject(t *testing.T) {
	for _, failure := range []string{"content", "completion"} {
		t.Run(failure, func(t *testing.T) {
			record, persistence, enqueuer := retentionJobFixture(t, model.JobTypeRetentionPurge, 2)
			first := persistence.purges[0]
			second := store.RetentionPurgeObject{RetirementID: model.NewRetentionRetirementID(), ObjectID: model.NewAttemptWorkspaceObjectID()}
			persistence.purges = append(persistence.purges, second)
			content := &selectiveRetentionPurger{}
			if failure == "content" {
				content.fail = first.ObjectID
			} else {
				persistence.completeFailFor = first.ObjectID
			}
			handler := retentionHandler{records: persistence, content: content, jobs: enqueuer, now: model.NowUTC, purge: true}
			outcome := handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
			require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
			require.NotContains(t, outcome.Err.Error(), "private")
			require.Len(t, content.seen, 2, "healthy object starved: calls")
			require.Len(t, persistence.completedObjects, 1, "healthy object starved: calls")
			require.Equal(t, second.ObjectID, persistence.completedObjects[0], "healthy object starved: calls")
			require.Equal(t, 2, record.WorkReserved, "healthy object starved: calls")
			var cp RetentionCheckpointV1
			err := json.Unmarshal(record.Checkpoint, &cp)
			require.NoError(t, err, "failure claimed completion: %#v (%v)", cp, err)
			require.Equal(t, 2, cp.Examined, "failure claimed completion: %#v (%v)", cp, err)
			require.Equal(t, 1, cp.Changed, "failure claimed completion: %#v (%v)", cp, err)
			outcome = handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
			require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind, "exhausted failed batch returned success or amplified retries")
			require.Empty(t, enqueuer.enqueued, "exhausted failed batch returned success or amplified retries")
			require.Len(t, content.seen, 2, "exhausted failed batch returned success or amplified retries")
		})
	}
}

func TestRetentionPurgeUncertaintyCannotRestartBudgetOrEmptySuccessorChain(t *testing.T) {
	for _, failure := range []string{"begin", "checkpoint", "empty_checkpoint"} {
		t.Run(failure, func(t *testing.T) {
			record, persistence, enqueuer := retentionJobFixture(t, model.JobTypeRetentionPurge, 2)
			content := &retentionJobPurgerFake{}
			checkpoint := keepRetentionCheckpoint(record)
			if failure == "begin" {
				persistence.beginPurge = func(context.Context, int) ([]store.RetentionPurgeObject, error) {
					return nil, errors.New("commit acknowledgement lost")
				}
			} else {
				checkpoint = func(context.Context, jobengine.CheckpointValue) error {
					return errors.New("checkpoint acknowledgement lost")
				}
				if failure == "empty_checkpoint" {
					persistence.purges = nil
				}
			}
			handler := retentionHandler{records: persistence, content: content, jobs: enqueuer, now: model.NowUTC, purge: true}
			outcome := handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), checkpoint))
			require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
			require.Equal(t, 2, record.WorkReserved)
			require.Equal(t, 1, persistence.listed)
			for range 3 {
				outcome = handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), checkpoint))
				require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind)
			}
			require.Equal(t, 1, persistence.listed, "uncertainty amplified inventory or created successor Jobs")
			require.Equal(t, 2, record.WorkReserved, "uncertainty amplified inventory or created successor Jobs")
			require.Empty(t, enqueuer.enqueued, "uncertainty amplified inventory or created successor Jobs")
		})
	}
}

func TestRetentionPurgeCompletedFullBatchHasOneFiniteSuccessor(t *testing.T) {
	record, persistence, enqueuer := retentionJobFixture(t, model.JobTypeRetentionPurge, 1)
	content := &retentionJobPurgerFake{}
	handler := retentionHandler{records: persistence, content: content, jobs: enqueuer, now: model.NowUTC, purge: true}
	for range 2 {
		outcome := handler.Run(context.Background(), testJobExecution(record, reserveRetentionWork(record), keepRetentionCheckpoint(record)))
		require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	}
	require.Len(t, enqueuer.enqueued, 1, "completed batch duplicated work or its successor")
	require.Equal(t, 1, content.calls, "completed batch duplicated work or its successor")
	next := enqueuer.enqueued[0]
	persistence.purges = nil
	outcome := handler.Run(context.Background(), testJobExecution(next, reserveRetentionWork(next), keepRetentionCheckpoint(next)))
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind, "empty completed inventory continued spawning successors")
	require.Len(t, enqueuer.enqueued, 1, "empty completed inventory continued spawning successors")
}
