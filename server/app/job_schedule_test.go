// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

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
	if err := first.Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if err := second.Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if err := (defaultProfilePictureReconciliationJobProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if err := (jobHistoryCleanupProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if err := (commandOutcomeCleanupProposer{jobs: jobs, now: func() time.Time { return at }}).Propose(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if len(jobs.jobs) != 4 {
		t.Fatalf("logical jobs = %d, want 4", len(jobs.jobs))
	}
	for _, job := range jobs.jobs {
		if job.DedupePolicy != model.JobDedupePermanent {
			t.Fatalf("daily job dedupe policy = %q", job.DedupePolicy)
		}
	}
}
