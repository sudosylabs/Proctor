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

	"github.com/stretchr/testify/require"
)

type periodicTestRunner struct {
	mu    sync.Mutex
	calls int
	err   error
	seen  chan struct{}
}

func (r *periodicTestRunner) Run(context.Context) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case r.seen <- struct{}{}:
	default:
	}
	return r.err
}

func (r *periodicTestRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestClonePeriodicTasksValidatesAndClones(t *testing.T) {
	t.Parallel()
	runner := &periodicTestRunner{}
	values := []PeriodicTask{{Name: "exam-attempt-participation-expiry", Interval: 2 * time.Second, Runner: runner}}
	cloned, err := clonePeriodicTasks(values)
	require.NoError(t, err)
	values[0] = PeriodicTask{Name: "replacement", Interval: time.Hour, Runner: runner}
	require.Equal(t, "exam-attempt-participation-expiry", cloned[0].Name)
	require.Equal(t, 2*time.Second, cloned[0].Interval)

	tests := []struct {
		name  string
		value []PeriodicTask
	}{
		{name: "empty name", value: []PeriodicTask{{Interval: time.Second, Runner: runner}}},
		{name: "unsafe name", value: []PeriodicTask{{Name: "UPPER", Interval: time.Second, Runner: runner}}},
		{name: "nonpositive interval", value: []PeriodicTask{{Name: "scan", Runner: runner}}},
		{name: "missing runner", value: []PeriodicTask{{Name: "scan", Interval: time.Second}}},
		{name: "duplicate", value: []PeriodicTask{{Name: "scan", Interval: time.Second, Runner: runner}, {Name: "scan", Interval: 2 * time.Second, Runner: runner}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := clonePeriodicTasks(test.value)
			require.Error(t, err, "clonePeriodicTasks() accepted invalid definition")
		})
	}
}

func TestRunPeriodicTaskRunsImmediatelyAndOnEachInterval(t *testing.T) {
	t.Parallel()
	clock := newManualClock(time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC))
	runner := &periodicTestRunner{seen: make(chan struct{}, 3)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runPeriodicTask(ctx, PeriodicTask{Name: "scan", Interval: 2 * time.Second, Runner: runner}, &jobDiagnosticsFake{}, clock, nil)
		close(done)
	}()

	waitPeriodicCall(t, runner.seen)
	timer := waitPeriodicTimer(t, clock.timers)
	clock.advance(timer, clock.Now().Add(2*time.Second))
	waitPeriodicCall(t, runner.seen)
	timer = waitPeriodicTimer(t, clock.timers)
	clock.advance(timer, clock.Now().Add(2*time.Second))
	waitPeriodicCall(t, runner.seen)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "periodic task did not stop after cancellation")
	}
	require.Equal(t, 3, runner.count(), "Run() calls")
}

func TestRunPeriodicTaskReportsErrorAndContinues(t *testing.T) {
	t.Parallel()
	clock := newManualClock(time.Now())
	runner := &periodicTestRunner{err: errors.New("database unavailable"), seen: make(chan struct{}, 2)}
	diagnostics := &recordingPeriodicDiagnostics{seen: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runPeriodicTask(ctx, PeriodicTask{Name: "scan", Interval: 2 * time.Second, Runner: runner}, diagnostics, clock, nil)
		close(done)
	}()
	waitPeriodicCall(t, runner.seen)
	select {
	case <-diagnostics.seen:
	case <-time.After(time.Second):
		require.FailNow(t, "periodic task error was not reported")
	}
	timer := waitPeriodicTimer(t, clock.timers)
	clock.advance(timer, clock.Now().Add(2*time.Second))
	waitPeriodicCall(t, runner.seen)
	cancel()
	<-done
}

func TestEngineOwnsPeriodicTaskLifecycleAndCopiesDefinitions(t *testing.T) {
	t.Parallel()
	clock := newManualClock(time.Now())
	original := &periodicTestRunner{seen: make(chan struct{}, 1)}
	replacement := &periodicTestRunner{seen: make(chan struct{}, 1)}
	tasks := []PeriodicTask{{Name: "scan", Interval: 2 * time.Second, Runner: original}}
	descriptor := testDescriptor(handlerFunc(func(context.Context, Execution) Outcome { return succeededOutcome() }))
	engine, err := New(Config{
		Store: &jobRunnerStoreFake{}, Descriptors: []Descriptor{descriptor}, NodeID: "node-a",
		Diagnostics: &jobDiagnosticsFake{}, Policy: Policy{PollInterval: time.Hour}, Clock: clock, PeriodicTasks: tasks,
	})
	require.NoError(t, err)
	tasks[0] = PeriodicTask{Name: "replacement", Interval: time.Hour, Runner: replacement}
	err = engine.Start(context.Background())
	require.NoError(t, err)
	waitPeriodicCall(t, original.seen)
	select {
	case <-replacement.seen:
		require.FailNow(t, "Engine retained the caller-owned periodic task slice")
	default:
	}
	timer := waitPeriodicTimer(t, clock.timers)
	require.Equal(t, 2*time.Second, timer.delay, "periodic timer delay")
	err = engine.Close()
	require.NoError(t, err)
	clock.advance(timer, clock.Now().Add(2*time.Second))
	select {
	case <-original.seen:
		require.FailNow(t, "periodic task ran after Engine shutdown")
	default:
	}
}

func waitPeriodicCall(t *testing.T, seen <-chan struct{}) {
	t.Helper()
	select {
	case <-seen:
	case <-time.After(time.Second):
		require.FailNow(t, "periodic task did not run")
	}
}

func waitPeriodicTimer(t *testing.T, timers <-chan *manualTimer) *manualTimer {
	t.Helper()
	select {
	case timer := <-timers:
		return timer
	case <-time.After(time.Second):
		require.FailNow(t, "periodic task did not create its timer")
		return nil
	}
}

type recordingPeriodicDiagnostics struct{ seen chan struct{} }

func (d *recordingPeriodicDiagnostics) ErrorContext(context.Context, string, error) {
	select {
	case d.seen <- struct{}{}:
	default:
	}
}
