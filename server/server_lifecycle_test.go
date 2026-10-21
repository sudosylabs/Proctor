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
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/logging"
)

type lifecycleEvents struct {
	mu     sync.Mutex
	values []string
}

func (e *lifecycleEvents) record(value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.values = append(e.values, value)
}

func (e *lifecycleEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.values...)
}

type lifecyclePlatform struct {
	startErr   error
	closeErr   error
	events     *lifecycleEvents
	afterStart func()
}

func (p *lifecyclePlatform) Start(context.Context) error {
	p.events.record("platform-start")
	if p.afterStart != nil {
		p.afterStart()
	}
	return p.startErr
}

func (p *lifecyclePlatform) Close() error {
	p.events.record("platform-close")
	return p.closeErr
}

type lifecycleTransport struct {
	events   *lifecycleEvents
	closeErr error
}

func (t *lifecycleTransport) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (t *lifecycleTransport) Close() error {
	t.events.record("transport-close")
	return t.closeErr
}

type lifecycleWebSocket struct {
	startErr   error
	closeErr   error
	events     *lifecycleEvents
	afterStart func()
}

type lifecycleJobs struct {
	startErr   error
	closeErr   error
	events     *lifecycleEvents
	afterStart func()
}

type lifecycleServingNodeLease struct {
	startErr error
	closeErr error
	events   *lifecycleEvents
	failures chan error
}

type lifecycleMetrics struct {
	startErr       error
	closeErr       error
	events         *lifecycleEvents
	failures       chan error
	failureSources []<-chan error
	failureCalls   atomic.Int64
	ready          atomic.Bool
}

func (m *lifecycleMetrics) Start(context.Context) error {
	m.events.record("metrics-start")
	return m.startErr
}

func (m *lifecycleMetrics) Failures() <-chan error {
	if len(m.failureSources) == 0 {
		return m.failures
	}
	index := int(m.failureCalls.Add(1) - 1)
	if index >= len(m.failureSources) {
		index = len(m.failureSources) - 1
	}
	return m.failureSources[index]
}
func (m *lifecycleMetrics) SetReady(ready bool) { m.ready.Store(ready) }
func (m *lifecycleMetrics) Close() error {
	m.events.record("metrics-close")
	return m.closeErr
}

func (l *lifecycleServingNodeLease) Start(context.Context) error {
	l.events.record("serving-lease-start")
	return l.startErr
}

func (l *lifecycleServingNodeLease) Failures() <-chan error { return l.failures }

func (l *lifecycleServingNodeLease) Close() error {
	l.events.record("serving-lease-close")
	return l.closeErr
}

type lifecycleReconciler struct {
	err         error
	recoveryErr error
	events      *lifecycleEvents
}

func (r *lifecycleReconciler) ReconcileAdministratorRecovery(context.Context) error {
	r.events.record("administrator-recovery-reconcile")
	return r.recoveryErr
}

func (r *lifecycleReconciler) ReconcileSystemAdministratorRole(context.Context) error {
	r.events.record("role-reconcile")
	return r.err
}

func (j *lifecycleJobs) Start(context.Context) error {
	j.events.record("jobs-start")
	if j.afterStart != nil {
		j.afterStart()
	}
	return j.startErr
}

func (j *lifecycleJobs) Close() error {
	j.events.record("jobs-close")
	return j.closeErr
}

func (w *lifecycleWebSocket) Start(context.Context) error {
	w.events.record("websocket-start")
	if w.afterStart != nil {
		w.afterStart()
	}
	return w.startErr
}

func (w *lifecycleWebSocket) Close() error {
	w.events.record("websocket-close")
	return w.closeErr
}

type lifecycleReadiness struct {
	ready  atomic.Bool
	events *lifecycleEvents
}

func (r *lifecycleReadiness) Ready() bool {
	return r.ready.Load()
}

func (r *lifecycleReadiness) SetReady(ready bool) {
	previous := r.ready.Swap(ready)
	if previous == ready {
		return
	}
	if r.events != nil {
		if ready {
			r.events.record("ready")
		} else {
			r.events.record("unready")
		}
	}
}

type lifecycleHTTP struct {
	events           *lifecycleEvents
	started          chan struct{}
	stopped          chan struct{}
	failed           chan struct{}
	serveErr         error
	shutdownErr      error
	closeErr         error
	immediateFailure bool
	beforeAccept     <-chan struct{}
	afterShutdown    func()
	keepServing      bool
	drainUntilDone   bool
	stoppedErr       error
	once             sync.Once
}

func (s *lifecycleHTTP) Serve(_ net.Listener, accept func() bool) error {
	s.events.record("http-serve")
	close(s.started)
	if s.immediateFailure {
		return s.serveErr
	}
	if s.beforeAccept != nil {
		<-s.beforeAccept
	}
	if !accept() {
		return net.ErrClosed
	}
	if s.failed != nil {
		<-s.failed
		return s.serveErr
	}
	<-s.stopped
	if s.stoppedErr != nil {
		return s.stoppedErr
	}
	return http.ErrServerClosed
}

func (s *lifecycleHTTP) Shutdown(ctx context.Context) error {
	s.events.record("http-shutdown")
	if s.afterShutdown != nil {
		s.afterShutdown()
	}
	if s.drainUntilDone {
		<-ctx.Done()
		return ctx.Err()
	}
	if !s.keepServing {
		s.once.Do(func() { close(s.stopped) })
	}
	return s.shutdownErr
}

func (s *lifecycleHTTP) Close() error {
	s.events.record("http-force-close")
	s.once.Do(func() { close(s.stopped) })
	return s.closeErr
}

type lifecycleListener struct {
	events   *lifecycleEvents
	closeErr error
	closed   atomic.Int64
	accepted atomic.Int64
}

func (l *lifecycleListener) Accept() (net.Conn, error) {
	l.accepted.Add(1)
	return nil, net.ErrClosed
}
func (l *lifecycleListener) Close() error {
	l.closed.Add(1)
	if l.events != nil {
		l.events.record("listener-close")
	}
	return l.closeErr
}
func (*lifecycleListener) Addr() net.Addr { return lifecycleAddress("127.0.0.1:8065") }

type lifecycleAddress string

func (a lifecycleAddress) Network() string { return "tcp" }
func (a lifecycleAddress) String() string  { return string(a) }

func TestOwnershipListenerTransfersAtFirstAccept(t *testing.T) {
	t.Parallel()

	for _, accepted := range []bool{false, true} {
		accepted := accepted
		t.Run(fmt.Sprintf("accepted=%t", accepted), func(t *testing.T) {
			t.Parallel()
			underlying := &lifecycleListener{}
			var handoffs atomic.Int64
			listener := &ownershipListener{Listener: underlying, accept: func() bool {
				handoffs.Add(1)
				return accepted
			}}
			for range 2 {
				_, _ = listener.Accept()
			}
			require.Equal(t, int64(1), handoffs.Load(), "handoff count")
			wantAccepts := int64(0)
			if accepted {
				wantAccepts = 2
			}
			require.Equal(t, wantAccepts, underlying.accepted.Load(), "underlying Accept count")
			require.NoError(t, listener.Close())
			wantCloses := int64(0)
			if accepted {
				wantCloses = 1
			}
			require.Equal(t, wantCloses, underlying.closed.Load(), "underlying Close count")
		})
	}
}

func TestOwnershipListenerCloseWaitsForAtomicHandoff(t *testing.T) {
	t.Parallel()

	underlying := &lifecycleListener{}
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	listener := &ownershipListener{Listener: underlying, accept: func() bool {
		close(callbackEntered)
		<-releaseCallback
		return true
	}}
	acceptDone := make(chan struct{})
	go func() {
		_, _ = listener.Accept()
		close(acceptDone)
	}()
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		require.FailNow(t, "handoff callback was not entered")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- listener.Close() }()
	select {
	case err := <-closeDone:
		require.FailNow(t, fmt.Sprintf("Close() returned before ownership committed: %v", err))
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseCallback)
	require.NoError(t, receiveLifecycleResult(t, closeDone, "listener Close"))
	select {
	case <-acceptDone:
	case <-time.After(time.Second):
		require.FailNow(t, "Accept() did not return")
	}
	require.Equal(t, int64(1), underlying.closed.Load(), "underlying Close count")
}

func TestServerStartupFailureCleansUpConstructedRuntime(t *testing.T) {
	t.Parallel()

	startErr := errors.New("cluster unavailable")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{startErr: startErr, events: events},
		jobs:      &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events},
		readiness: readiness,
	})
	var notifications atomic.Int64
	node.readyNotifier = func(context.Context) error {
		notifications.Add(1)
		return nil
	}

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	require.False(t, readiness.Ready(), "Ready() = true after startup failure")
	require.Equal(t, int64(0), notifications.Load(), "notifications")
	assertLifecycleEvents(t, events, "platform-start", "jobs-close", "websocket-close", "transport-close", "platform-close")

	require.NoError(t, node.Close())
	assertLifecycleEvents(t, events, "platform-start", "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func TestServerJobStartupFailureClosesWorkersBeforeInfrastructure(t *testing.T) {
	t.Parallel()

	startErr := errors.New("job registry unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{startErr: startErr, events: events},
		websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	assertLifecycleEvents(t, events, "platform-start", "jobs-start", "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func TestServerMetricsStartupFailureIsFatalAndClosesBeforeInfrastructure(t *testing.T) {
	t.Parallel()

	startErr := errors.New("metrics listener unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		metrics:   &lifecycleMetrics{startErr: startErr, events: events, failures: make(chan error)},
		jobs:      &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	assertLifecycleEvents(t, events,
		"platform-start", "metrics-start", "jobs-close", "websocket-close",
		"transport-close", "metrics-close", "platform-close",
	)
}

func TestServerRechecksQueuedMetricsFailureBeforePublishingReady(t *testing.T) {
	t.Parallel()

	metricsErr := errors.New("metrics listener stopped during HTTP handoff")
	queuedFailure := make(chan error, 1)
	queuedFailure <- metricsErr
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	metrics := &lifecycleMetrics{
		events: events,
		// The first source covers the post-Start probe, the nil source disables
		// the handoff select case, and the third models a failure already queued
		// when the immediate pre-readiness probe runs.
		failureSources: []<-chan error{make(chan error), nil, queuedFailure},
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, metrics: metrics,
		jobs: &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: readiness,
		listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{events: events}, nil },
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, metricsErr, "Run() error")
	require.False(t, readiness.Ready(), "node published readiness after a queued metrics failure")
	require.False(t, metrics.ready.Load(), "node published readiness after a queued metrics failure")
}

func TestServerServingLeaseMustCommitBeforeWorkersOrListener(t *testing.T) {
	t.Parallel()

	startErr := errors.New("serving lease unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events},
		servingLease: &lifecycleServingNodeLease{
			startErr: startErr, events: events, failures: make(chan error, 1),
		},
		jobs: &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	assertLifecycleEvents(t, events,
		"platform-start", "serving-lease-start", "jobs-close", "websocket-close",
		"transport-close", "serving-lease-close", "platform-close",
	)
}

func TestServerServingLeaseRenewalFailureDrainsBeforeLeaseWithdrawal(t *testing.T) {
	t.Parallel()

	renewalErr := errors.New("serving lease renewal failed")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{events: events, started: make(chan struct{}), stopped: make(chan struct{})}
	lease := &lifecycleServingNodeLease{events: events, failures: make(chan error, 1)}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, servingLease: lease,
		jobs: &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: readiness,
		listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})
	done := make(chan error, 1)
	go func() { done <- node.Run(context.Background()) }()
	waitForLifecycleReady(t, node)
	lease.failures <- renewalErr
	err := receiveLifecycleResult(t, done, "Run after serving lease failure")
	require.ErrorIs(t, err, renewalErr, "Run() error")
	require.False(t, node.Ready(), "server remained ready after serving lease failure")
	assertLifecycleEvents(t, events,
		"platform-start", "serving-lease-start", "jobs-start", "websocket-start",
		"http-serve", "ready", "unready", "http-force-close", "jobs-close",
		"websocket-close", "transport-close", "serving-lease-close", "platform-close",
	)
}

func TestServerServingLeaseFailureDuringGracefulDrainForceClosesHTTP(t *testing.T) {
	t.Parallel()

	renewalErr := errors.New("serving lease renewal failed")
	events := &lifecycleEvents{}
	lease := &lifecycleServingNodeLease{events: events, failures: make(chan error, 1)}
	// Connections never drain on their own; the lease fails mid-drain.
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), drainUntilDone: true,
		afterShutdown: func() { lease.failures <- renewalErr },
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, servingLease: lease,
		jobs: &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: &lifecycleReadiness{events: events},
		listen:  func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})
	node.components.settings.shutdownTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	err := receiveLifecycleResult(t, done, "Run after serving lease failure during drain")
	require.ErrorIs(t, err, renewalErr, "Run() error")
	assertLifecycleEvents(t, events,
		"platform-start", "serving-lease-start", "jobs-start", "websocket-start",
		"http-serve", "ready", "unready", "http-shutdown", "http-force-close", "jobs-close",
		"websocket-close", "transport-close", "serving-lease-close", "platform-close",
	)
}

func TestServerReconcilesProtectedRoleBeforeStartingWorkers(t *testing.T) {
	t.Parallel()

	startErr := errors.New("role reconciliation unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:   &lifecyclePlatform{events: events},
		reconciler: &lifecycleReconciler{err: startErr, events: events},
		jobs:       &lifecycleJobs{events: events},
		websocket:  &lifecycleWebSocket{events: events},
		transport:  &lifecycleTransport{events: events},
		readiness:  &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	assertLifecycleEvents(t, events,
		"platform-start", "role-reconcile", "jobs-close", "websocket-close",
		"transport-close", "platform-close",
	)
}

func TestServerReconcilesOfflineAdministratorRecoveryBeforeStartingWorkers(t *testing.T) {
	t.Parallel()

	startErr := errors.New("administrator recovery reconciliation unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:   &lifecyclePlatform{events: events},
		reconciler: &lifecycleReconciler{recoveryErr: startErr, events: events},
		jobs:       &lifecycleJobs{events: events}, websocket: &lifecycleWebSocket{events: events},
		transport: &lifecycleTransport{events: events}, readiness: &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, startErr, "Run() error")
	assertLifecycleEvents(t, events,
		"platform-start", "role-reconcile", "administrator-recovery-reconcile",
		"jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestServerListenerFailureUnwindsStartedRuntime(t *testing.T) {
	t.Parallel()

	listenErr := errors.New("address unavailable")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: readiness,
		listen: func(string, string) (net.Listener, error) {
			return nil, listenErr
		},
	})

	err := node.Run(context.Background())
	require.ErrorIs(t, err, listenErr, "Run() error")
	require.False(t, node.Ready(), "Ready() = true after listener failure")
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerCloseDrainsHTTPBeforeClosingRuntime(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{
		events:  events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: readiness,
		listen: func(string, string) (net.Listener, error) {
			events.record("listener-bind")
			return &lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	done := make(chan error, 1)
	go func() { done <- node.Run(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)

	require.NoError(t, node.Close())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "Close() returned without stopping Run()")
	}
	require.False(t, node.Ready(), "Ready() = true after shutdown")
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"listener-bind",
		"http-serve",
		"ready",
		"unready",
		"http-shutdown",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
	require.NoError(t, node.Close())
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"listener-bind",
		"http-serve",
		"ready",
		"unready",
		"http-shutdown",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerServeFailureDrainsHTTPBeforeClosingRuntime(t *testing.T) {
	t.Parallel()

	serveErr := errors.New("accept failure")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{
		events:           events,
		started:          make(chan struct{}),
		stopped:          make(chan struct{}),
		serveErr:         serveErr,
		immediateFailure: true,
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: readiness,
		listen: func(string, string) (net.Listener, error) {
			return &lifecycleListener{events: events}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	done := make(chan error, 1)
	go func() { done <- node.Run(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP serving did not start")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, serveErr, "Run() error")
	case <-time.After(time.Second):
		require.FailNow(t, "server did not stop after HTTP serving failure")
	}
	require.False(t, node.Ready(), "Ready() = true after HTTP serving failure")
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"http-serve",
		"listener-close",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerCloseBeforeRunDisposesInertRuntimeExactlyOnce(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: &lifecycleReadiness{},
	})

	require.NoError(t, node.Close())
	require.NoError(t, node.Close())
	err := node.Run(context.Background())
	require.Error(t, err, "Run() after Close() error")
	require.Equal(t, "server is closed", err.Error(), "Run() after Close() error")
	assertLifecycleEvents(t, events, "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func TestServerRejectsASecondRunWhileRunning(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{
		events:  events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: &lifecycleReadiness{},
		listen: func(string, string) (net.Listener, error) {
			return &lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	first := make(chan error, 1)
	go func() { first <- node.Run(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP serving did not start")
	}
	err := node.Run(context.Background())
	require.Error(t, err, "second Run() error")
	require.Equal(t, "server has already been run", err.Error(), "second Run() error")
	require.NoError(t, node.Close())
	require.NoError(t, (<-first))
}

func TestServerCanceledStartupIsGracefulAndClosesRuntime(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{
			startErr: context.Canceled,
			events:   events,
		},
		transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
	})

	require.NoError(t, node.Run(ctx), "Run(canceled) error")
	require.False(t, node.Ready(), "Ready() = true after canceled startup")
	assertLifecycleEvents(t, events, "platform-start", "transport-close", "platform-close")
}

func TestServerRunningContextCancellationIsGraceful(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{
		events:  events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: readiness,
		listen: func(string, string) (net.Listener, error) {
			return &lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "running cancellation did not stop Server.Run")
	}
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"http-serve",
		"ready",
		"unready",
		"http-shutdown",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerNotifiesHostExactlyOnceAfterPublishingReadiness(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	var notifications atomic.Int64
	notificationErr := errors.New("notification socket unavailable")
	node.readyNotifier = func(context.Context) error {
		assert.True(t, node.Ready(), "host was notified before the node became ready")
		notifications.Add(1)
		events.record("host-ready-notify")
		return notificationErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	require.NoError(t, receiveLifecycleResult(t, done, "server readiness notification"))
	require.Equal(t, int64(1), notifications.Load(), "notifications")
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "ready", "host-ready-notify",
		"unready", "http-shutdown", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestServerLogsSuccessfulStartupMilestones(t *testing.T) {
	t.Parallel()

	logger, err := logging.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Shutdown() })
	var output logging.Buffer
	require.NoError(t, logger.Configure(logging.Config{
		MaxFieldBytes: 1024,
		Targets: []logging.Target{{
			Name: "test", Type: "console", Level: "info", Format: "json", Writer: &output,
		}},
	}))

	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{
		events:  events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:     &lifecyclePlatform{events: events},
		servingLease: &lifecycleServingNodeLease{events: events, failures: make(chan error)},
		reconciler:   &lifecycleReconciler{events: events},
		jobs:         &lifecycleJobs{events: events},
		websocket:    &lifecycleWebSocket{events: events},
		transport:    &lifecycleTransport{events: events},
		readiness:    readiness,
		logger:       logger,
		listen: func(string, string) (net.Listener, error) {
			return &lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	require.NoError(t, receiveLifecycleResult(t, done, "logged server startup"))
	for _, message := range []string{
		"serving node lease started",
		"startup reconciliation complete",
		"durable jobs started",
		"WebSocket service started",
		"server started",
	} {
		require.Contains(t, output.String(), message, "runtime startup log is missing")
	}
}

func TestServerRunningContextCancellationReturnsCleanupFailure(t *testing.T) {
	t.Parallel()

	cleanupErr := errors.New("transport cleanup failed")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	httpService := &lifecycleHTTP{
		events:  events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events},
		jobs:     &lifecycleJobs{events: events},
		transport: &lifecycleTransport{
			events:   events,
			closeErr: cleanupErr,
		},
		websocket: &lifecycleWebSocket{events: events},
		readiness: readiness,
		listen: func(string, string) (net.Listener, error) {
			return &lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, cleanupErr, "Run() after running cancellation error")
	case <-time.After(time.Second):
		require.FailNow(t, "running cancellation did not stop Server.Run")
	}
}

func TestServerCancellationBeforeHTTPHandoffClosesOwnedListener(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	accept := make(chan struct{})
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), beforeAccept: accept,
	}
	listener := &lifecycleListener{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen: func(string, string) (net.Listener, error) {
			events.record("listener-bind")
			return listener, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP runtime did not reach handoff")
	}
	cancel()
	close(accept)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "cancellation before HTTP handoff blocked")
	}
	require.Equal(t, int64(1), listener.closed.Load(), "listener close count")
	require.False(t, node.Ready(), "node became ready before HTTP handoff")
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "listener-bind", "http-serve",
		"listener-close", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestServerCancellationPreservesHTTPDrainAndForcedCloseFailures(t *testing.T) {
	t.Parallel()

	shutdownErr := errors.New("HTTP drain failed")
	forceErr := errors.New("HTTP forced close failed")
	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}),
		shutdownErr: shutdownErr, closeErr: forceErr,
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{events: events},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()
	waitForLifecycleReady(t, node)
	cancel()
	err := <-done
	for _, expected := range []error{shutdownErr, forceErr} {
		require.ErrorIs(t, err, expected, "Run() error")
	}
	closeErr := node.Close()
	for _, expected := range []error{shutdownErr, forceErr} {
		require.ErrorIs(t, closeErr, expected, "Close() error")
	}
	assertLifecycleEvents(t, events,
		"platform-start", "jobs-start", "websocket-start", "http-serve", "ready", "unready",
		"http-shutdown", "http-force-close", "jobs-close", "websocket-close", "transport-close", "platform-close",
	)
}

func TestServerRetainsPostShutdownServeFailureForCloseCallers(t *testing.T) {
	t.Parallel()

	serveErr := errors.New("serve failed during drain")
	events := &lifecycleEvents{}
	failed := make(chan struct{})
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}),
		failed: failed, serveErr: serveErr, afterShutdown: func() { close(failed) },
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	assertRetainedDrainFailure(t, node, serveErr)
}

func TestServerRetainsHTTPDrainTimeoutForCloseCallers(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), keepServing: true,
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
		listen:    func(string, string) (net.Listener, error) { return &lifecycleListener{}, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	node.components.settings.shutdownTimeout = 10 * time.Millisecond
	assertRetainedDrainFailure(t, node, errHTTPServerStopTimeout)
	close(httpService.stopped)
}

func assertRetainedDrainFailure(t *testing.T, node *Server, want error) {
	t.Helper()

	startDone := make(chan error, 1)
	go func() { startDone <- node.Run(context.Background()) }()
	waitForLifecycleReady(t, node)
	closeDone := make(chan error, 1)
	go func() { closeDone <- node.Close() }()
	startErr := receiveLifecycleResult(t, startDone, "Run")
	closeErr := receiveLifecycleResult(t, closeDone, "Close")
	for name, err := range map[string]error{"Run": startErr, "Close": closeErr, "repeated Close": node.Close()} {
		require.ErrorIs(t, err, want, "%s() error", name)
	}
}

func TestServerRetainsPreHandoffListenerCloseFailureForCloseCallers(t *testing.T) {
	t.Parallel()

	listenerErr := errors.New("listener close failed")
	events := &lifecycleEvents{}
	accept := make(chan struct{})
	httpService := &lifecycleHTTP{
		events: events, started: make(chan struct{}), stopped: make(chan struct{}), beforeAccept: accept,
	}
	listener := &lifecycleListener{events: events, closeErr: listenerErr}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{events: events}, jobs: &lifecycleJobs{events: events},
		websocket: &lifecycleWebSocket{events: events}, transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
		listen:    func(string, string) (net.Listener, error) { return listener, nil },
		newHTTP:   func(httpServerSettings) httpRuntime { return httpService },
	})
	startDone := make(chan error, 1)
	go func() { startDone <- node.Run(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		require.FailNow(t, "HTTP runtime did not reach handoff")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- node.Close() }()
	deadline := time.Now().Add(time.Second)
	for listener.closed.Load() == 0 {
		require.False(t, time.Now().After(deadline), "Close did not release the Server-owned listener")
		time.Sleep(time.Millisecond)
	}
	close(accept)
	startErr := receiveLifecycleResult(t, startDone, "Run")
	closeErr := receiveLifecycleResult(t, closeDone, "Close")
	for name, err := range map[string]error{"Run": startErr, "Close": closeErr, "repeated Close": node.Close()} {
		require.ErrorIs(t, err, listenerErr, "%s() error", name)
	}
}

func receiveLifecycleResult(t *testing.T, results <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(time.Second):
		require.FailNow(t, fmt.Sprintf("%s did not return", operation))
		return nil
	}
}

func TestServerDependencyFailureWinsCancellationRace(t *testing.T) {
	t.Parallel()

	dependencyErr := errors.New("dependency failed")
	events := &lifecycleEvents{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{startErr: errors.Join(context.Canceled, dependencyErr), events: events},
		transport: &lifecycleTransport{events: events}, readiness: &lifecycleReadiness{},
	})
	require.ErrorIs(t, node.Run(ctx), dependencyErr, "Run() error")
}

func TestServerCancellationBetweenStartupPhasesPreventsLaterWork(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{"platform", "jobs", "websocket", "listener"} {
		phase := phase
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			events := &lifecycleEvents{}
			ctx, cancel := context.WithCancel(context.Background())
			platformRuntime := &lifecyclePlatform{events: events}
			jobsRuntime := &lifecycleJobs{events: events}
			websocketRuntime := &lifecycleWebSocket{events: events}
			if phase == "platform" {
				platformRuntime.afterStart = cancel
			}
			if phase == "jobs" {
				jobsRuntime.afterStart = cancel
			}
			if phase == "websocket" {
				websocketRuntime.afterStart = cancel
			}
			node := newLifecycleTestServer(t, runtimeComponents{
				platform: platformRuntime, jobs: jobsRuntime, websocket: websocketRuntime,
				transport: &lifecycleTransport{events: events}, readiness: &lifecycleReadiness{},
				listen: func(string, string) (net.Listener, error) {
					events.record("listener-bind")
					if phase == "listener" {
						cancel()
					}
					return &lifecycleListener{events: events}, nil
				},
			})
			require.NoError(t, node.Run(ctx))
			want := []string{"platform-start"}
			if phase != "platform" {
				want = append(want, "jobs-start")
			}
			if phase == "websocket" || phase == "listener" {
				want = append(want, "websocket-start")
			}
			if phase == "listener" {
				want = append(want, "listener-bind", "listener-close")
			}
			want = append(want, "jobs-close", "websocket-close", "transport-close", "platform-close")
			assertLifecycleEvents(t, events, want...)
		})
	}
}

func TestServerPreservesPrimaryAndCleanupFailures(t *testing.T) {
	t.Parallel()

	startErr := errors.New("WebSocket start failed")
	jobsCloseErr := errors.New("jobs close failed")
	webSocketCloseErr := errors.New("websocket close failed")
	transportCloseErr := errors.New("transport close failed")
	platformCloseErr := errors.New("platform close failed")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform: &lifecyclePlatform{
			closeErr: platformCloseErr,
			events:   events,
		},
		jobs: &lifecycleJobs{
			closeErr: jobsCloseErr,
			events:   events,
		},
		websocket: &lifecycleWebSocket{
			startErr: startErr,
			closeErr: webSocketCloseErr,
			events:   events,
		},
		transport: &lifecycleTransport{
			closeErr: transportCloseErr,
			events:   events,
		},
		readiness: &lifecycleReadiness{},
	})

	err := node.Run(context.Background())
	for _, expected := range []error{
		startErr,
		jobsCloseErr,
		webSocketCloseErr,
		transportCloseErr,
		platformCloseErr,
	} {
		require.ErrorIs(t, err, expected, "Run() error")
	}
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerConcurrentCloseBeforeRunIsIdempotent(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: &lifecycleReadiness{},
	})

	const callers = 8
	errorsByCaller := make(chan error, callers)
	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			errorsByCaller <- node.Close()
		}()
	}
	callersDone.Wait()
	close(errorsByCaller)
	for err := range errorsByCaller {
		require.NoError(t, err)
	}
	assertLifecycleEvents(t, events, "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func newLifecycleTestServer(t *testing.T, components runtimeComponents) *Server {
	t.Helper()

	if components.logger == nil {
		logger, err := logging.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = logger.Shutdown() })
		components.logger = logger
	}
	_, ok := components.platform.(*lifecyclePlatform)
	require.True(t, ok, "lifecycle test platform has unexpected type")
	settings := config.Default().Server
	settings.ListenAddress = "127.0.0.1:0"
	settings.ShutdownTimeout.Duration = time.Second
	components.settings = runtimeSettingsFromConfig(settings)
	if components.listen == nil {
		components.listen = func(string, string) (net.Listener, error) {
			return nil, errors.New("listener should not be created")
		}
	}
	if components.newHTTP == nil {
		components.newHTTP = func(httpServerSettings) httpRuntime {
			require.FailNow(t, "HTTP server should not be created")
			return nil
		}
	}
	return &Server{components: components}
}

func assertLifecycleEvents(t *testing.T, events *lifecycleEvents, expected ...string) {
	t.Helper()
	actual := events.snapshot()

	require.Len(t, actual, len(expected), "lifecycle events")
	for index := range expected {
		require.Equal(t, expected[index], actual[index], "lifecycle events = %v, want %v", actual, expected)
	}
}

func waitForLifecycleReady(t *testing.T, node *Server) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for !node.Ready() {
		require.False(t, time.Now().After(deadline), "Ready() did not become true after HTTP serving started")
		time.Sleep(time.Millisecond)
	}
}
