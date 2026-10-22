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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
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
	result := handler.Run(context.Background(), testJobExecution(record, reserve, func(context.Context, job.CheckpointValue) error { return errors.New("crash") }))
	require.Equal(t, job.OutcomeRetryableFailure, result.Kind, "checkpoint crash not observed")
	require.Equal(t, 1, notices.called, "checkpoint crash not observed")
	for range 2 {
		result = handler.Run(context.Background(), testJobExecution(record, reserve, keepRetentionCheckpoint(record)))
		require.Equal(t, job.OutcomeSucceeded, result.Kind)
	}
	require.Equal(t, 1, notices.called, "crash reused budget or duplicated continuation")
	require.Len(t, jobs.enqueued, 1, "crash reused budget or duplicated continuation")
}
func TestRetentionNoticesLeaseLossDoesNotDispatch(t *testing.T) {
	t.Parallel()
	record, _, jobs := retentionJobFixture(t, model.JobTypeRetentionNotices, 1)
	notices := &retentionNoticesFake{items: []model.RetentionNotice{{RetirementID: model.NewRetentionRetirementID(), RecipientUserID: model.NewUserID()}}}
	result := (retentionNoticeHandler{notices: notices, dispatcher: notices, jobs: jobs, now: model.NowUTC}).Run(context.Background(), testJobExecution(record, func(context.Context, int, int) (bool, error) { return false, errors.New("lost lease") }, keepRetentionCheckpoint(record)))
	require.Equal(t, job.OutcomeRetryableFailure, result.Kind, "lost claim dispatched notice")
	require.Equal(t, 0, notices.called, "lost claim dispatched notice")
}
