// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"errors"
	"testing"
	"time"
)

type occurrenceProposerFake struct{ proposed chan time.Time }

func (f occurrenceProposerFake) Propose(_ context.Context, at time.Time) error {
	f.proposed <- at
	return nil
}

type retryingOccurrenceProposerFake struct {
	calls chan time.Time
	count int
}

func (f *retryingOccurrenceProposerFake) Propose(_ context.Context, at time.Time) error {
	f.count++
	f.calls <- at
	if f.count == 1 {
		return errors.New("database unavailable")
	}
	return nil
}

func TestDailyProposalRunsWithoutBlockingReadinessAndStopsWithEngine(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	proposed := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDailyProposal(ctx, dailyProposal{name: "test", proposer: occurrenceProposerFake{proposed: proposed}}, &jobDiagnosticsFake{}, func() time.Time { return at }, time.Millisecond)
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

func TestNextDailyOccurrenceUsesUTCDayBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 10, 23, 59, 0, 0, time.FixedZone("offset", 2*60*60))
	want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	if got := nextDailyOccurrence(now); !got.Equal(want) {
		t.Fatalf("next occurrence = %v, want %v", got, want)
	}
}

func TestDailyProposalRetriesTheSameOccurrenceAfterTransientFailure(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan time.Time, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDailyProposal(ctx, dailyProposal{name: "test", proposer: &retryingOccurrenceProposerFake{calls: calls}}, &jobDiagnosticsFake{}, func() time.Time { return at }, time.Millisecond)
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
