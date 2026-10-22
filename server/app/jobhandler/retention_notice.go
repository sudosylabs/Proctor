// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
)

type RetentionNoticeStore interface {
	ListPendingNotices(context.Context, int) ([]model.RetentionNotice, error)
}
type RetentionNoticeDispatcher interface {
	DispatchRetentionNotice(context.Context, model.RetentionNotice) error
}
type retentionNoticeHandler struct {
	notices    RetentionNoticeStore
	dispatcher RetentionNoticeDispatcher
	jobs       JobEnqueuer
	now        func() time.Time
}

func (h retentionNoticeHandler) Run(ctx context.Context, e job.Execution) job.Outcome {
	var command RetentionCommand
	if e.Job == nil || h.notices == nil || h.dispatcher == nil || h.jobs == nil || h.now == nil || decodeStrictJobDocument(e.Job.Command, &command) != nil || command.BatchSize < 1 || command.BatchSize > retentionJobMaximumBatch || !command.AfterSubmissionID.IsZero() {
		return job.PermanentFailure("job.command.invalid", errors.New("invalid retention notice command"))
	}
	var cp RetentionCheckpoint
	if len(e.Job.Checkpoint) > 0 && (decodeStrictJobDocument(e.Job.Checkpoint, &cp) != nil) {
		return job.PermanentFailure("job.checkpoint.invalid", errors.New("invalid retention notice checkpoint"))
	}
	if cp.Examined < 0 || cp.Examined > e.Job.WorkReserved || e.Job.WorkReserved > command.BatchSize || !cp.AfterSubmissionID.IsZero() || cp.Changed != cp.Examined {
		return job.PermanentFailure("job.checkpoint.invalid", errors.New("invalid retention notice work"))
	}
	finish := retentionHandler{jobs: h.jobs, now: h.now}
	remaining := command.BatchSize - e.Job.WorkReserved
	if cp.Done || remaining == 0 {
		return finish.finish(ctx, e.Job, command, cp)
	}
	items, err := h.notices.ListPendingNotices(ctx, remaining)
	if err != nil {
		return job.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
	}
	if len(items) > remaining {
		return job.PermanentFailure("job.invariant_failed", errors.New("invalid retention notice page"))
	}
	for _, item := range items {
		reserved, err := e.ReserveWork(ctx, 1, command.BatchSize)
		if err != nil {
			return job.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
		}
		if !reserved {
			return finish.finish(ctx, e.Job, command, cp)
		}
		if err = h.dispatcher.DispatchRetentionNotice(ctx, item); err != nil {
			return job.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
		}
		cp.Examined++
		cp.Changed++
		if err = saveRetentionCheckpoint(ctx, e, cp); err != nil {
			return job.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
		}
	}
	cp.Done = len(items) < remaining
	if err = saveRetentionCheckpoint(ctx, e, cp); err != nil {
		return job.RetryableFailure("retention.unavailable", errors.New("retention maintenance did not complete"))
	}
	return finish.finish(ctx, e.Job, command, cp)
}
