// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type deduplicatingJobEnqueuerFake struct {
	jobs map[string]*model.Job
}

func (f *deduplicatingJobEnqueuerFake) Enqueue(_ context.Context, input *store.JobEnqueue) (*model.Job, bool, error) {
	key := string(input.Job.Type) + ":" + input.Job.DedupeKey
	if existing := f.jobs[key]; existing != nil {
		return existing, false, nil
	}
	f.jobs[key] = input.Job
	return input.Job, true, nil
}

func TestDailyMaintenanceProposalsUsePermanentDedupeAcrossNodes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	jobs := &deduplicatingJobEnqueuerFake{jobs: map[string]*model.Job{}}
	first := filePurgeExpiredContentProposer{jobs: jobs, now: func() time.Time { return at }}
	second := filePurgeExpiredContentProposer{jobs: jobs, now: func() time.Time { return at.Add(time.Second) }}
	require.NoError(t, first.Propose(context.Background(), at))
	require.NoError(t, second.Propose(context.Background(), at))
	require.NoError(t, (defaultProfilePictureReconciliationJobProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.NoError(t, (jobHistoryCleanupProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.NoError(t, (commandOutcomeCleanupProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.NoError(t, (mailCleanupProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.NoError(t, (invitationMaintenanceProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.NoError(t, (examSittingLifecycleRecoveryProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at))
	require.Len(t, jobs.jobs, 7, "logical jobs")
	for _, job := range jobs.jobs {
		require.Equal(t, model.JobDedupePermanent, job.DedupePolicy)
	}
}
