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
	configPath    string
	readyNotifier ReadyNotifier
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

var errHTTPServerStopTimeout = errors.New("HTTP server did not stop after graceful shutdown")

// Option configures construction without exposing infrastructure or transport
// implementation types.
type Option func(*options) error

// ReadyNotifier observes the single transition from startup to serving
// readiness. Implementations must return promptly; notification failures are
// logged and do not stop an otherwise healthy server.
type ReadyNotifier func(context.Context) error

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

// runtimeComponent is the one lifecycle shape the Server drives: the
// infrastructure Platform, durable Jobs, and the WebSocket transport.
// Construction is inert; Start enters runtime work and Close drains it. Jobs
// start only after the authoritative store is ready and drain before
// transports and infrastructure close, so handlers never outlive their VFS or
// persistence dependencies.
type runtimeComponent interface {
	Start(context.Context) error
	Close() error
}

// monitoredRuntimeComponent additionally reports asynchronous runtime
// failures that stop the node, such as a lost serving lease.
type monitoredRuntimeComponent interface {
	runtimeComponent
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

type runtimeMetrics interface {
	monitoredRuntimeComponent
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

type runtimeReadiness interface {
	Ready() bool
	SetReady(bool)
}

type httpRuntime interface {
	Serve(net.Listener, func() bool) error
	Shutdown(context.Context) error
	Close() error
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
	platform              runtimeComponent
	reconciler            runtimeReconciler
	administratorRecovery runtimeAdministratorRecovery
	settings              runtimeSettings
	logger                runtimeLogger
	jobs                  runtimeComponent
	servingLease          monitoredRuntimeComponent
	metrics               runtimeMetrics
	transport             runtimeTransport
	websocket             runtimeComponent
	readiness             runtimeReadiness
	listen                func(string, string) (net.Listener, error)
	newHTTP               func(httpServerSettings) httpRuntime
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
	// readyNotifier is a borrowed host-process observer, not an owned runtime
	// component. It has no lifecycle and is invoked only after readiness.
	readyNotifier ReadyNotifier

	lifecycleMu  sync.Mutex
	state        nodeState
	runCancel    context.CancelFunc
	runDone      chan struct{}
	milestones   lifecycleMilestones
	listener     net.Listener
	lifecycleErr error

	httpMu sync.Mutex
	http   httpRuntime
	// httpShutdownErr is retained so the Close call that requested shutdown and
	// every later Close observe the same drain failure as Run.
	httpShutdownErr error

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
	return result.server, nil
}

// Run starts shared infrastructure and HTTP serving in dependency order,
// marks the node ready only after both have started, and blocks until
// cancellation or serving failure. It returns an error when the server is
// closed, was already run, cannot start a dependency or listener, or stops
// unexpectedly. Every exit path closes the assembled runtime.
func (s *Server) Run(ctx context.Context) (resultErr error) {
	s.lifecycleMu.Lock()
	if s.state == nodeClosed {
		s.lifecycleMu.Unlock()
		return errors.New("server is closed")
	}
	if s.state != nodeInert {
		s.lifecycleMu.Unlock()
		return errors.New("server has already been run")
	}
	runCtx, runCancel := context.WithCancel(ctx)
	s.state = nodeStarting
	s.runCancel = runCancel
	s.runDone = make(chan struct{})
	runDone := s.runDone
	s.lifecycleMu.Unlock()

	defer func() {
		runCancel()
		resultErr = errors.Join(resultErr, s.closeRuntime())
		s.lifecycleMu.Lock()
		s.state = nodeClosed
		close(runDone)
		s.lifecycleMu.Unlock()
	}()

	if err := s.components.platform.Start(runCtx); err != nil {
		if gracefulCancellation(err, runCtx.Err()) {
			return nil
		}
		return fmt.Errorf("start platform: %w", err)
	}
	s.recordStarted(func(m *lifecycleMilestones) { m.platformStarted = true })
	if s.components.metrics != nil {
		if err := s.components.metrics.Start(runCtx); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("start metrics: %w", err)
		}
		if err := s.currentMetricsFailure(); err != nil {
			return fmt.Errorf("serve metrics: %w", err)
		}
	}
	if runCtx.Err() != nil {
		return nil
	}
	if s.components.servingLease != nil {
		if err := s.components.servingLease.Start(runCtx); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("start serving node lease: %w", err)
		}
		if err := s.currentServingNodeLeaseFailure(); err != nil {
			return fmt.Errorf("maintain serving node lease: %w", err)
		}
		s.components.logger.InfoContext(runCtx, "serving node lease started")
	}
	if s.components.reconciler != nil {
		if err := s.components.reconciler.ReconcileSystemAdministratorRole(runCtx, startupReconciliation); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("reconcile protected built-in roles: %w", err)
		}
		if runCtx.Err() != nil {
			return nil
		}
		if err := s.components.reconciler.ReconcileAdministratorRecovery(runCtx, startupReconciliation); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("reconcile offline administrator recovery: %w", err)
		}
		if runCtx.Err() != nil {
			return nil
		}
		if err := s.currentServingNodeLeaseFailure(); err != nil {
			return fmt.Errorf("maintain serving node lease: %w", err)
		}
		s.components.logger.InfoContext(runCtx, "startup reconciliation complete")
	}
	if s.components.jobs != nil {
		if err := s.components.jobs.Start(runCtx); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("start durable jobs: %w", err)
		}
		s.recordStarted(func(m *lifecycleMilestones) { m.jobsStarted = true })
		s.components.logger.InfoContext(runCtx, "durable jobs started")
		if runCtx.Err() != nil {
			return nil
		}
		if err := s.currentServingNodeLeaseFailure(); err != nil {
			return fmt.Errorf("maintain serving node lease: %w", err)
		}
	}
	if s.components.websocket != nil {
		if err := s.components.websocket.Start(runCtx); err != nil {
			if gracefulCancellation(err, runCtx.Err()) {
				return nil
			}
			return fmt.Errorf("start WebSocket: %w", err)
		}
		s.recordStarted(func(m *lifecycleMilestones) { m.websocketStarted = true })
		s.components.logger.InfoContext(runCtx, "WebSocket service started")
		if runCtx.Err() != nil {
			return nil
		}
		if err := s.currentServingNodeLeaseFailure(); err != nil {
			return fmt.Errorf("maintain serving node lease: %w", err)
		}
	}

	settings := s.components.settings
	listener, err := s.components.listen("tcp", settings.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", settings.listenAddress, err)
	}
	s.lifecycleMu.Lock()
	s.listener = listener
	s.milestones.listenerBound = true
	s.lifecycleMu.Unlock()
	if runCtx.Err() != nil {
		if err := s.closeOwnedListener(); err != nil {
			return fmt.Errorf("close listener after canceled startup: %w", err)
		}
		return nil
	}

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
	s.httpMu.Lock()
	s.http = httpServer
	s.httpMu.Unlock()

	serveErrors := make(chan error, 1)
	handoff := make(chan bool, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener, func() bool {
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
	// The listener is bound, every mandatory runtime dependency is running, and
	// the HTTP runtime has accepted the listener for serving. An implementation
	// that rejects the listener reports that error instead of acknowledging it.
	select {
	case serveErr := <-serveErrors:
		// Close marks the node stopping before it cancels runCtx, so a
		// refused handoff can surface the closed listener first.
		if s.handoffRefusedForShutdown(runCtx, serveErr) {
			return nil
		}
		var shutdownErr error
		if s.httpServing() {
			shutdownErr = s.shutdownHTTP(settings.shutdownTimeout)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("serve HTTP: %w", serveErr), shutdownErr)
		}
		return shutdownErr
	case accepted := <-handoff:
		if !accepted {
			return nil
		}
	case <-runCtx.Done():
		if err := s.closeOwnedListener(); err != nil {
			return fmt.Errorf("close listener before HTTP handoff: %w", err)
		}
		if !s.httpServing() {
			return nil
		}
	case leaseErr := <-s.servingNodeLeaseFailures():
		return errors.Join(
			fmt.Errorf("maintain serving node lease: %w", leaseErr),
			s.closeOwnedListener(),
			s.forceCloseHTTP(),
		)
	case metricsErr := <-s.metricsFailures():
		return errors.Join(fmt.Errorf("serve metrics: %w", metricsErr), s.closeOwnedListener(), s.forceCloseHTTP())
	}
	// Handoff and a dependency failure may become selectable together. Drain
	// any already-queued failure before publishing readiness so a dead required
	// listener or serving lease is never briefly advertised as healthy.
	if metricsErr := s.currentMetricsFailure(); metricsErr != nil {
		return errors.Join(fmt.Errorf("serve metrics: %w", metricsErr), s.forceCloseHTTP())
	}
	if leaseErr := s.currentServingNodeLeaseFailure(); leaseErr != nil {
		return errors.Join(fmt.Errorf("maintain serving node lease: %w", leaseErr), s.forceCloseHTTP())
	}
	if s.publishReady(runCtx) {
		s.components.logger.InfoContext(
			runCtx,
			"server started",
			logging.String("listen_address", listener.Addr().String()),
			logging.String("public_url", settings.publicURL),
			logging.String("tls_mode", string(settings.tls.mode)),
			logging.Bool("http_to_https_forwarding", settings.tls.forwardHTTPToHTTPS),
			logging.String("version", app.Version),
		)
		if s.readyNotifier != nil {
			if err := s.readyNotifier(runCtx); err != nil {
				s.components.logger.ErrorContext(
					runCtx,
					"host readiness notification failed",
					logging.Err(err),
				)
			} else {
				s.components.logger.InfoContext(runCtx, "host readiness notification sent")
			}
		}
	}

	var runtimeErr error
	forceServingStop := false
	select {
	case serveErr := <-serveErrors:
		s.setUnready()
		shutdownErr := s.shutdownHTTP(settings.shutdownTimeout)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("serve HTTP: %w", serveErr), shutdownErr)
		}
		return shutdownErr
	case <-runCtx.Done():
		s.setUnready()
		s.components.logger.Info("server shutdown started")
	case leaseErr := <-s.servingNodeLeaseFailures():
		runtimeErr = fmt.Errorf("maintain serving node lease: %w", leaseErr)
		forceServingStop = true
		s.setUnready()
		s.components.logger.ErrorContext(runCtx, "serving node lease failed; stopping server", logging.Err(leaseErr))
	case metricsErr := <-s.metricsFailures():
		runtimeErr = fmt.Errorf("serve metrics: %w", metricsErr)
		forceServingStop = true
		s.setUnready()
		s.components.logger.ErrorContext(runCtx, "metrics listener failed; stopping server", logging.Err(metricsErr))
	}

	var stopErr error
	if forceServingStop {
		stopErr = s.forceCloseHTTP()
	} else {
		stopErr = s.shutdownHTTP(settings.shutdownTimeout)
	}
	if stopErr != nil {
		return errors.Join(runtimeErr, stopErr)
	}
	shutdownTimer := time.NewTimer(settings.shutdownTimeout)
	defer shutdownTimer.Stop()
	select {
	case serveErr := <-serveErrors:
		// HTTP was just stopped by this lifecycle, so a closed listener is
		// the expected result of that stop, not a serving failure.
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
			err := fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
			s.retainHTTPShutdownError(err)
			return errors.Join(runtimeErr, err)
		}
	case <-shutdownTimer.C:
		err := errHTTPServerStopTimeout
		s.retainHTTPShutdownError(err)
		return errors.Join(runtimeErr, err)
	}
	s.components.logger.Info("server stopped")
	return runtimeErr
}

// Close makes the node unready, gracefully stops HTTP when running, and then
// closes transport and shared infrastructure. It is idempotent, waits for a
// running Run call to finish, and returns any shutdown failures.
func (s *Server) Close() error {
	s.lifecycleMu.Lock()
	if s.state == nodeClosed {
		done := s.runDone
		s.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return errors.Join(s.retainedLifecycleError(), s.retainedHTTPShutdownError(), s.closeRuntime())
	}
	s.state = nodeStopping
	cancel := s.runCancel
	done := s.runDone
	started := done != nil
	s.lifecycleMu.Unlock()

	s.setUnready()
	if !started {
		s.lifecycleMu.Lock()
		s.state = nodeClosed
		s.lifecycleMu.Unlock()
		return errors.Join(s.retainedLifecycleError(), s.retainedHTTPShutdownError(), s.closeRuntime())
	}
	cancel()
	<-done
	return errors.Join(s.retainedLifecycleError(), s.retainedHTTPShutdownError(), s.closeRuntime())
}

// Ready reports whether the server can currently accept traffic.
func (s *Server) Ready() bool {
	return s.components.readiness.Ready()
}

// Reload re-reads the operator-owned configuration file, applying it to the
// dynamically reconfigurable consumers as one change, and reloads static TLS
// certificate files for a serving node. Each step is independent: a failure
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
	if reloader, ok := httpServer.(interface{ ReloadCertificates() error }); ok {
		if err := reloader.ReloadCertificates(); err != nil {
			result = errors.Join(result, fmt.Errorf("reload TLS certificates: %w", err))
			s.components.logger.ErrorContext(ctx, "TLS certificate reload failed; keeping the current certificate", logging.Err(err))
		}
	}
	return result
}

// configurationReloader is the platform capability Reload uses.
type configurationReloader interface {
	ReloadConfiguration(context.Context) (config.ReloadResult, error)
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
	// The serving lease keeps renewing while HTTP drains. If it fails, serving
	// must stop before the lease can expire, so the drain becomes a close.
	leaseLost := make(chan error, 1)
	stopWatching := make(chan struct{})
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		select {
		case err := <-s.servingNodeLeaseFailures():
			leaseLost <- err
			cancel()
		case <-stopWatching:
		}
	}()
	err := httpServer.Shutdown(ctx)
	close(stopWatching)
	<-watching
	select {
	case leaseErr := <-leaseLost:
		result := errors.Join(fmt.Errorf("maintain serving node lease during HTTP shutdown: %w", leaseErr), httpServer.Close())
		s.retainHTTPShutdownError(result)
		return result
	default:
	}
	if err != nil {
		result := errors.Join(fmt.Errorf("graceful HTTP shutdown: %w", err), httpServer.Close())
		s.retainHTTPShutdownError(result)
		return result
	}
	return nil
}

func (s *Server) forceCloseHTTP() error {
	s.httpMu.Lock()
	httpServer := s.http
	s.httpMu.Unlock()
	if httpServer == nil {
		return nil
	}
	if err := httpServer.Close(); err != nil {
		result := fmt.Errorf("force close HTTP: %w", err)
		s.retainHTTPShutdownError(result)
		return result
	}
	return nil
}

func (s *Server) retainHTTPShutdownError(err error) {
	if err == nil {
		return
	}
	s.httpMu.Lock()
	s.httpShutdownErr = errors.Join(s.httpShutdownErr, err)
	s.httpMu.Unlock()
}

func (s *Server) retainedHTTPShutdownError() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	return s.httpShutdownErr
}

func (s *Server) closeRuntime() error {
	s.closeOnce.Do(func() {
		s.setUnready()

		s.lifecycleMu.Lock()
		listener := s.listener
		s.listener = nil
		s.milestones.listenerBound = false
		s.lifecycleMu.Unlock()

		var listenerErr error
		if listener != nil {
			listenerErr = listener.Close()
		}

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
		transportErr := s.components.transport.Close()
		var servingLeaseErr error
		if s.components.servingLease != nil {
			servingLeaseErr = s.components.servingLease.Close()
		}
		var metricsErr error
		if s.components.metrics != nil {
			metricsErr = s.components.metrics.Close()
		}
		s.closeErr = errors.Join(
			listenerErr,
			jobsErr,
			websocketErr,
			transportErr,
			servingLeaseErr,
			metricsErr,
			s.components.platform.Close(),
		)
	})
	return s.closeErr
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

func (s *Server) closeOwnedListener() error {
	s.lifecycleMu.Lock()
	listener := s.listener
	s.listener = nil
	s.milestones.listenerBound = false
	s.lifecycleMu.Unlock()
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if err != nil {
		s.retainLifecycleError(fmt.Errorf("close Server-owned listener: %w", err))
	}
	return err
}

func (s *Server) retainLifecycleError(err error) {
	if err == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.lifecycleErr = errors.Join(s.lifecycleErr, err)
	s.lifecycleMu.Unlock()
}

func (s *Server) retainedLifecycleError() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.lifecycleErr
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
