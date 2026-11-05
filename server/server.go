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
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/logging"
	"github.com/sudosylabs/proctor/server/model"
)

type options struct {
	configPath       string
	readyNotifier    ReadyNotifier
	stoppingNotifier StoppingNotifier
	// requireCurrentSchema makes construction refuse an out-of-date schema
	// instead of migrating it; offline host commands must not converge
	// schema as a side effect.
	requireCurrentSchema bool
}

// withCurrentSchemaRequired constructs an inert graph that never applies
// migrations and fails unless the database schema matches this build.
func withCurrentSchemaRequired() Option {
	return func(settings *options) error {
		settings.requireCurrentSchema = true
		return nil
	}
}

// Option configures construction without exposing infrastructure or transport
// implementation types.
type Option func(*options) error

// ReadyNotifier observes the single transition from startup to serving
// readiness. Implementations must return promptly; notification failures are
// logged and do not stop an otherwise healthy server.
type ReadyNotifier func(context.Context) error

// StoppingNotifier observes the single transition of a running node into
// shutdown. Implementations must return promptly; notification failures are
// logged and never delay or alter the shutdown sequence.
type StoppingNotifier func(context.Context) error

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

// WithReadyNotifier attaches a host-process readiness notification. The
// notifier runs exactly once after every required runtime dependency and HTTP
// serving are ready. It is not called when startup fails or is canceled.
func WithReadyNotifier(notify ReadyNotifier) Option {
	return func(settings *options) error {
		if notify == nil {
			return errors.New("ready notifier is nil")
		}
		settings.readyNotifier = notify
		return nil
	}
}

// WithStoppingNotifier attaches a host-process shutdown notification. The
// notifier runs exactly once when a node that Run started begins shutting
// down, after readiness is withdrawn and before HTTP stops. It is not called
// when an inert node closes.
func WithStoppingNotifier(notify StoppingNotifier) Option {
	return func(settings *options) error {
		if notify == nil {
			return errors.New("stopping notifier is nil")
		}
		settings.stoppingNotifier = notify
		return nil
	}
}

// runtimeComponent is the lifecycle shape of durable Jobs and the WebSocket
// transport. Construction is inert; Start enters runtime work and Close drains
// it until ctx, the remainder of the shared shutdown budget, ends. Jobs start
// only after the authoritative store is ready and drain before transports and
// infrastructure close, so handlers never outlive their VFS or persistence
// dependencies while the budget lasts.
type runtimeComponent interface {
	Start(context.Context) error
	Close(context.Context) error
}

// runtimePlatform is the infrastructure owner. It starts first and closes
// last, within the remainder of the shutdown budget it is handed.
type runtimePlatform interface {
	Start(context.Context) error
	Close(context.Context) error
}

// monitoredRuntimeComponent is a started component that reports asynchronous
// runtime failures that stop the node, such as a lost serving lease.
type monitoredRuntimeComponent interface {
	Start(context.Context) error
	Close() error
	Failures() <-chan error
}

// runtimeLogger is a borrowed operational view. Lifecycle operations such as
// Flush and Shutdown remain available only to Platform, the infrastructure
// owner; the architecture tests hold this contract free of lifecycle methods.
type runtimeLogger interface {
	Info(message string, fields ...logging.Field)
	InfoContext(ctx context.Context, message string, fields ...logging.Field)
	WarnContext(ctx context.Context, message string, fields ...logging.Field)
	ErrorContext(ctx context.Context, message string, fields ...logging.Field)
	StdLogger(level slog.Level) *log.Logger
}

type runtimeTransport interface {
	http.Handler
	Close() error
}

// runtimeMetrics is the metrics listener. Close stops it within ctx, the
// remainder of the shared shutdown budget.
type runtimeMetrics interface {
	runtimeComponent
	Failures() <-chan error
	SetReady(bool)
}

// startupReconciliation names the actorless startup convergence work.
var startupReconciliation = invocation.Task("startup-reconciliation")

// runtimeReconciler owns bounded durable startup convergence that must complete
// after infrastructure is ready and before any worker or transport can serve.
type runtimeReconciler interface {
	ReconcileSystemAdministratorRole(context.Context, invocation.System) error
	ReconcileAdministratorRecovery(context.Context, invocation.System) error
}

// runtimeAdministratorRecovery is borrowed only by the inert Server facade;
// it is never projected into HTTP or another transport.
type runtimeAdministratorRecovery interface {
	RecoverAdministratorAccess(context.Context, invocation.System, app.AdministratorRecoveryCommand) (*app.AdministratorRecoveryResult, error)
	ResetAdministratorMFA(context.Context, invocation.System, app.AdministratorMFAResetCommand) (*app.AdministratorMFAResetResult, error)
}

// runtimeTracing is the span exporter's lifecycle: Close flushes buffered
// spans within the shutdown budget it is handed.
type runtimeTracing interface {
	Close(context.Context) error
}

type runtimeReadiness interface {
	Ready() bool
	SetReady(bool)
}

type httpRuntime interface {
	Serve(net.Listener, func() bool) error
	// Stop drains in-flight requests until ctx ends and then closes the
	// connections that remain; an ended ctx closes at once.
	Stop(context.Context) error
}

// ownershipListener transfers the bound listener at the first HTTP accept.
// Until then Server remains responsible for closing it on cancellation.
type ownershipListener struct {
	net.Listener
	accept func() bool
	mu     sync.Mutex
	once   sync.Once
	owned  bool
}

func (l *ownershipListener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		l.mu.Lock()
		l.owned = l.accept()
		l.mu.Unlock()
	})
	l.mu.Lock()
	owned := l.owned
	l.mu.Unlock()
	if !owned {
		return nil, net.ErrClosed
	}
	return l.Listener.Accept()
}

func (l *ownershipListener) Close() error {
	l.mu.Lock()
	owned := l.owned
	l.mu.Unlock()
	if !owned {
		return nil
	}
	return l.Listener.Close()
}

type httpServerSettings struct {
	handler           http.Handler
	errorLog          *log.Logger
	listen            func(string, string) (net.Listener, error)
	tls               httpTLSSettings
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	maxHeaderBytes    int
}

type httpTLSSettings struct {
	mode                      config.ServerTLSMode
	certificateFile           string
	privateKeyFile            string
	letsEncryptEmail          string
	letsEncryptAcceptTOS      bool
	letsEncryptCacheDirectory string
	letsEncryptHostname       string
	forwardHTTPToHTTPS        bool
	httpListenAddress         string
	redirectAuthority         string
}

type runtimeSettings struct {
	listenAddress     string
	publicURL         string
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
	maxHeaderBytes    int
	tls               httpTLSSettings
}

func runtimeSettingsFromConfig(settings config.Server) runtimeSettings {
	publicURL, _ := url.Parse(settings.PublicURL)
	authority := canonicalPublicURLAuthority(publicURL)
	return runtimeSettings{
		listenAddress: settings.ListenAddress, publicURL: settings.PublicURL,
		readHeaderTimeout: settings.ReadHeaderTimeout.Duration,
		readTimeout:       settings.ReadTimeout.Duration, writeTimeout: settings.WriteTimeout.Duration,
		idleTimeout: settings.IdleTimeout.Duration, shutdownTimeout: settings.ShutdownTimeout.Duration,
		maxHeaderBytes: settings.MaxHeaderBytes,
		tls: httpTLSSettings{
			mode:                      settings.TLS.Mode,
			certificateFile:           settings.TLS.CertificateFile,
			privateKeyFile:            settings.TLS.PrivateKeyFile,
			letsEncryptEmail:          settings.TLS.LetsEncrypt.Email,
			letsEncryptAcceptTOS:      settings.TLS.LetsEncrypt.AcceptTermsOfService,
			letsEncryptCacheDirectory: settings.TLS.LetsEncrypt.CacheDirectory,
			letsEncryptHostname:       settings.LetsEncryptHostname(),
			forwardHTTPToHTTPS:        settings.TLS.ForwardHTTPToHTTPS,
			httpListenAddress:         settings.TLS.HTTPListenAddress,
			redirectAuthority:         authority,
		},
	}
}

func canonicalPublicURLAuthority(publicURL *url.URL) string {
	if publicURL == nil {
		return ""
	}
	hostname := strings.TrimSuffix(strings.ToLower(publicURL.Hostname()), ".")
	if port := publicURL.Port(); port != "" {
		return net.JoinHostPort(hostname, port)
	}
	if strings.ContainsRune(hostname, ':') {
		return "[" + hostname + "]"
	}
	return hostname
}

type runtimeComponents struct {
	platform              runtimePlatform
	reconciler            runtimeReconciler
	administratorRecovery runtimeAdministratorRecovery
	settings              runtimeSettings
	logger                runtimeLogger
	jobs                  runtimeComponent
	servingLease          monitoredRuntimeComponent
	metrics               runtimeMetrics
	// tracing flushes and stops span export; nil when tracing was never built.
	tracing   runtimeTracing
	transport runtimeTransport
	websocket runtimeComponent
	readiness runtimeReadiness
	listen    func(string, string) (net.Listener, error)
	newHTTP   func(httpServerSettings) httpRuntime
}

// AdministratorRecoveryCommand is the narrow module-root contract consumed by
// the host CLI. Password must come from a private input channel, never argv.
type AdministratorRecoveryCommand struct {
	InstitutionID    string
	UserID           string
	EnableLocalLogin bool
	Password         string
}

type AdministratorRecoveryResult struct {
	LocalLoginEnabled bool
	PasswordRotated   bool
}

// lifecycleMilestones records only stages that the node successfully entered.
// It is private because callers need behavioral lifecycle operations, not a
// second state-machine API.
type lifecycleMilestones struct {
	platformStarted  bool
	jobsStarted      bool
	websocketStarted bool
	listenerBound    bool
	httpServing      bool
	ready            bool
}

type nodeState uint8

const (
	nodeInert nodeState = iota
	nodeStarting
	nodeRunning
	nodeStopping
	nodeClosed
)

// Server is the lifecycle owner for one assembled Proctor node.
type Server struct {
	components runtimeComponents
	// readyNotifier and stoppingNotifier are borrowed host-process observers,
	// not owned runtime components. They have no lifecycle; readyNotifier runs
	// only after readiness and stoppingNotifier only when shutdown begins.
	readyNotifier    ReadyNotifier
	stoppingNotifier StoppingNotifier

	lifecycleMu sync.Mutex
	state       nodeState
	runCancel   context.CancelFunc
	runDone     chan struct{}
	milestones  lifecycleMilestones
	listener    net.Listener

	httpMu    sync.Mutex
	http      httpRuntime
	serveExit <-chan error

	// forced closes once when the host demands an immediate stop.
	forceOnce sync.Once
	forced    chan struct{}

	// closeErr is the one shutdown result shared by Run and every Close.
	closeOnce sync.Once
	closeErr  error
}

// RecoverAdministratorAccess runs only while the constructed node remains
// inert. Running the node permanently closes this host-only capability.
func (s *Server) RecoverAdministratorAccess(ctx context.Context, command AdministratorRecoveryCommand) (*AdministratorRecoveryResult, error) {
	if s == nil {
		return nil, errors.New("server is nil")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.state != nodeInert {
		return nil, errors.New("administrator recovery requires an inert server")
	}
	recovery := s.components.administratorRecovery
	if recovery == nil {
		return nil, errors.New("administrator recovery is unavailable")
	}
	result, err := recovery.RecoverAdministratorAccess(ctx, invocation.Task("administrator-recovery"), app.AdministratorRecoveryCommand{
		InstitutionID: model.InstitutionID(command.InstitutionID), UserID: model.UserID(command.UserID),
		EnableLocalLogin: command.EnableLocalLogin, Password: command.Password,
	})
	if err != nil {
		return nil, err
	}
	return &AdministratorRecoveryResult{
		LocalLoginEnabled: result.LocalLoginEnabled,
		PasswordRotated:   result.PasswordRotated,
	}, nil
}

// New constructs one Proctor server. It validates options and returns
// configuration or infrastructure construction failures without a partially
// usable Server.
//
// New does not start network listeners or WebSocket background work. Call
// Run to begin serving and Close to drain transports and infrastructure.
func New(ctx context.Context, optionValues ...Option) (*Server, error) {
	settings := options{}
	for _, option := range optionValues {
		if option == nil {
			return nil, errors.New("server option is nil")
		}
		if err := option(&settings); err != nil {
			return nil, fmt.Errorf("apply server option: %w", err)
		}
	}

	environment, err := readServiceEnvironment()
	if err != nil {
		return nil, err
	}
	result, err := composeNode(ctx, compositionInput{
		configPath: settings.configPath, serviceEnvironment: environment,
		requireCurrentSchema: settings.requireCurrentSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("construct server: %w", err)
	}
	result.server.readyNotifier = settings.readyNotifier
	result.server.stoppingNotifier = settings.stoppingNotifier
	return result.server, nil
}

// Run starts shared infrastructure and HTTP serving in dependency order,
// marks the node ready only after both have started, and blocks until
// cancellation or serving failure. It returns an error when the server is
// closed, was already run, cannot start a dependency or listener, or stops
// unexpectedly. Every exit path goes through the one shutdown sequence.
func (s *Server) Run(ctx context.Context) (resultErr error) {
	runCtx, endRun, err := s.beginRun(ctx)
	if err != nil {
		return err
	}
	var stop stopPlan
	defer func() {
		resultErr = errors.Join(stop.cause, s.endRun(stop, endRun))
	}()
	stop = s.serve(runCtx)
	return nil
}

// beginRun moves an inert node to starting and returns the run context that
// cancellation and Close end.
func (s *Server) beginRun(ctx context.Context) (context.Context, context.CancelFunc, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.state == nodeClosed {
		return nil, nil, errors.New("server is closed")
	}
	if s.state != nodeInert {
		return nil, nil, errors.New("server has already been run")
	}
	runCtx, runCancel := context.WithCancel(ctx)
	s.state = nodeStarting
	s.runCancel = runCancel
	s.runDone = make(chan struct{})
	return runCtx, runCancel, nil
}

// serve runs startup, HTTP handoff, readiness, and steady-state serving, and
// returns how the shutdown sequence must stop the node.
func (s *Server) serve(runCtx context.Context) stopPlan {
	if err := s.startRuntime(runCtx); err != nil {
		return stopAfter(err)
	}
	listener, err := s.bindListener(runCtx)
	if err != nil {
		return stopAfter(err)
	}
	serving := s.startHTTP(runCtx, listener)
	if stop, handedOff := s.awaitHandoff(runCtx, serving); !handedOff {
		return stop
	}
	s.announceReady(runCtx, listener)
	return s.awaitStop(runCtx, serving)
}

// endRun shuts the node down and releases Close callers waiting for Run.
func (s *Server) endRun(stop stopPlan, endRun context.CancelFunc) error {
	err := s.shutdown(stop, endRun)
	s.lifecycleMu.Lock()
	s.state = nodeClosed
	close(s.runDone)
	s.lifecycleMu.Unlock()
	return err
}

// errStopRequested ends startup without a failure: the run was canceled.
var errStopRequested = errors.New("stop requested during startup")

func stopAfter(err error) stopPlan {
	if errors.Is(err, errStopRequested) {
		return stopPlan{}
	}
	return stopPlan{cause: err}
}

// startupStage is one ordered step of node startup. verify reports a runtime
// failure that became observable while the stage started.
type startupStage struct {
	name    string
	start   func(context.Context) error
	entered func(context.Context)
	verify  func() error
}

func (s *Server) startupStages() []startupStage {
	stages := []startupStage{{
		name:    "start platform",
		start:   s.components.platform.Start,
		entered: func(context.Context) { s.recordStarted(func(m *lifecycleMilestones) { m.platformStarted = true }) },
	}}
	if s.components.metrics != nil {
		stages = append(stages, startupStage{
			name: "start metrics", start: s.components.metrics.Start, verify: s.verifyMetrics,
		})
	}
	if s.components.servingLease != nil {
		stages = append(stages, startupStage{
			name: "start serving node lease", start: s.components.servingLease.Start, verify: s.verifyServingNodeLease,
			entered: func(ctx context.Context) { s.components.logger.InfoContext(ctx, "serving node lease started") },
		})
	}
	if reconciler := s.components.reconciler; reconciler != nil {
		stages = append(stages, startupStage{
			name: "reconcile protected built-in roles",
			start: func(ctx context.Context) error {
				return reconciler.ReconcileSystemAdministratorRole(ctx, startupReconciliation)
			},
		}, startupStage{
			name: "reconcile offline administrator recovery",
			start: func(ctx context.Context) error {
				return reconciler.ReconcileAdministratorRecovery(ctx, startupReconciliation)
			},
			entered: func(ctx context.Context) { s.components.logger.InfoContext(ctx, "startup reconciliation complete") },
			verify:  s.verifyServingNodeLease,
		})
	}
	if s.components.jobs != nil {
		stages = append(stages, startupStage{
			name: "start durable jobs", start: s.components.jobs.Start, verify: s.verifyServingNodeLease,
			entered: func(ctx context.Context) {
				s.recordStarted(func(m *lifecycleMilestones) { m.jobsStarted = true })
				s.components.logger.InfoContext(ctx, "durable jobs started")
			},
		})
	}
	if s.components.websocket != nil {
		stages = append(stages, startupStage{
			name: "start WebSocket", start: s.components.websocket.Start, verify: s.verifyServingNodeLease,
			entered: func(ctx context.Context) {
				s.recordStarted(func(m *lifecycleMilestones) { m.websocketStarted = true })
				s.components.logger.InfoContext(ctx, "WebSocket service started")
			},
		})
	}
	return stages
}

// startRuntime enters every startup stage in dependency order. Cancellation
// between or during stages stops startup without a failure; a dependency
// failure wins over a concurrent cancellation.
func (s *Server) startRuntime(ctx context.Context) error {
	for _, stage := range s.startupStages() {
		if err := stage.start(ctx); err != nil {
			if gracefulCancellation(err, ctx.Err()) {
				return errStopRequested
			}
			return fmt.Errorf("%s: %w", stage.name, err)
		}
		if stage.entered != nil {
			stage.entered(ctx)
		}
		if stage.verify != nil {
			if err := stage.verify(); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return errStopRequested
		}
	}
	return nil
}

func (s *Server) verifyMetrics() error {
	if err := s.currentMetricsFailure(); err != nil {
		return fmt.Errorf("serve metrics: %w", err)
	}
	return nil
}

func (s *Server) verifyServingNodeLease() error {
	if err := s.currentServingNodeLeaseFailure(); err != nil {
		return fmt.Errorf("maintain serving node lease: %w", err)
	}
	return nil
}

// bindListener binds the client-facing listener. Until HTTP accepts it, the
// Server owns the listener and the shutdown sequence closes it.
func (s *Server) bindListener(ctx context.Context) (net.Listener, error) {
	address := s.components.settings.listenAddress
	listener, err := s.components.listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", address, err)
	}
	s.lifecycleMu.Lock()
	s.listener = listener
	s.milestones.listenerBound = true
	s.lifecycleMu.Unlock()
	if ctx.Err() != nil {
		return nil, errStopRequested
	}
	return listener, nil
}

// httpServe is one running Serve call: its exit result and whether it
// accepted the listener handoff.
type httpServe struct {
	exit    <-chan error
	handoff <-chan bool
}

func (s *Server) startHTTP(runCtx context.Context, listener net.Listener) httpServe {
	settings := s.components.settings
	httpServer := s.components.newHTTP(httpServerSettings{
		handler:           s.components.transport,
		errorLog:          s.components.logger.StdLogger(slog.LevelError),
		listen:            s.components.listen,
		tls:               settings.tls,
		readHeaderTimeout: settings.readHeaderTimeout,
		readTimeout:       settings.readTimeout,
		writeTimeout:      settings.writeTimeout,
		idleTimeout:       settings.idleTimeout,
		maxHeaderBytes:    settings.maxHeaderBytes,
	})
	exit := make(chan error, 1)
	handoff := make(chan bool, 1)
	s.httpMu.Lock()
	s.http, s.serveExit = httpServer, exit
	s.httpMu.Unlock()
	go func() {
		exit <- httpServer.Serve(listener, func() bool {
			s.lifecycleMu.Lock()
			accepted := s.state == nodeStarting && runCtx.Err() == nil && s.listener == listener
			if accepted {
				s.listener = nil
				s.milestones.listenerBound = false
				s.milestones.httpServing = true
			}
			s.lifecycleMu.Unlock()
			handoff <- accepted
			return accepted
		})
	}()
	return httpServe{exit: exit, handoff: handoff}
}

// awaitHandoff waits until HTTP accepts the listener. The listener is bound
// and every mandatory runtime dependency is running; an HTTP runtime that
// rejects the listener reports that error instead of acknowledging it.
func (s *Server) awaitHandoff(runCtx context.Context, serving httpServe) (stopPlan, bool) {
	select {
	case serveErr := <-serving.exit:
		stop := stopPlan{serveExited: true}
		// Close marks the node stopping before it cancels runCtx, so a
		// refused handoff can surface the closed listener first.
		if !s.handoffRefusedForShutdown(runCtx, serveErr) && serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			stop.cause = fmt.Errorf("serve HTTP: %w", serveErr)
		}
		return stop, false
	case accepted := <-serving.handoff:
		if !accepted {
			return stopPlan{}, false
		}
	case <-runCtx.Done():
		return stopPlan{}, false
	case leaseErr := <-s.servingNodeLeaseFailures():
		return stopPlan{cause: fmt.Errorf("maintain serving node lease: %w", leaseErr), force: true}, false
	case metricsErr := <-s.metricsFailures():
		return stopPlan{cause: fmt.Errorf("serve metrics: %w", metricsErr), force: true}, false
	}
	// Handoff and a dependency failure may become selectable together. Drain
	// any already-queued failure before publishing readiness so a dead required
	// listener or serving lease is never briefly advertised as healthy.
	if err := s.verifyMetrics(); err != nil {
		return stopPlan{cause: err, force: true}, false
	}
	if err := s.verifyServingNodeLease(); err != nil {
		return stopPlan{cause: err, force: true}, false
	}
	return stopPlan{}, true
}

// announceReady publishes readiness and then tells the host process.
func (s *Server) announceReady(runCtx context.Context, listener net.Listener) {
	if !s.publishReady(runCtx) {
		return
	}
	settings := s.components.settings
	s.components.logger.InfoContext(
		runCtx,
		"server started",
		logging.String("listen_address", listener.Addr().String()),
		logging.String("public_url", settings.publicURL),
		logging.String("tls_mode", string(settings.tls.mode)),
		logging.Bool("http_to_https_forwarding", settings.tls.forwardHTTPToHTTPS),
		logging.String("version", app.Version),
	)
	if s.readyNotifier == nil {
		return
	}
	if err := s.readyNotifier(runCtx); err != nil {
		s.components.logger.ErrorContext(runCtx, "host readiness notification failed", logging.Err(err))
		return
	}
	s.components.logger.InfoContext(runCtx, "host readiness notification sent")
}

// awaitStop serves until cancellation or a runtime failure. A failed serving
// lease or metrics listener closes HTTP at once instead of draining it.
func (s *Server) awaitStop(runCtx context.Context, serving httpServe) stopPlan {
	select {
	case serveErr := <-serving.exit:
		stop := stopPlan{serveExited: true}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			stop.cause = fmt.Errorf("serve HTTP: %w", serveErr)
		}
		return stop
	case <-runCtx.Done():
		return stopPlan{}
	case leaseErr := <-s.servingNodeLeaseFailures():
		s.components.logger.ErrorContext(runCtx, "serving node lease failed; stopping server", logging.Err(leaseErr))
		return stopPlan{cause: fmt.Errorf("maintain serving node lease: %w", leaseErr), force: true}
	case metricsErr := <-s.metricsFailures():
		s.components.logger.ErrorContext(runCtx, "metrics listener failed; stopping server", logging.Err(metricsErr))
		return stopPlan{cause: fmt.Errorf("serve metrics: %w", metricsErr), force: true}
	}
}

// Close makes the node unready, stops a running node through the shutdown
// sequence, and disposes an inert one. It is idempotent, waits for a running
// Run call to finish, and returns the shutdown failures every caller shares.
func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.state == nodeClosed {
		done := s.runDone
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return s.shutdown(stopPlan{}, nil)
	}
	cancel, done := s.runCancel, s.runDone
	if done == nil {
		s.state = nodeClosed
	} else {
		s.state = nodeStopping
	}
	s.lifecycleMu.Unlock()

	s.setUnready()
	if done != nil {
		cancel()
		<-done
	}
	return s.shutdown(stopPlan{}, nil)
}

// Ready reports whether the server can currently accept traffic.
func (s *Server) Ready() bool {
	return s.components.readiness.Ready()
}

// Reload re-reads the operator-owned configuration file, applying it to the
// dynamically reconfigurable consumers as one change, and reloads static TLS
// certificate files for a serving node's HTTP and metrics listeners. Each step is independent: a failure
// keeps that step's current state in force, is logged, and is returned
// joined. Changed settings that apply only at startup keep their running
// values and are named in a warning. The host process invokes Reload on
// SIGHUP.
func (s *Server) Reload(ctx context.Context) error {
	var result error
	if reloader, ok := s.components.platform.(configurationReloader); ok {
		reloaded, err := reloader.ReloadConfiguration(ctx)
		switch {
		case err != nil:
			result = errors.Join(result, fmt.Errorf("reload configuration: %w", err))
			s.components.logger.ErrorContext(ctx, "configuration reload failed; keeping the current configuration", logging.Err(err))
		case len(reloaded.RestartRequired) > 0:
			s.components.logger.WarnContext(ctx, "configuration reloaded; changed settings that require a restart keep their running values",
				logging.String("restart_required", strings.Join(reloaded.RestartRequired, ", ")))
		default:
			s.components.logger.InfoContext(ctx, "configuration reloaded")
		}
	}
	s.httpMu.Lock()
	httpServer := s.http
	s.httpMu.Unlock()
	if reloader, ok := httpServer.(certificateReloader); ok {
		if err := reloader.ReloadCertificates(); err != nil {
			result = errors.Join(result, fmt.Errorf("reload TLS certificates: %w", err))
			s.components.logger.ErrorContext(ctx, "TLS certificate reload failed; keeping the current certificate", logging.Err(err))
		}
	}
	if reloader, ok := s.components.metrics.(certificateReloader); ok {
		if err := reloader.ReloadCertificates(); err != nil {
			result = errors.Join(result, fmt.Errorf("reload metrics TLS certificates: %w", err))
			s.components.logger.ErrorContext(ctx, "metrics TLS certificate reload failed; keeping the current certificate", logging.Err(err))
		}
	}
	return result
}

// certificateReloader is the capability Reload uses on the HTTP runtime and the
// metrics listener to reload operator-managed TLS key pairs.
type certificateReloader interface {
	ReloadCertificates() error
}

// configurationReloader is the platform capability Reload uses.
type configurationReloader interface {
	ReloadConfiguration(context.Context) (config.ReloadResult, error)
}

func (s *Server) servingNodeLeaseFailures() <-chan error {
	if s.components.servingLease == nil {
		return nil
	}
	return s.components.servingLease.Failures()
}

func (s *Server) currentServingNodeLeaseFailure() error {
	select {
	case err := <-s.servingNodeLeaseFailures():
		return err
	default:
		return nil
	}
}

func (s *Server) metricsFailures() <-chan error {
	if s.components.metrics == nil {
		return nil
	}
	return s.components.metrics.Failures()
}

func (s *Server) currentMetricsFailure() error {
	select {
	case err := <-s.metricsFailures():
		return err
	default:
		return nil
	}
}

func (s *Server) recordStarted(record func(*lifecycleMilestones)) {
	s.lifecycleMu.Lock()
	record(&s.milestones)
	s.lifecycleMu.Unlock()
}

// handoffRefusedForShutdown reports a serve result that only reflects the
// listener handoff being refused because shutdown was requested: by
// cancellation, or by a Close that has marked the node stopping but has not
// yet canceled the run context.
func (s *Server) handoffRefusedForShutdown(runCtx context.Context, serveErr error) bool {
	if !errors.Is(serveErr, net.ErrClosed) {
		return false
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.milestones.httpServing {
		return false
	}
	return runCtx.Err() != nil || s.state == nodeStopping || s.state == nodeClosed
}

func (s *Server) httpServing() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.milestones.httpServing
}

func (s *Server) publishReady(ctx context.Context) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.state != nodeStarting || ctx.Err() != nil || !s.milestones.httpServing {
		return false
	}
	s.milestones.ready = true
	s.state = nodeRunning
	s.components.readiness.SetReady(true)
	if s.components.metrics != nil {
		s.components.metrics.SetReady(true)
	}
	return true
}

func (s *Server) setUnready() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.milestones.ready = false
	s.components.readiness.SetReady(false)
	if s.components.metrics != nil {
		s.components.metrics.SetReady(false)
	}
}

func gracefulCancellation(err, contextErr error) bool {
	if err == nil || contextErr == nil {
		return false
	}
	return errorContainsOnly(err, contextErr)
}

func errorContainsOnly(err, target error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !errorContainsOnly(child, target) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return errorContainsOnly(wrapped.Unwrap(), target)
	}
	return err == target
}
