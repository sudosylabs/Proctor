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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	engine, err := New(Dependencies{
		Store: persistence, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{},
		Policy: Policy{PollInterval: time.Hour, ProposalRetryDelay: 10 * time.Second}, Clock: clock,
		Recurrences: []Recurrence{{Name: "daily", Proposer: proposer}},
	})
	require.NoError(t, err)
	err = engine.Start(context.Background())
	require.NoError(t, err)
	first := <-proposer.calls
	retryTimer := <-clock.timers
	require.True(t, first.Equal(at), "first occurrence=%v retry delay", first)
	require.Equal(t, 10*time.Second, retryTimer.delay, "first occurrence=%v retry delay", first)
	// The worker polls once on startup. A failed proposal must not add a wake.
	select {
	case <-persistence.claimRequests:
	case <-time.After(time.Second):
		require.FailNow(t, "worker did not perform its initial claim")
	}
	select {
	case <-persistence.claimRequests:
		require.FailNow(t, "failed proposal woke the local worker")
	default:
	}
	clock.advance(retryTimer, at.Add(10*time.Second))
	second := <-proposer.calls
	require.True(t, second.Equal(at), "retry occurrence=%v", second)
	select {
	case <-persistence.claimRequests:
	case <-time.After(time.Second):
		require.FailNow(t, "successful proposal did not wake the local worker")
	}
	dailyTimer := <-clock.timers
	require.Equal(t, 14*time.Hour+29*time.Minute+50*time.Second, dailyTimer.delay)
	err = engine.Close()
	require.NoError(t, err)
	clock.advance(dailyTimer, nextDailyOccurrence(at))
	select {
	case occurrence := <-proposer.calls:
		require.FailNow(t, fmt.Sprintf("proposal ran after engine shutdown: %v", occurrence))
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
	engine, err := New(Dependencies{
		Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: "node-a", Diagnostics: &jobDiagnosticsFake{},
		Policy: Policy{PollInterval: time.Hour}, Clock: clock, Recurrences: recurrences,
	})
	require.NoError(t, err)
	recurrences[0] = Recurrence{Name: "replacement", Proposer: replacement}
	err = engine.Start(context.Background())
	require.NoError(t, err)
	select {
	case <-original.calls:
	case <-time.After(time.Second):
		require.FailNow(t, "constructed recurrence did not run")
	}
	select {
	case <-replacement.calls:
		require.FailNow(t, "engine retained caller-owned recurrence slice")
	default:
	}
	err = engine.Close()
	require.NoError(t, err)
}

func TestConcurrentEnginesAndRestartProposeTheSameOccurrence(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 10, 9, 30, 0, 0, time.UTC)
	proposer := &occurrenceProposerFake{calls: make(chan time.Time, 3)}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	newEngine := func(nodeID string) *Engine {
		t.Helper()
		engine, err := New(Dependencies{
			Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: nodeID, Diagnostics: &jobDiagnosticsFake{},
			Policy: Policy{PollInterval: time.Hour}, Clock: newManualClock(at),
			Recurrences: []Recurrence{{Name: "daily", Proposer: proposer}},
		})
		require.NoError(t, err)
		return engine
	}

	first, second := newEngine("node-a"), newEngine("node-b")
	require.NoError(t, first.Start(context.Background()))
	require.NoError(t, second.Start(context.Background()))
	for range 2 {
		occurrence := <-proposer.calls
		require.True(t, occurrence.Equal(at), "concurrent occurrence=%v", occurrence)
	}
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())

	restarted := newEngine("node-a-restarted")
	require.NoError(t, restarted.Start(context.Background()))
	occurrence := <-proposer.calls
	require.True(t, occurrence.Equal(at), "restart occurrence=%v", occurrence)
	require.NoError(t, restarted.Close())
}

func TestNextDailyOccurrenceUsesUTCDayBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 10, 23, 59, 0, 0, time.FixedZone("offset", 2*60*60))
	want := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	got := nextDailyOccurrence(now)
	require.True(t, got.Equal(want), "next occurrence = %v", got)
}

func TestRecurrenceIntervalRetriesSameBucketThenAdvancesAtUTCBoundary(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 7, 13, 57, 0, 0, time.FixedZone("offset", 2*60*60))
	clock := newManualClock(at)
	proposer := &occurrenceProposerFake{calls: make(chan time.Time, 4), fail: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRecurrenceProposal(ctx, Recurrence{Name: "hourly", Interval: time.Hour, Proposer: proposer}, &jobDiagnosticsFake{}, clock, 5*time.Minute, nil, nil)
	}()
	defer func() { cancel(); <-done }()
	require.True(t, (<-proposer.calls).Equal(at))
	retry := <-clock.timers
	require.Equal(t, 5*time.Minute, retry.delay)
	// A transient failure crossing noon must first finish the old occurrence.
	clock.advance(retry, at.Add(5*time.Minute))
	require.True(t, (<-proposer.calls).Equal(at))
	require.True(t, (<-proposer.calls).Equal(at.Add(5*time.Minute)))
	next := <-clock.timers
	require.Equal(t, 58*time.Minute, next.delay)
	clock.advance(next, time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC))
	got := <-proposer.calls
	require.Equal(t, 13, got.Hour())
	require.Same(t, time.UTC, got.Location())
}

func TestRecurrenceIntervalsAreBoundedAndAlignAcrossNodes(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{-time.Hour, time.Second, 7 * time.Minute, 25 * time.Hour} {
		_, err := cloneRecurrences([]Recurrence{{Name: "invalid", Interval: interval, Proposer: noOpProposer{}}})
		assert.Error(t, err, "accepted interval %s", interval)
	}
	for _, interval := range []time.Duration{0, time.Minute, 10 * time.Minute, time.Hour, 24 * time.Hour} {
		_, err := cloneRecurrences([]Recurrence{{Name: "valid", Interval: interval, Proposer: noOpProposer{}}})
		require.NoError(t, err)
	}
	first := time.Date(2026, time.September, 7, 12, 31, 0, 0, time.UTC)
	second := first.Add(4 * time.Minute).In(time.FixedZone("offset", -5*60*60))
	want := time.Date(2026, time.September, 7, 12, 40, 0, 0, time.UTC)
	for _, now := range []time.Time{first, second} {
		got := nextRecurrenceOccurrence(now, 10*time.Minute)
		require.True(t, got.Equal(want), "boundary = %v", got)
	}
}
