// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type shutdownAttemptApplication struct {
	inboundTestApplication
	entered  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func (a *shutdownAttemptApplication) CloseExamAttemptConnection(ctx context.Context, _ invocation.Invocation, _ attempt.CloseConnectionCommand) (attempt.ConnectionClosedResult, error) {
	close(a.entered)
	select {
	case <-a.release:
		return attempt.ConnectionClosedResult{}, nil
	case <-ctx.Done():
		close(a.canceled)
		return attempt.ConnectionClosedResult{}, ctx.Err()
	}
}

func startShutdownAttempt(t *testing.T, timeout time.Duration) (*Hub, *shutdownAttemptApplication, <-chan struct{}) {
	t.Helper()
	application := &shutdownAttemptApplication{entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	hub, err := NewHub(application, replayTestLogger{}, "https://proctor.example", "node-a", nil, timeout)
	require.NoError(t, err)
	err = hub.Start(context.Background())
	require.NoError(t, err)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	socket := newRuntimeTestSocket()
	runtime, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, "")
	require.NotNil(t, runtime, "register failed")
	runtime.attempt = &examAttemptBinding{attemptID: model.NewExamAttemptID(), sittingID: model.NewExamSittingID(), classID: model.NewClassID(), connectionID: model.NewAttemptConnectionID(), participationID: model.NewAttemptParticipationID(), generation: 1}
	finished := make(chan struct{})
	go func() {
		runtime.run(context.Background())
		hub.unregister(runtime)
		close(finished)
	}()
	select {
	case <-socket.readDeadline:
	case <-time.After(time.Second):
		require.FailNow(t, "connection pumps did not start")
	}
	t.Cleanup(func() {
		close(application.release)
		_ = hub.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			assert.Fail(t, "connection did not stop")
		}
	})
	return hub, application, finished
}

func TestHubShutdownWaitsForDurableAttemptFinalization(t *testing.T) {
	t.Parallel()
	hub, application, finished := startShutdownAttempt(t, time.Second)
	closed := make(chan error, 2)
	go func() { closed <- hub.Close() }()
	select {
	case <-application.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "Attempt Connection finalization was not entered")
	}
	go func() { closed <- hub.Close() }()
	select {
	case err := <-closed:
		require.FailNow(t, fmt.Sprintf("Close returned %v before durable finalization", err))
	case <-time.After(20 * time.Millisecond):
	}
	application.release <- struct{}{}
	for range 2 {
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(time.Second):
			require.FailNow(t, "Close did not finish after finalization")
		}
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		require.FailNow(t, "connection did not finish")
	}
}

func TestHubShutdownDeadlineCancelsFinalizationAndRetainsError(t *testing.T) {
	t.Parallel()
	hub, application, _ := startShutdownAttempt(t, 50*time.Millisecond)
	started := time.Now()
	err := hub.Close()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.LessOrEqual(t, time.Since(started), time.Second, "Close exceeded its shared deadline")
	select {
	case <-application.canceled:
	case <-time.After(time.Second):
		require.FailNow(t, "durable finalization did not observe cancellation")
	}
	repeated := hub.Close()
	require.Equal(t, err, repeated, "repeated Close")
}

type shutdownSlowSocket struct {
	*runtimeTestSocket
	mu        sync.Mutex
	deadlines []time.Time
}

func (s *shutdownSlowSocket) WriteControl(_ int, _ []byte, deadline time.Time) error {
	s.mu.Lock()
	s.deadlines = append(s.deadlines, deadline)
	s.mu.Unlock()
	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.closed:
	}
	return context.DeadlineExceeded
}

func TestHubShutdownUsesOneControlWriteBudgetForAllPeers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		hub := newInternalTestHub(t)
		hub.shutdownTimeout = 40 * time.Millisecond
		require.NoError(t, hub.Start(context.Background()))
		var sockets []*shutdownSlowSocket
		for range 4 {
			socket := &shutdownSlowSocket{runtimeTestSocket: newRuntimeTestSocket()}
			sockets = append(sockets, socket)
			principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
			connection, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, "")
			require.NotNil(t, connection, "register failed")
		}
		started := time.Now()
		require.ErrorIs(t, hub.Close(), context.DeadlineExceeded)
		require.Equal(t, hub.shutdownTimeout, time.Since(started))
		var firstDeadline time.Time
		for _, socket := range sockets {
			select {
			case <-socket.closed:
			default:
				require.FailNow(t, "shutdown left a socket open")
			}
			socket.mu.Lock()
			for _, deadline := range socket.deadlines {
				if firstDeadline.IsZero() {
					firstDeadline = deadline
				}
				require.True(t, deadline.Equal(firstDeadline), "close frames received separate deadlines")
			}
			socket.mu.Unlock()
		}
	})
}

func TestHubShutdownPreventsRegisteredConnectionFromStarting(t *testing.T) {
	t.Parallel()
	hub := newInternalTestHub(t)
	require.NoError(t, hub.Start(context.Background()))
	socket := newRuntimeTestSocket()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	connection, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, "")
	require.NotNil(t, connection, "register failed")
	require.NoError(t, hub.Close())
	connection.run(context.Background())
	select {
	case <-socket.readDeadline:
		require.FailNow(t, "connection started after Hub closed")
	default:
	}
}
