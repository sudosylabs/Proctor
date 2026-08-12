// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

type runtimeTestApplication struct {
	validated chan model.Principal
}

func (a *runtimeTestApplication) AuthorizeWebSocketSubscription(
	context.Context,
	model.Principal,
	model.RequestMetadata,
	model.Action,
	model.Resource,
) error {
	return nil
}

func (a *runtimeTestApplication) ValidateWebSocketPrincipal(
	_ context.Context,
	principal model.Principal,
) error {
	a.validated <- principal
	return nil
}

type runtimeTestTicker struct {
	ticks chan time.Time
}

func (t *runtimeTestTicker) Chan() <-chan time.Time { return t.ticks }
func (t *runtimeTestTicker) Stop()                  {}

type runtimeTestClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers map[time.Duration]chan *runtimeTestTicker
}

func newRuntimeTestClock(now time.Time) *runtimeTestClock {
	return &runtimeTestClock{
		now: now,
		tickers: map[time.Duration]chan *runtimeTestTicker{
			pingInterval: make(chan *runtimeTestTicker, 1),
			sessionCheck: make(chan *runtimeTestTicker, 1),
		},
	}
}

func (c *runtimeTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *runtimeTestClock) NewTicker(interval time.Duration) runtimeTicker {
	ticker := &runtimeTestTicker{ticks: make(chan time.Time, 1)}
	c.tickers[interval] <- ticker
	return ticker
}

func (c *runtimeTestClock) waitTicker(t *testing.T, interval time.Duration) *runtimeTestTicker {
	t.Helper()
	select {
	case ticker := <-c.tickers[interval]:
		return ticker
	case <-time.After(time.Second):
		t.Fatalf("runtime did not create %s ticker", interval)
		return nil
	}
}

type runtimeTestSocket struct {
	readResults   chan error
	readDeadline  chan time.Time
	writeDeadline chan time.Time
	pingWrites    chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once

	mu          sync.Mutex
	readLimit   int64
	pongHandler func(string) error
}

func newRuntimeTestSocket() *runtimeTestSocket {
	return &runtimeTestSocket{
		readResults:   make(chan error, 1),
		readDeadline:  make(chan time.Time, 4),
		writeDeadline: make(chan time.Time, 4),
		pingWrites:    make(chan struct{}, 1),
		closed:        make(chan struct{}),
	}
}

func (s *runtimeTestSocket) SetReadLimit(limit int64) {
	s.mu.Lock()
	s.readLimit = limit
	s.mu.Unlock()
}

func (s *runtimeTestSocket) SetReadDeadline(deadline time.Time) error {
	s.readDeadline <- deadline
	return nil
}

func (s *runtimeTestSocket) SetPongHandler(handler func(string) error) {
	s.mu.Lock()
	s.pongHandler = handler
	s.mu.Unlock()
}

func (s *runtimeTestSocket) ReadJSON(any) error {
	select {
	case err := <-s.readResults:
		return err
	case <-s.closed:
		return io.EOF
	}
}

func (s *runtimeTestSocket) SetWriteDeadline(deadline time.Time) error {
	s.writeDeadline <- deadline
	return nil
}

func (s *runtimeTestSocket) WriteJSON(any) error { return nil }

func (s *runtimeTestSocket) WriteMessage(messageType int, _ []byte) error {
	if messageType != websocketPingMessage {
		return errors.New("unexpected WebSocket message type")
	}
	s.pingWrites <- struct{}{}
	return nil
}

func (s *runtimeTestSocket) WriteControl(int, []byte, time.Time) error { return nil }

func (s *runtimeTestSocket) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func TestConnectionRuntimeUsesDeterministicLivenessAndValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 10, 30, 0, 0, time.UTC)
	clock := newRuntimeTestClock(now)
	socket := newRuntimeTestSocket()
	application := &runtimeTestApplication{validated: make(chan model.Principal, 1)}
	principal := model.Principal{
		UserID:         model.NewUserID(),
		SessionID:      model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess,
	}
	runtime := &connectionRuntime{
		hub:           &Hub{application: application},
		socket:        socket,
		clock:         clock,
		principal:     principal,
		subscriptions: make(map[string]Subscription),
		send:          make(chan outboundMessage, 1),
	}

	done := make(chan struct{})
	go func() {
		runtime.run(context.Background())
		close(done)
	}()

	select {
	case deadline := <-socket.readDeadline:
		if want := now.Add(pongWait); !deadline.Equal(want) {
			t.Fatalf("initial read deadline = %s, want %s", deadline, want)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not establish initial read deadline")
	}

	pingTicker := clock.waitTicker(t, pingInterval)
	validationTicker := clock.waitTicker(t, sessionCheck)
	pingTicker.ticks <- now.Add(pingInterval)
	select {
	case <-socket.pingWrites:
	case <-time.After(time.Second):
		t.Fatal("ping tick did not write a ping control message")
	}
	select {
	case deadline := <-socket.writeDeadline:
		if want := now.Add(writeWait); !deadline.Equal(want) {
			t.Fatalf("ping write deadline = %s, want %s", deadline, want)
		}
	case <-time.After(time.Second):
		t.Fatal("ping tick did not establish a write deadline")
	}

	validationTicker.ticks <- now.Add(sessionCheck)
	select {
	case got := <-application.validated:
		if got.SessionID != principal.SessionID {
			t.Fatalf("validated session = %s, want %s", got.SessionID, principal.SessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("validation tick did not validate the principal")
	}

	socket.readResults <- io.EOF
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime did not stop after the read pump ended")
	}
}
