// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package server owns Proctor runtime composition and lifecycle. Business
// policy remains in the application layer and is not implemented here.
//
// As the composition root, this package may depend on the components it wires;
// those components must not depend back on the module-root package.
//
// New selects and assembles infrastructure while this package owns startup,
// readiness, graceful HTTP shutdown, and cleanup. WebSocket construction is
// inert; Start owns replay reaping and Close drains the hub.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
)

type options struct {
	configPath     string
	runtimeFactory func(context.Context, string) (runtimeComponents, error)
}

// Option configures construction without exposing infrastructure or transport
// implementation types.
type Option func(*options) error

// WithConfigPath selects the deployment configuration file used to construct
// the server. Applying the option fails when path is empty; loading and
// validating the file occurs in New.
func WithConfigPath(path string) Option {
	return func(settings *options) error {
		if path == "" {
			return errors.New("configuration path is empty")
		}
		settings.configPath = path
		return nil
	}
}

type runtimePlatform interface {
	Start(context.Context) error
	Close() error
	Config() config.Config
	Log() *mlog.Logger
}

type runtimeTransport interface {
	http.Handler
	Close() error
}

// runtimeWebSocket is the sibling WebSocket transport lifecycle. Construction
// is inert; Start owns background reaping and Close drains connections.
type runtimeWebSocket interface {
	Start(context.Context) error
	Close() error
}

// runtimeJobs owns finite durable work execution. It starts only after the
// authoritative store is ready and drains before transports and infrastructure
// close, so handlers never outlive their VFS or persistence dependencies.
type runtimeJobs interface {
	Start(context.Context) error
	Close() error
}

type runtimeReadiness interface {
	Ready() bool
	SetReady(bool)
}

type httpRuntime interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type httpServerSettings struct {
	handler           http.Handler
	errorLog          *log.Logger
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	maxHeaderBytes    int
}

type runtimeComponents struct {
	platform  runtimePlatform
	jobs      runtimeJobs
	transport runtimeTransport
	websocket runtimeWebSocket
	readiness runtimeReadiness
	listen    func(string, string) (net.Listener, error)
	newHTTP   func(httpServerSettings) httpRuntime
}

// Server is the lifecycle owner for one assembled Proctor node.
type Server struct {
	components runtimeComponents

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	runCancel   context.CancelFunc
	runDone     chan struct{}

	httpMu sync.Mutex
	http   httpRuntime

	closeOnce sync.Once
	closeErr  error
}

// New constructs one Proctor server. It validates options and returns
// configuration or infrastructure construction failures without a partially
// usable Server.
//
// New does not start network listeners or WebSocket background work. Call
// Start to begin serving and Close to drain transports and infrastructure.
func New(ctx context.Context, optionValues ...Option) (*Server, error) {
	settings := options{runtimeFactory: constructRuntime}
	for _, option := range optionValues {
		if option == nil {
			return nil, errors.New("server option is nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("apply server option: %w", err)
		}
	}

	components, err := settings.runtimeFactory(ctx, settings.configPath)
	if err != nil {
		return nil, fmt.Errorf("construct server: %w", err)
	}
	return &Server{components: components}, nil
}

func newHTTPServer(settings httpServerSettings) httpRuntime {
	return &http.Server{
		Handler:           settings.handler,
		ErrorLog:          settings.errorLog,
		ReadHeaderTimeout: settings.readHeaderTimeout,
		ReadTimeout:       settings.readTimeout,
		WriteTimeout:      settings.writeTimeout,
		IdleTimeout:       settings.idleTimeout,
		MaxHeaderBytes:    settings.maxHeaderBytes,
	}
}

// Start starts shared infrastructure and HTTP serving in dependency order,
// marks the node ready only after both have started, and runs until cancellation
// or serving failure. It returns an error when the server is closed, was already
// started, cannot start a dependency or listener, or stops unexpectedly. Every
// exit path closes the assembled runtime.
func (s *Server) Start(ctx context.Context) (resultErr error) {
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		return errors.New("server is closed")
	}
	if s.started {
		s.lifecycleMu.Unlock()
		return errors.New("server has already been started")
	}
	runCtx, runCancel := context.WithCancel(ctx)
	s.started = true
	s.runCancel = runCancel
	s.runDone = make(chan struct{})
	runDone := s.runDone
	s.lifecycleMu.Unlock()

	defer func() {
		runCancel()
		resultErr = errors.Join(resultErr, s.closeRuntime())
		s.lifecycleMu.Lock()
		s.closed = true
		close(runDone)
		s.lifecycleMu.Unlock()
	}()

	if err := s.components.platform.Start(runCtx); err != nil {
		if errors.Is(err, context.Canceled) && errors.Is(runCtx.Err(), context.Canceled) {
			return nil
		}
		return fmt.Errorf("start platform: %w", err)
	}
	if s.components.jobs != nil {
		if err := s.components.jobs.Start(runCtx); err != nil {
			if errors.Is(err, context.Canceled) && errors.Is(runCtx.Err(), context.Canceled) {
				return nil
			}
			return fmt.Errorf("start durable jobs: %w", err)
		}
	}
	if s.components.websocket != nil {
		if err := s.components.websocket.Start(runCtx); err != nil {
			if errors.Is(err, context.Canceled) && errors.Is(runCtx.Err(), context.Canceled) {
				return nil
			}
			return fmt.Errorf("start WebSocket: %w", err)
		}
	}

	cfg := s.components.platform.Config()
	listener, err := s.components.listen("tcp", cfg.Server.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Server.ListenAddress, err)
	}

	httpServer := s.components.newHTTP(httpServerSettings{
		handler:           s.components.transport,
		errorLog:          s.components.platform.Log().StdLogger(slog.LevelError),
		readHeaderTimeout: cfg.Server.ReadHeaderTimeout.Duration,
		readTimeout:       cfg.Server.ReadTimeout.Duration,
		writeTimeout:      cfg.Server.WriteTimeout.Duration,
		idleTimeout:       cfg.Server.IdleTimeout.Duration,
		maxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	})
	s.httpMu.Lock()
	s.http = httpServer
	s.httpMu.Unlock()

	serveErrors := make(chan error, 1)
	// The listener is bound and every mandatory runtime dependency is running.
	// Publish readiness before Serve can notify test/runtime observers that it
	// entered the accept loop; an immediate Serve failure clears it below.
	s.components.readiness.SetReady(true)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()
	s.components.platform.Log().InfoContext(
		runCtx,
		"server started",
		mlog.String("listen_address", listener.Addr().String()),
		mlog.String("public_url", cfg.Server.PublicURL),
		mlog.String("version", app.Version),
	)

	select {
	case serveErr := <-serveErrors:
		s.components.readiness.SetReady(false)
		shutdownErr := s.shutdownHTTP(cfg.Server.ShutdownTimeout.Duration)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("serve HTTP: %w", serveErr), shutdownErr)
		}
		return shutdownErr
	case <-runCtx.Done():
		s.components.readiness.SetReady(false)
		s.components.platform.Log().Info("server shutdown started")
	}

	if err := s.shutdownHTTP(cfg.Server.ShutdownTimeout.Duration); err != nil {
		return err
	}
	shutdownTimer := time.NewTimer(cfg.Server.ShutdownTimeout.Duration)
	defer shutdownTimer.Stop()
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
		}
	case <-shutdownTimer.C:
		return errors.New("HTTP server did not stop after graceful shutdown")
	}
	s.components.platform.Log().Info("server stopped")
	return nil
}

// Close makes the node unready, gracefully stops HTTP when running, and then
// closes transport and shared infrastructure. It is idempotent, waits for a
// running Start call to finish, and returns any shutdown failures.
func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.closed {
		done := s.runDone
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return s.closeRuntime()
	}
	s.closed = true
	cancel := s.runCancel
	done := s.runDone
	started := s.started
	s.lifecycleMu.Unlock()

	s.components.readiness.SetReady(false)
	if !started {
		return s.closeRuntime()
	}
	cancel()
	<-done
	return s.closeRuntime()
}

// Ready reports whether the server can currently accept traffic.
func (s *Server) Ready() bool {
	return s.components.readiness.Ready()
}

func (s *Server) shutdownHTTP(timeout time.Duration) error {
	s.httpMu.Lock()
	httpServer := s.http
	s.httpMu.Unlock()
	if httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		return errors.Join(fmt.Errorf("graceful HTTP shutdown: %w", err), httpServer.Close())
	}
	return nil
}

func (s *Server) closeRuntime() error {
	s.closeOnce.Do(func() {
		// Stop durable handlers before transports and shared infrastructure so
		// no worker outlives its persistence or VFS dependencies.
		var jobsErr error
		if s.components.jobs != nil {
			jobsErr = s.components.jobs.Close()
		}
		var websocketErr error
		if s.components.websocket != nil {
			websocketErr = s.components.websocket.Close()
		}
		s.closeErr = errors.Join(
			jobsErr,
			websocketErr,
			s.components.transport.Close(),
			s.components.platform.Close(),
		)
	})
	return s.closeErr
}
