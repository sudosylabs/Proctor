// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"errors"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"testing"
)

type retentionNoticesFake struct {
	items  []model.RetentionNotice
	called int
	fail   error
}

func (f *retentionNoticesFake) ListPendingNotices(context.Context, int) ([]model.RetentionNotice, error) {
	return f.items, nil
}
func (f *retentionNoticesFake) DispatchRetentionNotice(context.Context, model.RetentionNotice) error {
	f.called++
	return f.fail
}
func TestRetentionNoticesKeepCommittedBudgetAcrossFailedCheckpoint(t *testing.T) {
	t.Parallel()
	record, _, jobs := retentionJobFixture(t, model.JobTypeRetentionNotices, 1)
	notices := &retentionNoticesFake{items: []model.RetentionNotice{{RetirementID: model.NewRetentionRetirementID(), RecipientUserID: model.NewUserID()}}}
	handler := retentionNoticeHandler{notices: notices, dispatcher: notices, jobs: jobs, now: model.NowUTC}
	reserve := func(_ context.Context, units, limit int) (bool, error) {
		record.WorkReserved += units
		return true, nil
	}
	result := handler.Run(context.Background(), testJobExecution(record, reserve, func(context.Context, jobengine.CheckpointValue) error { return errors.New("crash") }))
	if result.Kind != jobengine.OutcomeRetryableFailure || notices.called != 1 {
		t.Fatal("checkpoint crash not observed")
	}
	for range 2 {
		result = handler.Run(context.Background(), testJobExecution(record, reserve, keepRetentionCheckpoint(record)))
		if result.Kind != jobengine.OutcomeSucceeded {
			t.Fatalf("retry=%#v", result)
		}
	}
	if notices.called != 1 || len(jobs.enqueued) != 1 {
		t.Fatal("crash reused budget or duplicated continuation")
	}
}
func TestRetentionNoticesLeaseLossDoesNotDispatch(t *testing.T) {
	t.Parallel()
	record, _, jobs := retentionJobFixture(t, model.JobTypeRetentionNotices, 1)
	notices := &retentionNoticesFake{items: []model.RetentionNotice{{RetirementID: model.NewRetentionRetirementID(), RecipientUserID: model.NewUserID()}}}
	result := (retentionNoticeHandler{notices: notices, dispatcher: notices, jobs: jobs, now: model.NowUTC}).Run(context.Background(), testJobExecution(record, func(context.Context, int, int) (bool, error) { return false, errors.New("lost lease") }, keepRetentionCheckpoint(record)))
	if result.Kind != jobengine.OutcomeRetryableFailure || notices.called != 0 {
		t.Fatal("lost claim dispatched notice")
	}
}
