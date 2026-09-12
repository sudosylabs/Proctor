// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type shutdownAttemptApplication struct {
	inboundTestApplication
	entered  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func (a *shutdownAttemptApplication) CloseExamAttemptConnection(ctx context.Context, _ app.Invocation, _ app.CloseExamAttemptConnectionCommand) (app.ExamAttemptConnectionClosed, error) {
	close(a.entered)
	select {
	case <-a.release:
		return app.ExamAttemptConnectionClosed{}, nil
	case <-ctx.Done():
		close(a.canceled)
		return app.ExamAttemptConnectionClosed{}, ctx.Err()
	}
}

func startShutdownAttempt(t *testing.T, timeout time.Duration) (*Hub, *shutdownAttemptApplication, <-chan struct{}) {
	t.Helper()
	application := &shutdownAttemptApplication{entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	hub, err := NewHub(application, replayTestLogger{}, "https://proctor.example", "node-a", nil, timeout)
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	socket := newRuntimeTestSocket()
	runtime, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, "")
	if runtime == nil {
		t.Fatal("register failed")
	}
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
		t.Fatal("connection pumps did not start")
	}
	t.Cleanup(func() {
		close(application.release)
		_ = hub.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("connection did not stop")
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
		t.Fatal("Attempt Connection finalization was not entered")
	}
	go func() { closed <- hub.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v before durable finalization", err)
	case <-time.After(20 * time.Millisecond):
	}
	application.release <- struct{}{}
	for range 2 {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after finalization")
		}
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("connection did not finish")
	}
}

func TestHubShutdownDeadlineCancelsFinalizationAndRetainsError(t *testing.T) {
	t.Parallel()
	hub, application, _ := startShutdownAttempt(t, 50*time.Millisecond)
	started := time.Now()
	err := hub.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("Close exceeded its shared deadline")
	}
	select {
	case <-application.canceled:
	case <-time.After(time.Second):
		t.Fatal("durable finalization did not observe cancellation")
	}
	if repeated := hub.Close(); repeated != err {
		t.Fatalf("repeated Close = %v, want retained %v", repeated, err)
	}
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
		if err := hub.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		var sockets []*shutdownSlowSocket
		for range 4 {
			socket := &shutdownSlowSocket{runtimeTestSocket: newRuntimeTestSocket()}
			sockets = append(sockets, socket)
			principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
			if connection, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, ""); connection == nil {
				t.Fatal("register failed")
			}
		}
		started := time.Now()
		if err := hub.Close(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close error = %v", err)
		}
		if elapsed := time.Since(started); elapsed != hub.shutdownTimeout {
			t.Fatalf("serial peers extended the drain budget: %s", elapsed)
		}
		var firstDeadline time.Time
		for _, socket := range sockets {
			select {
			case <-socket.closed:
			default:
				t.Fatal("shutdown left a socket open")
			}
			socket.mu.Lock()
			for _, deadline := range socket.deadlines {
				if firstDeadline.IsZero() {
					firstDeadline = deadline
				}
				if !deadline.Equal(firstDeadline) {
					t.Fatal("close frames received separate deadlines")
				}
			}
			socket.mu.Unlock()
		}
	})
}

func TestHubShutdownPreventsRegisteredConnectionFromStarting(t *testing.T) {
	t.Parallel()
	hub := newInternalTestHub(t)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	socket := newRuntimeTestSocket()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	connection, _ := hub.register(socket, principal, model.RequestMetadata{}, "", 0, "")
	if connection == nil {
		t.Fatal("register failed")
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	connection.run(context.Background())
	select {
	case <-socket.readDeadline:
		t.Fatal("connection started after Hub closed")
	default:
	}
}
