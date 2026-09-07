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

type RetentionExpiryMaintenanceStore interface {
	GetControl(context.Context) (*model.RetentionControl, error)
	ListExpiryRecords(context.Context, store.RetentionExpiryListOptions) (*store.RetentionExpiryPage, error)
	ReconcileExpiry(context.Context, *store.RetentionExpiryReconciliation) (*store.RetentionExpiryResult, error)
	ReconcileCleanupAuditExpiry(context.Context, *store.RetentionCleanupAuditReconciliation) (*store.RetentionCleanupAuditResult, error)
}

type RetentionExpirySystemAuditor interface {
	BeginRetentionExpiry(context.Context, model.InstitutionID, model.RetentionExpiryKind, string) (string, error)
	FailRetention(context.Context, string) error
	PrepareRetentionCleanupAudit(context.Context, model.InstitutionID) (*model.AuditEvent, error)
}

type RetentionExpiryCommandV1 struct {
	CleanupAudit bool                      `json:"cleanup_audit,omitempty"`
	Kind         model.RetentionExpiryKind `json:"kind"`
	BatchSize    int                       `json:"batch_size"`
	AfterID      string                    `json:"after_id,omitempty"`
	Before       time.Time                 `json:"before,omitempty"`
}

type RetentionExpiryCheckpointV1 struct {
	AfterID  string    `json:"after_id,omitempty"`
	Before   time.Time `json:"before,omitempty"`
	Examined int       `json:"examined"`
	Changed  int       `json:"changed"`
	Done     bool      `json:"done"`
}

type retentionExpiryHandler struct {
	records RetentionExpiryMaintenanceStore
	audit   RetentionExpirySystemAuditor
	jobs    JobEnqueuer
	now     func() time.Time
}

func (h retentionExpiryHandler) Run(ctx context.Context, e jobengine.Execution) jobengine.Outcome {
	if e.Job == nil || h.records == nil || h.audit == nil || h.jobs == nil || h.now == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("retention expiry dependencies unavailable"))
	}
	var command RetentionExpiryCommandV1
	if e.Job.CommandVersion != 1 || decodeStrictJobDocument(e.Job.Command, &command) != nil || !command.Kind.IsValid() || (command.CleanupAudit && command.Kind != model.RetentionExpiryAudit) || command.BatchSize < 1 || command.BatchSize > retentionJobMaximumBatch ||
		(command.AfterID != "" && !model.IsValidId(command.AfterID)) || (command.AfterID != "" && command.Before.IsZero()) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("invalid retention expiry command"))
	}
	cp := RetentionExpiryCheckpointV1{AfterID: command.AfterID, Before: command.Before}
	if len(e.Job.Checkpoint) > 0 && (e.Job.CheckpointVersion != 1 || decodeStrictJobDocument(e.Job.Checkpoint, &cp) != nil) {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("invalid retention expiry checkpoint"))
	}
	if cp.Examined < 0 || cp.Changed < 0 || cp.Changed > cp.Examined || cp.Examined > command.BatchSize || e.Job.WorkReserved < cp.Examined || e.Job.WorkReserved > command.BatchSize || cp.AfterID < command.AfterID ||
		(e.Job.WorkReserved > 0 && cp.Before.IsZero()) ||
		(cp.AfterID != "" && (!model.IsValidId(cp.AfterID) || cp.Before.IsZero())) || (!command.Before.IsZero() && !cp.Before.Equal(command.Before)) {
		return jobengine.PermanentFailure("job.checkpoint.invalid", errors.New("invalid retention expiry work budget or cursor"))
	}
	remaining := command.BatchSize - e.Job.WorkReserved
	if cp.Done || remaining == 0 {
		return h.finish(ctx, e.Job, command, cp)
	}
	control, err := h.records.GetControl(ctx)
	if store.IsNotFound(err) {
		cp.Done = true
		return h.finish(ctx, e.Job, command, cp)
	}
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	if control.Validate() != nil {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("invalid retention expiry control"))
	}
	if control.State != model.RetentionControlEnabled {
		cp.Done = true
		return h.finish(ctx, e.Job, command, cp)
	}
	page, err := h.records.ListExpiryRecords(ctx, store.RetentionExpiryListOptions{Kind: command.Kind, CleanupAudit: command.CleanupAudit, AfterID: cp.AfterID, Before: cp.Before, Limit: remaining})
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	if page == nil || page.InstitutionID != control.InstitutionID || page.Before.IsZero() || (!cp.Before.IsZero() && !page.Before.Equal(cp.Before)) || len(page.Items) > remaining || (page.HasMore && len(page.Items) == 0) {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("invalid retention expiry page"))
	}
	// Persist the finite scan boundary before reserving or mutating a record.
	// If this checkpoint is uncertain, retry may establish a new boundary only
	// while no work was spent; an exhausted parent cannot silently widen it.
	if cp.Before.IsZero() {
		cp.Before = page.Before
		document, err := json.Marshal(cp)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		if err = e.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document}); err != nil {
			return jobengine.RetryableFailure("retention.unavailable", err)
		}
	}
	if command.CleanupAudit {
		return h.cleanupAuditBatch(ctx, e, command, cp, control.InstitutionID, remaining)
	}
	for _, record := range page.Items {
		if record.Validate() != nil || record.Kind != command.Kind || record.ID <= cp.AfterID {
			return jobengine.PermanentFailure("job.invariant_failed", errors.New("invalid retention expiry record"))
		}
		reserved, err := e.ReserveWork(ctx, 1, command.BatchSize)
		if err != nil {
			return jobengine.RetryableFailure("retention.unavailable", err)
		}
		if !reserved {
			return h.finish(ctx, e.Job, command, cp)
		}
		needsChange := !record.ScheduledAt.Valid && record.Blocker == model.RetentionExpiryEligible ||
			record.ScheduledAt.Valid && (record.Blocker != model.RetentionExpiryEligible || !record.ExpiresAfter.Time.After(model.TimeUTC(h.now())))
		if needsChange {
			auditID, err := h.audit.BeginRetentionExpiry(ctx, control.InstitutionID, record.Kind, record.ID)
			if err != nil {
				return jobengine.RetryableFailure("retention.unavailable", err)
			}
			result, err := h.records.ReconcileExpiry(ctx, &store.RetentionExpiryReconciliation{Kind: record.Kind, RecordID: record.ID, AuditEventID: auditID, AuditAt: model.MillisFromTime(h.now())})
			if err != nil {
				return jobengine.RetryableFailure("retention.unavailable", errors.Join(err, h.audit.FailRetention(ctx, auditID)))
			}
			if result == nil || result.Scheduled && result.Expired {
				return jobengine.PermanentFailure("job.invariant_failed", errors.New("invalid retention expiry outcome"))
			}
			if result.Scheduled || result.Cancelled || result.Expired {
				cp.Changed++
			}
		}
		cp.AfterID = record.ID
		cp.Examined++
		document, err := json.Marshal(cp)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		if err = e.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document}); err != nil {
			return jobengine.RetryableFailure("retention.unavailable", err)
		}
	}
	cp.Done = !page.HasMore
	return h.finish(ctx, e.Job, command, cp)
}

func (h retentionExpiryHandler) finish(ctx context.Context, parent *model.Job, command RetentionExpiryCommandV1, cp RetentionExpiryCheckpointV1) jobengine.Outcome {
	if !cp.Done {
		command.AfterID = cp.AfterID
		command.Before = cp.Before
		document, err := json.Marshal(command)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		at := model.TimeUTC(h.now())
		successor, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeRetentionExpire, 1, document, "retention.expire:after:"+parent.ID.String(), model.JobDedupePermanent, at, at.Add(time.Second), 5)
		if err != nil {
			return jobengine.PermanentFailure("job.invariant_failed", err)
		}
		if _, _, err = h.jobs.Enqueue(ctx, &store.JobEnqueue{Job: successor}); err != nil {
			return jobengine.RetryableFailure("retention.unavailable", err)
		}
	}
	document, err := json.Marshal(cp)
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type retentionExpiryProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p retentionExpiryProposer) Propose(ctx context.Context, occurrence time.Time) error {
	at := model.TimeUTC(p.now())
	for _, kind := range []struct {
		value   model.RetentionExpiryKind
		cleanup bool
		suffix  string
	}{{model.RetentionExpiryAudit, false, "audit"}, {model.RetentionExpiryReceipt, false, "receipt"}, {model.RetentionExpiryAudit, true, "cleanup_audit"}} {
		document, err := json.Marshal(RetentionExpiryCommandV1{Kind: kind.value, CleanupAudit: kind.cleanup, BatchSize: retentionJobMaximumBatch})
		if err != nil {
			return err
		}
		job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeRetentionExpire, 1, document, "retention.expire:"+kind.suffix+":"+model.TimeUTC(occurrence).Format("2006-01-02T15"), model.JobDedupePermanent, at, at, 5)
		if err != nil {
			return err
		}
		if _, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job}); err != nil {
			return err
		}
	}
	return nil
}

func (h retentionExpiryHandler) cleanupAuditBatch(ctx context.Context, e jobengine.Execution, command RetentionExpiryCommandV1, cp RetentionExpiryCheckpointV1, institution model.InstitutionID, limit int) jobengine.Outcome {
	reserved, err := e.ReserveWork(ctx, limit, command.BatchSize)
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	if !reserved {
		return h.finish(ctx, e.Job, command, cp)
	}
	prepared, err := h.audit.PrepareRetentionCleanupAudit(ctx, institution)
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	result, err := h.records.ReconcileCleanupAuditExpiry(ctx, &store.RetentionCleanupAuditReconciliation{AfterID: cp.AfterID, Before: cp.Before, Limit: limit, Audit: prepared})
	if err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	if result == nil || result.Examined < 0 || result.Examined > limit || result.Changed < 0 || result.Changed > result.Examined || result.Scheduled < 0 || result.Scheduled > result.Changed || result.Cancelled < 0 || result.Cancelled > result.Changed || result.Expired < 0 || result.Expired > result.Changed ||
		!result.Before.Equal(cp.Before) || result.AfterID < cp.AfterID || (result.AfterID != "" && !model.IsValidId(result.AfterID)) || (result.HasMore && result.Examined == 0) {
		return jobengine.PermanentFailure("job.invariant_failed", errors.New("invalid cleanup audit batch result"))
	}
	cp.Examined += result.Examined
	cp.Changed += result.Changed
	cp.AfterID = result.AfterID
	cp.Done = !result.HasMore
	document, err := json.Marshal(cp)
	if err != nil {
		return jobengine.PermanentFailure("job.invariant_failed", err)
	}
	if err = e.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document}); err != nil {
		return jobengine.RetryableFailure("retention.unavailable", err)
	}
	return h.finish(ctx, e.Job, command, cp)
}
