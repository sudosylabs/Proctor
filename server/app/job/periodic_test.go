// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type periodicTestRunner struct {
	mu    sync.Mutex
	calls int
	err   error
	seen  chan struct{}
}

func (runner *periodicTestRunner) Run(context.Context) error {
	runner.mu.Lock()
	runner.calls++
	runner.mu.Unlock()
	select {
	case runner.seen <- struct{}{}:
	default:
	}
	return runner.err
}

func (runner *periodicTestRunner) count() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls
}

func TestClonePeriodicTasksValidatesAndClones(t *testing.T) {
	t.Parallel()
	runner := &periodicTestRunner{}
	values := []PeriodicTask{{Name: "exam-attempt-participation-expiry", Interval: 2 * time.Second, Runner: runner}}
	cloned, err := clonePeriodicTasks(values)
	if err != nil {
		t.Fatalf("clonePeriodicTasks() error = %v", err)
	}
	values[0] = PeriodicTask{Name: "replacement", Interval: time.Hour, Runner: runner}
	if cloned[0].Name != "exam-attempt-participation-expiry" || cloned[0].Interval != 2*time.Second {
		t.Fatalf("clonePeriodicTasks() retained caller slice: %#v", cloned)
	}

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
			if _, err := clonePeriodicTasks(test.value); err == nil {
				t.Fatal("clonePeriodicTasks() accepted invalid definition")
			}
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
		t.Fatal("periodic task did not stop after cancellation")
	}
	if got := runner.count(); got != 3 {
		t.Fatalf("Run() calls = %d, want 3", got)
	}
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
		t.Fatal("periodic task error was not reported")
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
	if err != nil {
		t.Fatal(err)
	}
	tasks[0] = PeriodicTask{Name: "replacement", Interval: time.Hour, Runner: replacement}
	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPeriodicCall(t, original.seen)
	select {
	case <-replacement.seen:
		t.Fatal("Engine retained the caller-owned periodic task slice")
	default:
	}
	timer := waitPeriodicTimer(t, clock.timers)
	if timer.delay != 2*time.Second {
		t.Fatalf("periodic timer delay = %s, want 2s", timer.delay)
	}
	if err = engine.Close(); err != nil {
		t.Fatal(err)
	}
	clock.advance(timer, clock.Now().Add(2*time.Second))
	select {
	case <-original.seen:
		t.Fatal("periodic task ran after Engine shutdown")
	default:
	}
}

func waitPeriodicCall(t *testing.T, seen <-chan struct{}) {
	t.Helper()
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("periodic task did not run")
	}
}

func waitPeriodicTimer(t *testing.T, timers <-chan *manualTimer) *manualTimer {
	t.Helper()
	select {
	case timer := <-timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("periodic task did not create its timer")
		return nil
	}
}

type recordingPeriodicDiagnostics struct{ seen chan struct{} }

func (diagnostics *recordingPeriodicDiagnostics) ErrorContext(context.Context, string, error) {
	select {
	case diagnostics.seen <- struct{}{}:
	default:
	}
}
