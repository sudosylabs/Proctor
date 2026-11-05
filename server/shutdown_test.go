// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownDrainAndServeExitShareOneBudget(t *testing.T) {
	t.Parallel()

	const budget = 400 * time.Millisecond
	events := &lifecycleEvents{}
	// The drain spends most of the budget and Serve then never returns.
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), keepServing: true,
		afterShutdown: func() { time.Sleep(3 * budget / 4) },
	}
	t.Cleanup(func() { close(httpService.stopped) })
	platformRuntime := &lifecyclePlatform{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: platformRuntime, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	node.components.settings.shutdownTimeout = budget
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)

	stopStarted := time.Now()
	cancel()
	err := receiveLifecycleResult(t, done, "Run")
	elapsed := time.Since(stopStarted)
	require.ErrorIs(t, err, errHTTPServerStopTimeout, "Run() error")
	// A separate Serve-exit wait would have taken another full budget.
	require.Less(t, elapsed, budget+budget/2, "shutdown took %s for a %s budget", elapsed, budget)
	deadline := platformRuntime.closeDeadline.Load()
	require.NotNil(t, deadline, "Platform closed without the shutdown budget")
	// A fresh budget would end no earlier than the drain plus another budget.
	require.True(t, deadline.Before(stopStarted.Add(budget+budget/4)), "Platform received a fresh budget instead of the remainder")
}

type lifecycleTracing struct {
	events   *lifecycleEvents
	deadline atomic.Pointer[time.Time]
}

func (r *lifecycleTracing) Close(ctx context.Context) error {
	r.events.record("tracing-close")
	if deadline, ok := ctx.Deadline(); ok {
		r.deadline.Store(&deadline)
	}
	return nil
}

func TestShutdownFlushesTracingAfterEverySpanProducerAndBeforePlatform(t *testing.T) {
	t.Parallel()

	const budget = time.Minute
	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	tracer := &lifecycleTracing{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		servingLease: &lifecycleServingNodeLease{events: events, failures: make(chan error)},
		metrics:      &lifecycleMetrics{events: events, failures: make(chan error)},
		tracing:      tracer, readiness: &lifecycleReadiness{events: events},
		listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})
	node.components.settings.shutdownTimeout = budget
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	stopStarted := time.Now()
	cancel()
	require.NoError(t, receiveLifecycleResult(t, done, "Run"))
	// The order is the contract: span producers close first so their final
	// spans are flushed, and the exporter never outlives Platform's logging.
	assertLifecycleEvents(t, events,
		"platform-start", "metrics-start", "serving-lease-start", "jobs-start", "websocket-start",
		"http-serve", "ready", "unready", "http-shutdown", "jobs-close", "websocket-close",
		"transport-close", "serving-lease-close", "metrics-close", "tracing-close", "platform-close",
	)
	deadline := tracer.deadline.Load()
	require.NotNil(t, deadline, "tracing flushed without the shutdown budget")
	require.False(t, deadline.After(stopStarted.Add(budget+time.Second)), "tracing received a budget beyond the shared one")
}

func TestShutdownWaitsForARefusedServeBeforeClosingWorkers(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	accept := make(chan struct{})
	listener := &lifecycleListener{events: events}
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), beforeAccept: accept,
		onExit: func() { events.record("http-exit") },
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
		listen:    func(string, string) (net.Listener, error) { return listener, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	<-httpService.started
	cancel()
	deadline := time.Now().Add(time.Second)
	for listener.closed.Load() == 0 {
		require.False(t, time.Now().After(deadline), "shutdown did not close the Server-owned listener")
		time.Sleep(time.Millisecond)
	}
	// Serve is still inside the HTTP runtime; workers must outlive it.
	time.Sleep(20 * time.Millisecond)
	close(accept)
	require.NoError(t, receiveLifecycleResult(t, done, "Run"))
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "listener-close",
		"http-exit", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestShutdownRunsOnceForRunAndEveryClose(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	done := make(chan error, 1)
	go func() { done <- node.Run(context.Background()) }()
	waitForLifecycleReady(t, node)
	closed := make(chan error, 2)
	go func() { closed <- node.Close() }()
	go func() { closed <- node.Close() }()
	require.NoError(t, receiveLifecycleResult(t, done, "Run"))
	require.NoError(t, receiveLifecycleResult(t, closed, "first Close"))
	require.NoError(t, receiveLifecycleResult(t, closed, "second Close"))
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "ready", "unready",
		"http-shutdown", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestForceStopDuringGracefulDrainClosesAtOnceAndReportsTheForcedStop(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	draining := make(chan struct{})
	// Connections never drain on their own.
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), drainUntilDone: true,
		afterShutdown: func() { close(draining) },
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	node.components.settings.shutdownTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	<-draining
	node.ForceStop()
	err := receiveLifecycleResult(t, done, "Run after a forced stop")
	require.ErrorIs(t, err, ErrShutdownForced, "Run() error")
	require.ErrorIs(t, node.Close(), ErrShutdownForced, "Close() error")
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "ready", "unready",
		"http-shutdown", "http-force-close", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestForceStopOfARunningNodeStopsItAndReportsTheForcedStop(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	done := make(chan error, 1)
	go func() { done <- node.Run(context.Background()) }()
	waitForLifecycleReady(t, node)
	node.ForceStop()
	require.ErrorIs(t, receiveLifecycleResult(t, done, "Run after a forced stop"), ErrShutdownForced, "Run() error")
	require.False(t, node.Ready(), "node stayed ready after a forced stop")
}

func TestShutdownNotifiesTheHostOnceAfterWithdrawingReadinessAndBeforeHTTPStops(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	node.stoppingNotifier = func(ctx context.Context) error {
		_, bounded := ctx.Deadline()
		assert.True(t, bounded, "host shutdown notification has no deadline")
		events.record("host-stopping-notify")
		return errors.New("notification socket unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	require.NoError(t, receiveLifecycleResult(t, done, "Run"), "a failed host notification must not fail shutdown")
	require.NoError(t, node.Close())
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "ready", "unready",
		"host-stopping-notify", "http-shutdown", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestInertCloseDoesNotNotifyTheHostOfShutdown(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
	})
	node.stoppingNotifier = func(context.Context) error {
		events.record("host-stopping-notify")
		return nil
	}
	require.NoError(t, node.Close())
	assertLifecycleEvents(t, events, "transport-close", "platform-close")
}

// budgetObservation records the shutdown budget one draining step was handed.
type budgetObservation struct {
	deadline atomic.Pointer[time.Time]
	spent    atomic.Bool
}

func (o *budgetObservation) observe(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		o.deadline.Store(&deadline)
	}
	o.spent.Store(ctx.Err() != nil)
}

func TestShutdownSlowDrainingStepCannotExtendTheSharedBudget(t *testing.T) {
	t.Parallel()

	steps := []string{"jobs", "websocket", "metrics"}
	for slowIndex, slow := range steps {
		t.Run(slow, func(t *testing.T) {
			t.Parallel()

			const budget = 200 * time.Millisecond
			events := &lifecycleEvents{}
			observations := map[string]*budgetObservation{}
			hooks := map[string]func(context.Context){}
			for _, step := range steps {
				observation := &budgetObservation{}
				observations[step] = observation
				hooks[step] = func(ctx context.Context) {
					observation.observe(ctx)
					if step == slow {
						// The slow step never finishes on its own; only the
						// budget it is handed ends its wait.
						<-ctx.Done()
					}
				}
			}
			httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
			platformRuntime := &lifecyclePlatform{events: events}
			node := newLifecycleTestServer(t, runtimeComponents{
				platform:     platformRuntime,
				jobs:         &lifecycleJobs{events: events, onClose: hooks["jobs"]},
				websocket:    &lifecycleWebSocket{events: events, onClose: hooks["websocket"]},
				transport:    &lifecycleTransport{events: events},
				servingLease: &lifecycleServingNodeLease{events: events, failures: make(chan error)},
				metrics:      &lifecycleMetrics{events: events, failures: make(chan error), onClose: hooks["metrics"]},
				tracing:      &lifecycleTracing{events: events}, readiness: &lifecycleReadiness{events: events},
				listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
				newHTTP: func(httpServerSettings) httpRuntime { return httpService },
			})
			node.components.settings.shutdownTimeout = budget
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- node.Run(ctx) }()
			waitForLifecycleReady(t, node)

			stopStarted := time.Now()
			cancel()
			require.NoError(t, receiveLifecycleResult(t, done, "Run"))
			elapsed := time.Since(stopStarted)
			require.GreaterOrEqual(t, elapsed, budget, "the slow %s step did not wait for its budget", slow)
			// A fresh timeout per step would add at least another budget.
			require.Less(t, elapsed, budget+budget/2, "shutdown took %s for a %s budget", elapsed, budget)
			assertLifecycleEvents(t, events,
				"platform-start", "metrics-start", "serving-lease-start", "jobs-start", "websocket-start",
				"http-serve", "ready", "unready", "http-shutdown", "jobs-close", "websocket-close",
				"transport-close", "serving-lease-close", "metrics-close", "tracing-close", "platform-close",
			)
			for index, step := range steps {
				deadline := observations[step].deadline.Load()
				require.NotNil(t, deadline, "%s closed without the shutdown budget", step)
				require.True(t, deadline.Before(stopStarted.Add(budget+budget/4)), "%s received a budget beyond the shared one", step)
				// Every step after the slow one inherits a spent budget.
				require.Equal(t, index > slowIndex, observations[step].spent.Load(), "%s budget spent", step)
			}
			platformDeadline := platformRuntime.closeDeadline.Load()
			require.NotNil(t, platformDeadline, "Platform closed without the shutdown budget")
			require.True(t, platformDeadline.Before(stopStarted.Add(budget+budget/4)), "Platform received a fresh budget instead of the remainder")
		})
	}
}
