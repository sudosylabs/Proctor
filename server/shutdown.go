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
	"time"
)

// ErrShutdownForced reports that the host forced the stop before the graceful
// drain finished, so in-flight work may have been cut off.
var ErrShutdownForced = errors.New("shutdown was forced before the graceful drain finished")

var errHTTPServerStopTimeout = errors.New("HTTP server did not stop after graceful shutdown")

// forcedServeExitWait bounds the wait for Serve to return once HTTP has been
// closed rather than drained. A closed server returns promptly, so this wait
// is independent of whatever shutdown budget remains.
const forcedServeExitWait = 2 * time.Second

// stopPlan is what the run hands the shutdown sequence: why the node stops
// and how HTTP serving must end.
type stopPlan struct {
	// cause is the failure Run reports; nil when the stop was requested.
	cause error
	// force closes HTTP at once instead of draining it, because a required
	// runtime dependency such as the serving lease has failed.
	force bool
	// serveExited records that Serve already returned its result.
	serveExited bool
}

// shutdown runs the node's one ordered shutdown sequence exactly once. Run,
// Close, and every repeated Close share its result; the first caller's plan
// decides how HTTP stops. endRun ends the run context once HTTP has stopped;
// it is nil when an inert node closes.
func (s *Server) shutdown(plan stopPlan, endRun context.CancelFunc) error {
	s.closeOnce.Do(func() { s.closeErr = s.runShutdown(plan, endRun) })
	return s.closeErr
}

// runShutdown makes the node unready and then disposes every constructed
// owner in reverse dependency order: the Server-owned listener, HTTP serving,
// Jobs, WebSocket, the HTTP transport, the serving lease, metrics, tracing,
// and Platform. One budget of Server.ShutdownTimeout, starting now, bounds
// the HTTP drain and Serve exit and is handed on to the tracing flush and
// Platform; Jobs, WebSocket, and metrics close within their own bounds. The serving lease keeps
// renewing until its turn, after HTTP has stopped, so this node never serves
// without a proven lease.
func (s *Server) runShutdown(plan stopPlan, endRun context.CancelFunc) error {
	running := endRun != nil
	s.beginStopping(running)
	budget, cancelBudget := s.shutdownBudget()
	defer cancelBudget()

	steps := []func(context.Context) error{
		func(context.Context) error { return s.closeOwnedListener() },
		func(ctx context.Context) error { return s.stopHTTP(ctx, plan) },
		func(context.Context) error {
			if endRun != nil {
				endRun()
			}
			return nil
		},
		closing(s.components.jobs),
		closing(s.components.websocket),
		closing(s.components.transport),
		closing(s.components.servingLease),
		closing(s.components.metrics),
		// Tracing closes after every span producer so their final spans are
		// flushed, and before Platform so the exporter never outlives logging.
		func(ctx context.Context) error {
			if s.components.tracing == nil {
				return nil
			}
			return s.components.tracing.Close(ctx)
		},
		func(ctx context.Context) error {
			if running {
				s.components.logger.Info("server stopped")
			}
			if s.components.platform == nil {
				return nil
			}
			return s.components.platform.Close(ctx)
		},
	}
	var result error
	for _, step := range steps {
		result = errors.Join(result, step(budget))
	}
	if s.forceRequested() {
		result = errors.Join(result, ErrShutdownForced)
	}
	return result
}

func closing(component interface{ Close() error }) func(context.Context) error {
	return func(context.Context) error {
		if component == nil {
			return nil
		}
		return component.Close()
	}
}

// beginStopping refuses any later listener handoff and withdraws readiness.
func (s *Server) beginStopping(running bool) {
	s.lifecycleMu.Lock()
	if s.state != nodeClosed {
		s.state = nodeStopping
	}
	s.lifecycleMu.Unlock()
	s.setUnready()
	if running {
		s.components.logger.Info("server shutdown started")
	}
}

// shutdownBudget is the one deadline the shutdown sequence shares. A forced
// stop ends it at once.
func (s *Server) shutdownBudget() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), s.components.settings.shutdownTimeout)
	forced := s.forcedSignal()
	go func() {
		select {
		case <-forced:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// ForceStop demands an immediate stop: a running node begins shutting down,
// and a graceful drain already in progress ends at once by closing the
// connections that remain. Run and Close then report ErrShutdownForced.
// Jobs and WebSocket still close within their own bounds. ForceStop is safe
// to call concurrently and more than once.
func (s *Server) ForceStop() {
	s.forceOnce.Do(func() { close(s.forcedSignal()) })
	s.lifecycleMu.Lock()
	cancel := s.runCancel
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) forcedSignal() chan struct{} {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.forced == nil {
		s.forced = make(chan struct{})
	}
	return s.forced
}

func (s *Server) forceRequested() bool {
	select {
	case <-s.forcedSignal():
		return true
	default:
		return false
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
	if err := listener.Close(); err != nil {
		return fmt.Errorf("close Server-owned listener: %w", err)
	}
	return nil
}

// stopHTTP ends HTTP serving as the plan requires and waits for Serve to
// return. A node that never handed the listener to HTTP has nothing to drain.
func (s *Server) stopHTTP(budget context.Context, plan stopPlan) error {
	s.httpMu.Lock()
	httpServer, exit := s.http, s.serveExit
	s.httpMu.Unlock()
	if httpServer == nil {
		return nil
	}
	var stopErr error
	forced := plan.force
	switch {
	case plan.force:
		closed, cancel := context.WithCancel(context.Background())
		cancel()
		if err := httpServer.Stop(closed); err != nil {
			stopErr = fmt.Errorf("force close HTTP: %w", err)
		}
	case s.httpServing():
		forced, stopErr = s.drainHTTP(budget, httpServer)
	}
	if plan.serveExited {
		return stopErr
	}
	return errors.Join(stopErr, awaitServeExit(budget, exit, forced))
}

// drainHTTP gracefully stops HTTP within the budget. The serving lease keeps
// renewing while HTTP drains; if it fails, serving must stop before the lease
// can expire, so the drain becomes a close. It reports whether HTTP was
// closed rather than fully drained.
func (s *Server) drainHTTP(budget context.Context, httpServer httpRuntime) (bool, error) {
	ctx, cancel := context.WithCancel(budget)
	defer cancel()
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
	err := httpServer.Stop(ctx)
	close(stopWatching)
	<-watching
	select {
	case leaseErr := <-leaseLost:
		return true, errors.Join(fmt.Errorf("maintain serving node lease during HTTP shutdown: %w", leaseErr), err)
	default:
	}
	if err != nil {
		return true, fmt.Errorf("graceful HTTP shutdown: %w", err)
	}
	return false, nil
}

// awaitServeExit waits for Serve to return within the budget, or briefly when
// HTTP was closed. HTTP was stopped by this sequence, so a closed listener or
// server is the expected result of that stop, not a serving failure.
func awaitServeExit(budget context.Context, exit <-chan error, forced bool) error {
	wait := budget
	if forced {
		var cancel context.CancelFunc
		wait, cancel = context.WithTimeout(context.Background(), forcedServeExitWait)
		defer cancel()
	}
	var serveErr error
	select {
	case serveErr = <-exit:
	default:
		select {
		case serveErr = <-exit:
		case <-wait.Done():
			return errHTTPServerStopTimeout
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
	}
	return nil
}
