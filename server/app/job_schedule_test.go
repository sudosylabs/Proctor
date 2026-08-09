// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type jobOccurrenceProposerFake struct{ proposed chan time.Time }

func (f jobOccurrenceProposerFake) Propose(_ context.Context, at time.Time) error {
	f.proposed <- at
	return nil
}

type retryingJobOccurrenceProposerFake struct {
	calls chan time.Time
	count int
}

func (f *retryingJobOccurrenceProposerFake) Propose(_ context.Context, at time.Time) error {
	f.count++
	f.calls <- at
	if f.count == 1 {
		return errors.New("database unavailable")
	}
	return nil
}

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

func TestDailyJobProposalRunsOnEveryNodeWithoutBlockingReadiness(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	proposed := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDailyJobProposal(ctx, dailyJobProposal{name: "test", proposer: jobOccurrenceProposerFake{proposed: proposed}}, &jobDiagnosticsFake{}, func() time.Time { return at }, time.Millisecond)
	}()
	select {
	case got := <-proposed:
		if !got.Equal(at) {
			t.Fatalf("proposal time = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("daily proposal blocked startup")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("daily proposal did not stop with its owner")
	}
}

func TestNextDailyJobOccurrenceUsesUTCDayBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 10, 23, 59, 0, 0, time.FixedZone("offset", 2*60*60))
	want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	if got := nextDailyJobOccurrence(now); !got.Equal(want) {
		t.Fatalf("next occurrence = %v, want %v", got, want)
	}
}

func TestDailyJobProposalRetriesTheSameOccurrenceAfterTransientFailure(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan time.Time, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDailyJobProposal(ctx, dailyJobProposal{name: "test", proposer: &retryingJobOccurrenceProposerFake{calls: calls}}, &jobDiagnosticsFake{}, func() time.Time { return at }, time.Millisecond)
	}()
	first := <-calls
	second := <-calls
	if !first.Equal(at) || !second.Equal(at) {
		t.Fatalf("retry occurrences = %v, %v", first, second)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retrying proposal did not stop")
	}
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
	if len(jobs.jobs) != 3 {
		t.Fatalf("logical jobs = %d, want 3", len(jobs.jobs))
	}
	for _, job := range jobs.jobs {
		if job.DedupePolicy != model.JobDedupePermanent {
			t.Fatalf("daily job dedupe policy = %q", job.DedupePolicy)
		}
	}
}
