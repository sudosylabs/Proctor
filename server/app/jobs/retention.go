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
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const retentionJobMaximumBatch = 100

type RetentionMaintenanceStore interface {
	GetControl(context.Context) (*model.RetentionControl, error)
	ListRecords(context.Context, store.RetentionRecordListOptions) (*store.RetentionRecordPage, error)
	ReconcileSubmission(context.Context, *store.RetentionReconciliation) (*store.RetentionReconciliationResult, error)
	BeginPurgeBatch(context.Context, int) ([]store.RetentionPurgeObject, error)
	CompletePurge(context.Context, *store.RetentionPurgeCompletion) error
}

type RetentionContentPurger interface {
	PurgeRetiredAttemptWorkspaceObject(context.Context, model.AttemptWorkspaceObjectID) error
}

type RetentionSystemAuditor interface {
	BeginRetention(context.Context, model.InstitutionID, model.SubmissionID) (string, error)
	FailRetention(context.Context, string) error
}

type RetentionCommandV1 struct {
	BatchSize         int                `json:"batch_size"`
	AfterSubmissionID model.SubmissionID `json:"after_submission_id,omitempty"`
}

type RetentionCheckpointV1 struct {
	AfterSubmissionID model.SubmissionID `json:"after_submission_id,omitempty"`
	Examined          int                `json:"examined"`
	Changed           int                `json:"changed"`
	Done              bool               `json:"done"`
}

type retentionHandler struct {
	records RetentionMaintenanceStore
	content RetentionContentPurger
	audit   RetentionSystemAuditor
	jobs    JobEnqueuer
	now     func() time.Time
	purge   bool
}

func (h retentionHandler) Run(ctx context.Context, e jobengine.Execution) jobengine.Outcome {
	if e.Job == nil || h.records == nil || h.jobs == nil || h.now == nil || (!h.purge && h.audit == nil) || (h.purge && h.content == nil) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("retention dependencies are unavailable"))
	}
	var command RetentionCommandV1
	if e.Job.CommandVersion != 1 || decodeStrictJobDocument(e.Job.Command, &command) != nil || command.BatchSize < 1 || command.BatchSize > retentionJobMaximumBatch ||
		(!command.AfterSubmissionID.IsZero() && !command.AfterSubmissionID.IsValid()) || (h.purge && !command.AfterSubmissionID.IsZero()) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("invalid retention command"))
	}
	cp := RetentionCheckpointV1{AfterSubmissionID: command.AfterSubmissionID}
	if len(e.Job.Checkpoint) > 0 && (e.Job.CheckpointVersion != 1 || decodeStrictJobDocument(e.Job.Checkpoint, &cp) != nil) {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("invalid retention checkpoint"))
	}
	if cp.Examined < 0 || cp.Examined > command.BatchSize || cp.Changed < 0 || cp.Changed > cp.Examined ||
		(!cp.AfterSubmissionID.IsZero() && !cp.AfterSubmissionID.IsValid()) || cp.AfterSubmissionID < command.AfterSubmissionID ||
		e.Job.WorkReserved < cp.Examined || e.Job.WorkReserved > command.BatchSize || (h.purge && !cp.AfterSubmissionID.IsZero()) {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("retention work or cursor is invalid"))
	}
	remaining := command.BatchSize - e.Job.WorkReserved
	if cp.Done || remaining == 0 {
		return h.finish(ctx, e.Job, command, cp)
	}
	var err error
	if h.purge {
		err = h.purgePage(ctx, e, command, &cp, remaining)
	} else {
		err = h.reconcilePage(ctx, e, command, &cp, remaining)
	}
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
	}
	return h.finish(ctx, e.Job, command, cp)
}

func (h retentionHandler) reconcilePage(ctx context.Context, e jobengine.Execution, command RetentionCommandV1, cp *RetentionCheckpointV1, limit int) error {
	control, err := h.records.GetControl(ctx)
	if store.IsNotFound(err) {
		cp.Done = true
		return saveRetentionCheckpoint(ctx, e, *cp)
	}
	if err != nil {
		return err
	}
	if control.Validate() != nil {
		return errors.New("invalid retention control")
	}
	if control.State != model.RetentionControlEnabled {
		cp.Done = true
		return saveRetentionCheckpoint(ctx, e, *cp)
	}
	page, err := h.records.ListRecords(ctx, store.RetentionRecordListOptions{AfterSubmissionID: cp.AfterSubmissionID, Limit: limit})
	if err != nil {
		return err
	}
	if page == nil || len(page.Items) > 2*limit || len(page.Items)%2 != 0 || (page.HasMore && len(page.Items) == 0) {
		return errors.New("invalid retention page")
	}
	for i := 0; i < len(page.Items); i += 2 {
		work, integrity := page.Items[i], page.Items[i+1]
		id := work.Record.Scope.SubmissionID
		if !id.IsValid() || id <= cp.AfterSubmissionID || integrity.Record.Scope != work.Record.Scope || work.Record.Category != model.RetentionCategoryWork || integrity.Record.Category != model.RetentionCategoryIntegrity {
			return errors.New("invalid retention category ordering")
		}
		reserved, err := e.ReserveWork(ctx, 1, command.BatchSize)
		if err != nil {
			return err
		}
		if !reserved {
			return nil
		}
		needed := work.Eligibility.Blocker == model.RetentionBlockerNone || integrity.Eligibility.Blocker == model.RetentionBlockerNone ||
			(work.Retirement != nil && work.Retirement.State == model.RetentionRetirementGrace) || (integrity.Retirement != nil && integrity.Retirement.State == model.RetentionRetirementGrace)
		if needed {
			auditID, err := h.audit.BeginRetention(ctx, control.InstitutionID, id)
			if err != nil {
				return err
			}
			result, err := h.records.ReconcileSubmission(ctx, &store.RetentionReconciliation{SubmissionID: id, AuditEventID: auditID, AuditAt: model.MillisFromTime(h.now())})
			if err != nil {
				return errors.Join(err, h.audit.FailRetention(ctx, auditID))
			}
			if result == nil || result.Scheduled < 0 || result.Cancelled < 0 || result.Retired < 0 || result.Scheduled > 2 || result.Cancelled > 2 || result.Retired > 2 {
				return errors.New("invalid retention result")
			}
			if result.Scheduled+result.Cancelled+result.Retired > 0 {
				cp.Changed++
			}
		}
		cp.Examined++
		cp.AfterSubmissionID = id
		if err = saveRetentionCheckpoint(ctx, e, *cp); err != nil {
			return err
		}
	}
	cp.Done = !page.HasMore
	return saveRetentionCheckpoint(ctx, e, *cp)
}

func (h retentionHandler) purgePage(ctx context.Context, e jobengine.Execution, command RetentionCommandV1, cp *RetentionCheckpointV1, limit int) error {
	// Beginning a batch is itself a durable mutation. Reserve its complete
	// bounded cost first; an unknown result cannot reset this Job's budget.
	reserved, err := e.ReserveWork(ctx, limit, command.BatchSize)
	if err != nil {
		return err
	}
	if !reserved {
		return nil
	}
	objects, err := h.records.BeginPurgeBatch(ctx, limit)
	if err != nil {
		return err
	}
	if len(objects) > limit {
		return errors.New("invalid retention purge page")
	}
	seen := make(map[store.RetentionPurgeObject]struct{}, len(objects))
	for _, object := range objects {
		if !object.RetirementID.IsValid() || !object.ObjectID.IsValid() {
			return errors.New("invalid retention purge identity")
		}
		if _, duplicate := seen[object]; duplicate {
			return errors.New("duplicate retention purge identity")
		}
		seen[object] = struct{}{}
	}
	failed := false
	for _, object := range objects {
		if err = ctx.Err(); err != nil {
			return err
		}
		cp.Examined++
		if err = h.content.PurgeRetiredAttemptWorkspaceObject(ctx, object.ObjectID); err != nil {
			failed = true
		} else if err = h.records.CompletePurge(ctx, &store.RetentionPurgeCompletion{RetirementID: object.RetirementID, ObjectID: object.ObjectID}); err != nil {
			failed = true
		} else {
			cp.Changed++
		}
		if err = saveRetentionCheckpoint(ctx, e, *cp); err != nil {
			return err
		}
	}
	cp.Done = len(objects) < limit
	if err = saveRetentionCheckpoint(ctx, e, *cp); err != nil {
		return err
	}
	if failed {
		return errors.New("retention purge attempts remain incomplete")
	}
	return nil
}

func saveRetentionCheckpoint(ctx context.Context, e jobengine.Execution, cp RetentionCheckpointV1) error {
	document, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return e.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document})
}

func (h retentionHandler) finish(ctx context.Context, parent *model.Job, command RetentionCommandV1, cp RetentionCheckpointV1) jobengine.Outcome {
	// A failed or uncheckpointed purge batch must remain visibly failed. Its
	// exact keys are already deferred and will be revisited by hourly work.
	// Only a fully completed, nonempty batch can create an immediate successor;
	// otherwise repeated failures saving an empty checkpoint could spawn an
	// unbounded chain of fresh Jobs with fresh work budgets.
	if h.purge && (cp.Changed != cp.Examined || (!cp.Done && cp.Examined != command.BatchSize)) {
		return jobengine.PermanentFailure("retention.unavailable", errors.New("retention purge batch did not complete"))
	}
	if !cp.Done {
		command.AfterSubmissionID = cp.AfterSubmissionID
		document, err := json.Marshal(command)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		at := model.TimeUTC(h.now())
		// A short delay avoids an unbounded tight retry chain after repeated
		// uncertainty, while each permanent dedupe key has one successor.
		successor, err := model.NewJobWithDedupePolicy(model.NewJobID(), parent.Type, 1, document, string(parent.Type)+":after:"+parent.ID.String(), model.JobDedupePermanent, at, at.Add(time.Second), 5)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		if _, _, err = h.jobs.Enqueue(ctx, &store.JobEnqueue{Job: successor}); err != nil {
			return jobengine.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
		}
	}
	document, err := json.Marshal(cp)
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type retentionProposer struct {
	jobs    JobEnqueuer
	now     func() time.Time
	jobType model.JobType
}

func (p retentionProposer) Propose(ctx context.Context, occurrence time.Time) error {
	document, err := json.Marshal(RetentionCommandV1{BatchSize: retentionJobMaximumBatch})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	interval := time.Hour
	if p.jobType == model.JobTypeRetentionNotices {
		interval = 10 * time.Minute
	}
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), p.jobType, 1, document, string(p.jobType)+":"+model.TimeUTC(occurrence).Truncate(interval).Format(time.RFC3339), model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func retentionDescriptor(jobType model.JobType, handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: jobType, CommandVersions: []int{1}, CheckpointVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"retention.unavailable", "job.command.invalid", "job.checkpoint.invalid", "job.invariant_failed"}, Timeout: 10 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 30 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
