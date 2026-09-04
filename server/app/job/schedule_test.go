// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type occurrenceProposerFake struct {
	calls chan time.Time
	mu    sync.Mutex
	fail  int
}

func (f *occurrenceProposerFake) Propose(_ context.Context, at time.Time) error {
	f.calls <- at
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("database unavailable")
	}
	return nil
}

type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers chan *manualTimer
}

func newManualClock(at time.Time) *manualClock {
	return &manualClock{now: at, timers: make(chan *manualTimer, 8)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTimer(delay time.Duration) Timer {
	timer := &manualTimer{delay: delay, ch: make(chan time.Time, 1)}
	c.timers <- timer
	return timer
}

func (c *manualClock) advance(timer *manualTimer, at time.Time) {
	c.mu.Lock()
	c.now = at
	c.mu.Unlock()
	timer.ch <- at
}

type manualTimer struct {
	delay time.Duration
	ch    chan time.Time
}

func (t *manualTimer) C() <-chan time.Time { return t.ch }
func (*manualTimer) Stop() bool            { return true }

func TestEngineRetriesSameOccurrenceWakesLocallyAndStopsRecurrence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	clock := newManualClock(at)
	proposer := &occurrenceProposerFake{calls: make(chan time.Time, 3), fail: 1}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	persistence := &jobRunnerStoreFake{claimRequests: make(chan struct{}, 8)}
	engine, err := New(Config{
		Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{},
		Policy: Policy{PollInterval: time.Hour, ProposalRetryDelay: 10 * time.Second}, Clock: clock,
		Recurrences: []Recurrence{{Name: "daily", Proposer: proposer}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := <-proposer.calls
	retryTimer := <-clock.timers
	if !first.Equal(at) || retryTimer.delay != 10*time.Second {
		t.Fatalf("first occurrence=%v retry delay=%v", first, retryTimer.delay)
	}
	// The worker polls once on startup. A failed proposal must not add a wake.
	select {
	case <-persistence.claimRequests:
	case <-time.After(time.Second):
		t.Fatal("worker did not perform its initial claim")
	}
	select {
	case <-persistence.claimRequests:
		t.Fatal("failed proposal woke the local worker")
	default:
	}
	clock.advance(retryTimer, at.Add(10*time.Second))
	if second := <-proposer.calls; !second.Equal(at) {
		t.Fatalf("retry occurrence=%v, want %v", second, at)
	}
	select {
	case <-persistence.claimRequests:
	case <-time.After(time.Second):
		t.Fatal("successful proposal did not wake the local worker")
	}
	dailyTimer := <-clock.timers
	if dailyTimer.delay != 14*time.Hour+29*time.Minute+50*time.Second {
		t.Fatalf("next daily delay=%v", dailyTimer.delay)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	clock.advance(dailyTimer, nextDailyOccurrence(at))
	select {
	case occurrence := <-proposer.calls:
		t.Fatalf("proposal ran after engine shutdown: %v", occurrence)
	default:
	}
}

func TestEngineCopiesRecurrenceDefinitionsAtConstruction(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	clock := newManualClock(at)
	original := &occurrenceProposerFake{calls: make(chan time.Time, 1)}
	replacement := &occurrenceProposerFake{calls: make(chan time.Time, 1)}
	recurrences := []Recurrence{{Name: "daily", Proposer: original}}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	engine, err := New(Config{
		Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{},
		Policy: Policy{PollInterval: time.Hour}, Clock: clock, Recurrences: recurrences,
	})
	if err != nil {
		t.Fatal(err)
	}
	recurrences[0] = Recurrence{Name: "replacement", Proposer: replacement}
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-original.calls:
	case <-time.After(time.Second):
		t.Fatal("constructed recurrence did not run")
	}
	select {
	case <-replacement.calls:
		t.Fatal("engine retained caller-owned recurrence slice")
	default:
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentEnginesAndRestartProposeTheSameOccurrence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	proposer := &occurrenceProposerFake{calls: make(chan time.Time, 3)}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	newEngine := func(nodeID string) *Engine {
		t.Helper()
		engine, err := New(Config{
			Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: nodeID, Diagnostics: &jobDiagnosticsFake{},
			Policy: Policy{PollInterval: time.Hour}, Clock: newManualClock(at),
			Recurrences: []Recurrence{{Name: "daily", Proposer: proposer}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}

	first, second := newEngine("node-a"), newEngine("node-b")
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if occurrence := <-proposer.calls; !occurrence.Equal(at) {
			t.Fatalf("concurrent occurrence=%v, want %v", occurrence, at)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newEngine("node-a-restarted")
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if occurrence := <-proposer.calls; !occurrence.Equal(at) {
		t.Fatalf("restart occurrence=%v, want %v", occurrence, at)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
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
