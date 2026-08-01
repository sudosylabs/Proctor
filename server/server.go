// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package server owns Proctor runtime composition and lifecycle. Business
// policy remains in the application layer and is not implemented here.
//
// As the composition root, this package may depend on the components it wires;
// those components must not depend back on the module-root package.
//
// During the architecture migration, New delegates component construction to
// the existing app server while this package owns startup, readiness, graceful
// HTTP shutdown, and cleanup. The legacy constructor starts bounded WebSocket
// replay maintenance during construction; callers must call Close even when
// Start is never called.
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
	transport runtimeTransport
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
// New does not start network listeners. During the architecture migration its
// legacy component constructor does start bounded WebSocket replay maintenance,
// so callers must call Close on every successfully constructed Server even if
// Start is never called.
func New(ctx context.Context, optionValues ...Option) (*Server, error) {
	settings := options{runtimeFactory: newLegacyRuntime}
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

func newLegacyRuntime(ctx context.Context, configPath string) (runtimeComponents, error) {
	var legacyOptions []app.Option
	if configPath != "" {
		legacyOptions = append(legacyOptions, app.WithConfigPath(configPath))
	}
	legacy, err := app.NewServer(ctx, legacyOptions...)
	if err != nil {
		return runtimeComponents{}, err
	}
	return runtimeComponents{
		platform:  legacy.Platform(),
		transport: legacy.API(),
		readiness: legacy.Health(),
		listen:    net.Listen,
		newHTTP:   newHTTPServer,
	}, nil
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
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	s.components.readiness.SetReady(true)
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
		// Connections are drained before shared infrastructure is stopped so
		// no socket remains attached to a node that can no longer receive
		// revocation or authorization invalidations.
		s.closeErr = errors.Join(
			s.components.transport.Close(),
			s.components.platform.Close(),
		)
	})
	return s.closeErr
}
