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

type lifecyclePlatform struct {
	startErr error
	closeErr error
	events   *[]string
	config   config.Config
	logger   *mlog.Logger
}

func (p *lifecyclePlatform) Start(context.Context) error {
	*p.events = append(*p.events, "platform-start")
	return p.startErr
}

func (p *lifecyclePlatform) Close() error {
	*p.events = append(*p.events, "platform-close")
	return p.closeErr
}

func (p *lifecyclePlatform) Config() config.Config { return p.config }
func (p *lifecyclePlatform) Log() *mlog.Logger     { return p.logger }

type lifecycleTransport struct {
	events *[]string
}

func (t *lifecycleTransport) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (t *lifecycleTransport) Close() error {
	*t.events = append(*t.events, "transport-close")
	return nil
}

type lifecycleWebSocket struct {
	startErr error
	events   *[]string
}

type lifecycleJobs struct {
	startErr error
	events   *[]string
}

func (j *lifecycleJobs) Start(context.Context) error {
	*j.events = append(*j.events, "jobs-start")
	return j.startErr
}

func (j *lifecycleJobs) Close() error {
	*j.events = append(*j.events, "jobs-close")
	return nil
}

func (w *lifecycleWebSocket) Start(context.Context) error {
	*w.events = append(*w.events, "websocket-start")
	return w.startErr
}

func (w *lifecycleWebSocket) Close() error {
	*w.events = append(*w.events, "websocket-close")
	return nil
}

type lifecycleReadiness struct {
	ready atomic.Bool
}

func (r *lifecycleReadiness) Ready() bool {
	return r.ready.Load()
}

func (r *lifecycleReadiness) SetReady(ready bool) {
	r.ready.Store(ready)
}

type lifecycleHTTP struct {
	events   *[]string
	started  chan struct{}
	stopped  chan struct{}
	failed   chan struct{}
	serveErr error
	once     sync.Once
}

func (s *lifecycleHTTP) Serve(net.Listener) error {
	close(s.started)
	if s.failed != nil {
		<-s.failed
		return s.serveErr
	}
	<-s.stopped
	return http.ErrServerClosed
}

func (s *lifecycleHTTP) Shutdown(context.Context) error {
	*s.events = append(*s.events, "http-shutdown")
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
	events := []string{}
	readiness := &lifecycleReadiness{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{startErr: startErr, events: &events},
		transport: &lifecycleTransport{events: &events},
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
	events := []string{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: &events},
		jobs:      &lifecycleJobs{startErr: startErr, events: &events},
		transport: &lifecycleTransport{events: &events},
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
	events := []string{}
	readiness := &lifecycleReadiness{}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: &events},
		jobs:      &lifecycleJobs{events: &events},
		transport: &lifecycleTransport{events: &events},
		websocket: &lifecycleWebSocket{events: &events},
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

	events := []string{}
	readiness := &lifecycleReadiness{}
	httpService := &lifecycleHTTP{
		events:  &events,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: &events},
		jobs:      &lifecycleJobs{events: &events},
		transport: &lifecycleTransport{events: &events},
		websocket: &lifecycleWebSocket{events: &events},
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
	if !node.Ready() {
		t.Fatal("Ready() = false after runtime startup")
	}

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
	events := []string{}
	readiness := &lifecycleReadiness{}
	httpService := &lifecycleHTTP{
		events:   &events,
		started:  make(chan struct{}),
		stopped:  make(chan struct{}),
		failed:   make(chan struct{}),
		serveErr: serveErr,
	}
	node := newLifecycleTestServer(t, runtimeComponents{
		platform:  &lifecyclePlatform{events: &events},
		jobs:      &lifecycleJobs{events: &events},
		transport: &lifecycleTransport{events: &events},
		websocket: &lifecycleWebSocket{events: &events},
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
	close(httpService.failed)

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
		"http-shutdown",
		"jobs-close",
		"websocket-close",
		"transport-close",
		"platform-close",
	)
}

func newLifecycleTestServer(t *testing.T, components runtimeComponents) *Server {
	t.Helper()

	logger, err := mlog.New()
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })
	platform, ok := components.platform.(*lifecyclePlatform)
	if !ok {
		t.Fatal("lifecycle test platform has unexpected type")
	}
	platform.config = config.Default()
	platform.config.Server.ListenAddress = "127.0.0.1:0"
	platform.config.Server.ShutdownTimeout.Duration = time.Second
	platform.logger = logger
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
	option := func(settings *options) error {
		settings.runtimeFactory = func(context.Context, string) (runtimeComponents, error) {
			return components, nil
		}
		return nil
	}
	node, err := New(context.Background(), option)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return node
}

func assertLifecycleEvents(t *testing.T, actual []string, expected ...string) {
	t.Helper()

	if len(actual) != len(expected) {
		t.Fatalf("lifecycle events = %v, want %v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("lifecycle events = %v, want %v", actual, expected)
		}
	}
}
