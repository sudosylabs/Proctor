// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
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
	startErr error
	closeErr error
	events   *lifecycleEvents
}

func (p *lifecyclePlatform) Start(context.Context) error {
	p.events.record("platform-start")
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
	startErr error
	closeErr error
	events   *lifecycleEvents
}

type lifecycleJobs struct {
	startErr error
	closeErr error
	events   *lifecycleEvents
}

func (j *lifecycleJobs) Start(context.Context) error {
	j.events.record("jobs-start")
	return j.startErr
}

func (j *lifecycleJobs) Close() error {
	j.events.record("jobs-close")
	return j.closeErr
}

func (w *lifecycleWebSocket) Start(context.Context) error {
	w.events.record("websocket-start")
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
	immediateFailure bool
	once             sync.Once
}

func (s *lifecycleHTTP) Serve(_ net.Listener, entered func()) error {
	s.events.record("http-serve")
	close(s.started)
	if s.immediateFailure {
		return s.serveErr
	}
	entered()
	if s.failed != nil {
		<-s.failed
		return s.serveErr
	}
	<-s.stopped
	return http.ErrServerClosed
}

func (s *lifecycleHTTP) Shutdown(context.Context) error {
	s.events.record("http-shutdown")
	s.once.Do(func() { close(s.stopped) })
	return nil
}

func (s *lifecycleHTTP) Close() error {
	s.once.Do(func() { close(s.stopped) })
	return nil
}

type lifecycleListener struct{}

func (lifecycleListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (lifecycleListener) Close() error              { return nil }
func (lifecycleListener) Addr() net.Addr            { return lifecycleAddress("127.0.0.1:8065") }

type lifecycleAddress string

func (a lifecycleAddress) Network() string { return "tcp" }
func (a lifecycleAddress) String() string  { return string(a) }

func TestServerStartupFailureCleansUpConstructedRuntime(t *testing.T) {
	t.Parallel()

	startErr := errors.New("cluster unavailable")
	events := &lifecycleEvents{}
	readiness := &lifecycleReadiness{events: events}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{startErr: startErr, events: events},
		transport: &lifecycleTransport{events: events},
		readiness: readiness,
	})

	err := node.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want wrapped %v", err, startErr)
	}
	if readiness.Ready() {
		t.Fatal("Ready() = true after startup failure")
	}
	assertLifecycleEvents(t, events, "platform-start", "transport-close", "platform-close")

	if err := node.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	assertLifecycleEvents(t, events, "platform-start", "transport-close", "platform-close")
}

func TestServerJobStartupFailureClosesWorkersBeforeInfrastructure(t *testing.T) {
	t.Parallel()

	startErr := errors.New("job registry unavailable")
	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{startErr: startErr, events: events},
		transport: &lifecycleTransport{events: events},
		readiness: &lifecycleReadiness{},
	})

	err := node.Start(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want wrapped %v", err, startErr)
	}
	assertLifecycleEvents(t, events, "platform-start", "jobs-start", "jobs-close", "transport-close", "platform-close")
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

	err := node.Start(context.Background())
	if !errors.Is(err, listenErr) {
		t.Fatalf("Start() error = %v, want wrapped %v", err, listenErr)
	}
	if node.Ready() {
		t.Fatal("Ready() = true after listener failure")
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
			return lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	done := make(chan error, 1)
	go func() { done <- node.Start(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)

	if err := node.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() returned without stopping Start()")
	}
	if node.Ready() {
		t.Fatal("Ready() = true after shutdown")
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
	if err := node.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
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
			return lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	done := make(chan error, 1)
	go func() { done <- node.Start(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP serving did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, serveErr) {
			t.Fatalf("Start() error = %v, want wrapped %v", err, serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after HTTP serving failure")
	}
	if node.Ready() {
		t.Fatal("Ready() = true after HTTP serving failure")
	}
	assertLifecycleEvents(
		t,
		events,
		"platform-start",
		"jobs-start",
		"websocket-start",
		"http-serve",
		"http-shutdown",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func TestServerCloseBeforeStartDisposesInertRuntimeExactlyOnce(t *testing.T) {
	t.Parallel()

	events := &lifecycleEvents{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: events},
		jobs:      &lifecycleJobs{events: events},
		transport: &lifecycleTransport{events: events},
		websocket: &lifecycleWebSocket{events: events},
		readiness: &lifecycleReadiness{},
	})

	if err := node.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	if err := node.Start(context.Background()); err == nil || err.Error() != "server is closed" {
		t.Fatalf("Start() after Close() error = %v, want server is closed", err)
	}
	assertLifecycleEvents(t, events, "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func TestServerRejectsASecondStartWhileRunning(t *testing.T) {
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
			return lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	first := make(chan error, 1)
	go func() { first <- node.Start(context.Background()) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP serving did not start")
	}
	if err := node.Start(context.Background()); err == nil || err.Error() != "server has already been started" {
		t.Fatalf("second Start() error = %v, want already started", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-first; err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
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

	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start(canceled) error = %v, want nil", err)
	}
	if node.Ready() {
		t.Fatal("Ready() = true after canceled startup")
	}
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
			return lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Start(ctx) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() after running cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("running cancellation did not stop Server.Start")
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
			return lifecycleListener{}, nil
		},
		newHTTP: func(httpServerSettings) httpRuntime { return httpService },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- node.Start(ctx) }()
	select {
	case <-httpService.started:
	case <-time.After(time.Second):
		t.Fatal("HTTP serving did not start")
	}
	waitForLifecycleReady(t, node)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("Start() after running cancellation error = %v, want cleanup failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("running cancellation did not stop Server.Start")
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

	err := node.Start(context.Background())
	for _, expected := range []error{
		startErr,
		jobsCloseErr,
		webSocketCloseErr,
		transportCloseErr,
		platformCloseErr,
	} {
		if !errors.Is(err, expected) {
			t.Fatalf("Start() error = %v, want joined %v", err, expected)
		}
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

func TestServerConcurrentCloseBeforeStartIsIdempotent(t *testing.T) {
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
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	assertLifecycleEvents(t, events, "jobs-close", "websocket-close", "transport-close", "platform-close")
}

func newLifecycleTestServer(t *testing.T, components runtimeComponents) *Server {
	t.Helper()

	logger, err := mlog.New()
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	if _, ok := components.platform.(*lifecyclePlatform); !ok {
		t.Fatal("lifecycle test platform has unexpected type")
	}
	settings := config.Default().Server
	settings.ListenAddress = "127.0.0.1:0"
	settings.ShutdownTimeout.Duration = time.Second
	components.settings = runtimeSettingsFromConfig(settings)
	components.logger = logger
	if components.listen == nil {
		components.listen = func(string, string) (net.Listener, error) {
			return nil, errors.New("listener should not be created")
		}
	}
	if components.newHTTP == nil {
		components.newHTTP = func(httpServerSettings) httpRuntime {
			t.Fatal("HTTP server should not be created")
			return nil
		}
	}
	return &Server{components: components}
}

func assertLifecycleEvents(t *testing.T, events *lifecycleEvents, expected ...string) {
	t.Helper()
	actual := events.snapshot()

	if len(actual) != len(expected) {
		t.Fatalf("lifecycle events = %v, want %v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("lifecycle events = %v, want %v", actual, expected)
		}
	}
}

func waitForLifecycleReady(t *testing.T, node *Server) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for !node.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("Ready() did not become true after HTTP serving started")
		}
		time.Sleep(time.Millisecond)
	}
}
