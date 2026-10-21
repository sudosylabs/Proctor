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
	"fmt"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	// auditAttemptAbandonmentAge is far longer than any request or Job that
	// opens an audit attempt, so only an attempt whose requester is gone
	// reaches it.
	auditAttemptAbandonmentAge = 24 * time.Hour
	// maximumAuditAttemptSweepBatches bounds the work of one run; the next
	// hourly occurrence continues any remainder.
	maximumAuditAttemptSweepBatches = 20
)

// AuditAttemptSweepCommandV1 is the abandonment pass size.
type AuditAttemptSweepCommandV1 struct {
	BatchSize int `json:"batch_size"`
}

// AuditAttemptSweepResultV1 counts the attempts the run terminalized.
type AuditAttemptSweepResultV1 struct {
	Failed int64 `json:"failed"`
}

// AuditAttemptSweeper terminalizes abandoned audit attempts.
type AuditAttemptSweeper interface {
	FailAbandonedAttempts(context.Context, *store.AuditAttemptAbandonment) (int64, error)
}

type auditAttemptSweepHandler struct{ audits AuditAttemptSweeper }

func (h auditAttemptSweepHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("job is missing"))
	}
	if execution.Job.CommandVersion != 1 {
		return jobengine.PermanentFailure("job.command.invalid", fmt.Errorf("unsupported command version %d", execution.Job.CommandVersion))
	}
	var command AuditAttemptSweepCommandV1
	if err := decodeStrictJobDocument(execution.Job.Command, &command); err != nil ||
		command.BatchSize < 1 || command.BatchSize > store.MaximumAuditAttemptAbandonmentBatch {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("audit attempt sweep command is invalid"))
	}
	var failed int64
	for range maximumAuditAttemptSweepBatches {
		changed, err := h.audits.FailAbandonedAttempts(ctx, &store.AuditAttemptAbandonment{
			Age: auditAttemptAbandonmentAge, Limit: command.BatchSize,
		})
		if err != nil {
			return jobengine.RetryableFailure("dependency.unavailable", err)
		}
		failed += changed
		if changed < int64(command.BatchSize) {
			break
		}
	}
	document, err := json.Marshal(AuditAttemptSweepResultV1{Failed: failed})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: document, Err: err}
}

type auditAttemptSweepProposer struct {
	jobs JobEnqueuer
	now  func() time.Time
}

func (p auditAttemptSweepProposer) Propose(ctx context.Context, occurrence time.Time) error {
	command, err := json.Marshal(AuditAttemptSweepCommandV1{BatchSize: store.MaximumAuditAttemptAbandonmentBatch})
	if err != nil {
		return err
	}
	at := model.TimeUTC(p.now())
	key := "audit-attempt-sweep:" + model.TimeUTC(occurrence).Format("2006-01-02T15")
	job, err := model.NewJobWithDedupePolicy(model.NewJobID(), model.JobTypeAuditAttemptSweep, 1, command, key, model.JobDedupePermanent, at, at, 5)
	if err != nil {
		return err
	}
	_, _, err = p.jobs.Enqueue(ctx, &store.JobEnqueue{Job: job})
	return err
}

func auditAttemptSweepDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeAuditAttemptSweep, CommandVersions: []int{1}, ResultVersions: []int{1}, PublicErrorCodes: []string{"dependency.unavailable", "job.command.invalid"}, Timeout: 5 * time.Minute, Concurrency: 1, MaximumAttempts: 5, LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Second, MaximumRetryDelay: time.Minute, Visibility: jobengine.VisibilityOperator, SuccessRetention: 7 * 24 * time.Hour, FailureRetention: 90 * 24 * time.Hour, Handler: handler}
}
