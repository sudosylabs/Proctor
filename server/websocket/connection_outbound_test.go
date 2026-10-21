// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

type outboundTestLocalizer struct{}

func (outboundTestLocalizer) Translate(locale, id string, _ any) (string, error) {
	if locale == "fr" {
		switch id {
		case "websocket.error.request.invalid":
			return "Requête WebSocket invalide.", nil
		case "websocket.error.exam_attempt.focus_loss.signal_invalid":
			return "Signal de perte de focus invalide.", nil
		}
	}
	return "", errors.New("translation unavailable")
}

func (outboundTestLocalizer) SupportedLocales() []string { return []string{"en", "fr"} }

func TestConnectionRuntimeLocalizesErrorWithoutChangingCode(t *testing.T) {
	t.Parallel()
	runtime := &connectionRuntime{
		localizer: outboundTestLocalizer{}, locale: "fr",
		send: make(chan outboundMessage, 2),
	}
	runtime.enqueueError(8, "websocket.request.invalid", websocketErrorRequestInvalid)
	runtime.enqueueError(9, "websocket.request.invalid", websocketErrorFocusLossSignalInvalid)

	for _, want := range []struct {
		sequence int64
		message  string
	}{
		{sequence: 8, message: "Requête WebSocket invalide."},
		{sequence: 9, message: "Signal de perte de focus invalide."},
	} {
		t.Run(want.message, func(t *testing.T) {
			response := (<-runtime.send).response
			require.Equal(t, want.sequence, response.Sequence, "localized response = %#v, want sequence %d and message", response, want.sequence)
			require.Equal(t, "websocket.request.invalid", response.Error.Code, "localized response = %#v, want sequence %d and message", response, want.sequence)
			require.Equal(t, want.message, response.Error.Message, "localized response = %#v, want sequence %d and message", response, want.sequence)
		})
	}
}

func TestConnectionRuntimePreservesConcurrentEventSequenceOrder(t *testing.T) {
	t.Parallel()

	const eventCount = sendQueueSize
	runtime := &connectionRuntime{
		clock:         newOutboundTestClock(time.Date(2026, time.August, 12, 20, 0, 0, 0, time.UTC)),
		socket:        newOutboundTestSocket(),
		subscriptions: make(map[string]Subscription),
		replayable:    true,
		send:          make(chan outboundMessage, eventCount),
	}

	start := make(chan struct{})
	var publishers sync.WaitGroup
	publishers.Add(eventCount)
	for index := range eventCount {
		go func() {
			defer publishers.Done()
			<-start
			runtime.enqueueEvent(&Event{
				ID:     model.NewID(),
				Event:  "exam.updated",
				UserID: model.NewID(),
				Data:   []byte(`{"index":` + string(rune('0'+index%10)) + `}`),
			})
		}()
	}
	close(start)
	publishers.Wait()

	for want := int64(1); want <= eventCount; want++ {
		message := <-runtime.send
		require.NotNil(t, message.event, "outbound message %d is not an event", want)
		require.Equal(t, want, message.event.Sequence, "outbound sequence")
	}
}

func TestConnectionRuntimeClosesOnOutboundQueueSaturation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enqueue func(*connectionRuntime)
	}{
		{
			name: "event",
			enqueue: func(runtime *connectionRuntime) {
				runtime.enqueueEvent(&Event{
					ID:     model.NewID(),
					Event:  "exam.updated",
					UserID: model.NewID(),
				})
			},
		},
		{
			name: "response",
			enqueue: func(runtime *connectionRuntime) {
				runtime.enqueueResponse(7, json.RawMessage(`{"accepted":true}`))
			},
		},
		{
			name: "error",
			enqueue: func(runtime *connectionRuntime) {
				runtime.enqueueError(8, "websocket.request.invalid", websocketErrorRequestInvalid)
				runtime.enqueueError(9, "websocket.request.invalid", websocketErrorFocusLossSignalInvalid)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			socket := newOutboundTestSocket()
			runtime := newOutboundTestRuntime(socket, 1)
			runtime.send <- outboundMessage{response: &Response{Status: "ok", Sequence: 1}}

			test.enqueue(runtime)

			control := socket.singleControl(t)
			require.Equal(t, websocketCloseMessage, control.messageType, "control message type")
			require.Equal(t, CloseBackpressure, int(binary.BigEndian.Uint16(control.data[:2])), "close code")
			require.Equal(t, "client is too slow", string(control.data[2:]), "close reason")
			require.Equal(t, 1, socket.closeCount(), "socket close calls")
			require.False(t, runtime.replayable, "backpressure closure must not be replayable")
			require.Len(t, runtime.send, 1, "queue length")
		})
	}
}

func TestConnectionRuntimeWritesOutboundMessagesInQueueOrder(t *testing.T) {
	t.Parallel()

	socket := newOutboundTestSocket()
	runtime := newOutboundTestRuntime(socket, 3)
	runtime.enqueueEvent(&Event{
		ID:     model.NewID(),
		Event:  "exam.updated",
		UserID: model.NewID(),
	})
	runtime.enqueueResponse(7, json.RawMessage(`{"accepted":true}`))
	runtime.enqueueError(8, "websocket.request.invalid", websocketErrorRequestInvalid)
	runtime.enqueueError(9, "websocket.request.invalid", websocketErrorFocusLossSignalInvalid)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.writePump(ctx)
		close(done)
	}()

	for range 3 {
		socket.waitWrite(t)
	}
	cancel()
	waitDone(t, done, "write pump did not stop after cancellation")

	writes := socket.jsonWrites()
	require.Len(t, writes, 3, "JSON writes")
	event, ok := writes[0].(*Event)
	require.True(t, ok, "first write")
	require.Equal(t, int64(1), event.Sequence, "first write")
	require.Equal(t, "exam.updated", event.Event, "first write")
	response, ok := writes[1].(*Response)
	require.True(t, ok, "second write")
	require.Equal(t, "ok", response.Status, "second write")
	require.Equal(t, int64(7), response.Sequence, "second write")
	require.Equal(t, `{"accepted":true}`, string(response.Data), "second write")
	failure, ok := writes[2].(*Response)
	require.True(t, ok, "third write")
	require.Equal(t, "error", failure.Status, "third write")
	require.Equal(t, int64(8), failure.Sequence, "third write")
	require.NotNil(t, failure.Error, "third write")
	require.Equal(t, "websocket.request.invalid", failure.Error.Code, "third write")
	require.Equal(t, "Invalid WebSocket request.", failure.Error.Message, "third write")
	for index, deadline := range socket.deadlines() {
		require.True(t, deadline.Equal(runtime.clock.Now().Add(writeWait)), "write deadline %d = %s", index, deadline)
	}
}

func TestConnectionRuntimeEnqueuesHelloAndResyncInSequence(t *testing.T) {
	t.Parallel()

	runtime := newOutboundTestRuntime(newOutboundTestSocket(), 2)
	runtime.id = model.NewID()
	runtime.nodeID = "node-a"
	runtime.principal = model.Principal{UserID: model.NewUserID()}

	runtime.enqueueHello(false, true)

	helloEvent := (<-runtime.send).event
	resyncEvent := (<-runtime.send).event
	require.NotNil(t, helloEvent)
	require.Equal(t, string(EventHello), helloEvent.Event)
	require.Equal(t, int64(1), helloEvent.Sequence)
	var hello Hello
	require.NoError(t, json.Unmarshal(helloEvent.Data, &hello))
	require.Equal(t, runtime.id, hello.ConnectionID)
	require.Equal(t, "node-a", hello.NodeID)
	require.False(t, hello.Resumed)
	require.NotNil(t, resyncEvent)
	require.Equal(t, string(EventResync), resyncEvent.Event)
	require.Equal(t, int64(2), resyncEvent.Sequence)
}

func TestConnectionRuntimeSchedulesPingWithWriteDeadline(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 20, 0, 0, 0, time.UTC)
	clock := newOutboundTestClock(now)
	socket := newOutboundTestSocket()
	runtime := newOutboundTestRuntime(socket, 1)
	runtime.clock = clock

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.writePump(ctx)
		close(done)
	}()

	ticker := clock.waitTicker(t)
	ticker.ticks <- now.Add(pingInterval)
	socket.waitPing(t)
	cancel()
	waitDone(t, done, "write pump did not stop after ping")

	require.Equal(t, 1, socket.pingCount(), "ping writes")
	deadlines := socket.deadlines()
	require.Len(t, deadlines, 1, "ping deadlines")
	require.True(t, deadlines[0].Equal(now.Add(writeWait)), "ping deadlines")
}

func TestConnectionRuntimeStopsAfterWriteFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*connectionRuntime, *outboundTestClock, *outboundTestSocket, *testing.T)
	}{
		{
			name: "JSON write",
			prepare: func(runtime *connectionRuntime, _ *outboundTestClock, socket *outboundTestSocket, _ *testing.T) {
				socket.jsonErr = errors.New("write failed")
				runtime.enqueueResponse(1, nil)
			},
		},
		{
			name: "ping write",
			prepare: func(_ *connectionRuntime, clock *outboundTestClock, socket *outboundTestSocket, t *testing.T) {
				socket.pingErr = errors.New("ping failed")
				clock.waitTicker(t).ticks <- clock.Now().Add(pingInterval)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			clock := newOutboundTestClock(time.Date(2026, time.August, 12, 20, 0, 0, 0, time.UTC))
			socket := newOutboundTestSocket()
			runtime := newOutboundTestRuntime(socket, 1)
			runtime.clock = clock
			done := make(chan struct{})
			go func() {
				runtime.writePump(context.Background())
				close(done)
			}()

			test.prepare(runtime, clock, socket, t)
			waitDone(t, done, "write pump did not stop after write failure")
			require.Equal(t, 1, socket.closeCount(), "socket close calls")
		})
	}
}

func TestConnectionRuntimeWriteFailureAndExplicitCloseShareOneTerminalPath(t *testing.T) {
	t.Parallel()

	socket := newOutboundTestSocket()
	socket.jsonErr = errors.New("write failed")
	runtime := newOutboundTestRuntime(socket, 1)
	runtime.enqueueResponse(1, nil)
	done := make(chan struct{})
	go func() {
		runtime.writePump(context.Background())
		close(done)
	}()
	waitDone(t, done, "write pump did not stop after write failure")

	runtime.close(CloseServer, "connection closed", false)

	require.Equal(t, 1, socket.closeCount(), "socket close calls")
	require.Empty(t, socket.controls(), "terminal close controls after transport failure")
	runtime.mu.Lock()
	replayable := runtime.replayable
	runtime.mu.Unlock()
	require.False(t, replayable, "explicit close did not revoke replay after transport failure")
}

func TestConnectionRuntimeCloseIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	socket := newOutboundTestSocket()
	runtime := newOutboundTestRuntime(socket, 1)
	start := make(chan struct{})
	var closers sync.WaitGroup
	for index := range 64 {
		closers.Add(1)
		go func() {
			defer closers.Done()
			<-start
			runtime.close(CloseServer+index%4, "concurrent close", index%2 == 0)
		}()
	}
	close(start)
	closers.Wait()

	require.Len(t, socket.controls(), 1, "terminal close controls")
	require.Equal(t, 1, socket.closeCount(), "socket close calls")
}

func TestConnectionRuntimeBackpressureDoesNotAffectHealthyConnection(t *testing.T) {
	t.Parallel()

	slowSocket := newOutboundTestSocket()
	slow := newOutboundTestRuntime(slowSocket, 1)
	slow.send <- outboundMessage{response: &Response{Status: "ok", Sequence: 1}}
	healthySocket := newOutboundTestSocket()
	healthy := newOutboundTestRuntime(healthySocket, 1)
	event := &Event{ID: model.NewID(), Event: "exam.updated", UserID: model.NewID()}

	slow.enqueueEvent(event)
	healthy.enqueueEvent(event)

	require.Equal(t, 1, slowSocket.closeCount(), "slow socket close calls")
	select {
	case message := <-healthy.send:
		require.NotNil(t, message.event)
		require.Equal(t, event.ID, message.event.ID)
		require.Equal(t, int64(1), message.event.Sequence)
	default:
		require.FailNow(t, "healthy connection did not receive the event")
	}
	require.Equal(t, 0, healthySocket.closeCount(), "healthy socket close calls")
}

func newOutboundTestRuntime(socket *outboundTestSocket, queueSize int) *connectionRuntime {
	return &connectionRuntime{
		clock:         newOutboundTestClock(time.Date(2026, time.August, 12, 20, 0, 0, 0, time.UTC)),
		socket:        socket,
		subscriptions: make(map[string]Subscription),
		replayable:    true,
		send:          make(chan outboundMessage, queueSize),
	}
}

func waitDone(t *testing.T, done <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, failure)
	}
}

type outboundTestClock struct {
	now     time.Time
	created chan *outboundTestTicker
}

func newOutboundTestClock(now time.Time) *outboundTestClock {
	return &outboundTestClock{now: now, created: make(chan *outboundTestTicker, 1)}
}

func (c *outboundTestClock) Now() time.Time { return c.now }

func (c *outboundTestClock) NewTicker(time.Duration) runtimeTicker {
	ticker := &outboundTestTicker{ticks: make(chan time.Time, 1)}
	c.created <- ticker
	return ticker
}

func (c *outboundTestClock) waitTicker(t *testing.T) *outboundTestTicker {
	t.Helper()
	select {
	case ticker := <-c.created:
		return ticker
	case <-time.After(time.Second):
		require.FailNow(t, "write pump did not create its ticker")
		return nil
	}
}

type outboundTestTicker struct {
	ticks chan time.Time
}

func (t *outboundTestTicker) Chan() <-chan time.Time { return t.ticks }
func (t *outboundTestTicker) Stop()                  {}

type outboundTestControl struct {
	messageType int
	data        []byte
	deadline    time.Time
}

type outboundTestSocket struct {
	writeSignal chan struct{}
	pingSignal  chan struct{}
	closed      chan struct{}
	once        sync.Once

	mu             sync.Mutex
	writes         []any
	writeDeadlines []time.Time
	pings          int
	controlWrites  []outboundTestControl
	closeCalls     int
	jsonErr        error
	pingErr        error
}

func newOutboundTestSocket() *outboundTestSocket {
	return &outboundTestSocket{
		writeSignal: make(chan struct{}, sendQueueSize),
		pingSignal:  make(chan struct{}, 1),
		closed:      make(chan struct{}),
	}
}

func (*outboundTestSocket) SetReadLimit(int64)                {}
func (*outboundTestSocket) SetReadDeadline(time.Time) error   { return nil }
func (*outboundTestSocket) SetPongHandler(func(string) error) {}
func (*outboundTestSocket) ReadJSON(any) error                { return context.Canceled }

func (s *outboundTestSocket) SetWriteDeadline(deadline time.Time) error {
	s.mu.Lock()
	s.writeDeadlines = append(s.writeDeadlines, deadline)
	s.mu.Unlock()
	return nil
}

func (s *outboundTestSocket) WriteJSON(value any) error {
	s.mu.Lock()
	s.writes = append(s.writes, value)
	err := s.jsonErr
	s.mu.Unlock()
	s.writeSignal <- struct{}{}
	return err
}

func (s *outboundTestSocket) WriteMessage(messageType int, _ []byte) error {
	if messageType != websocketPingMessage {
		return errors.New("unexpected message type")
	}
	s.mu.Lock()
	s.pings++
	err := s.pingErr
	s.mu.Unlock()
	s.pingSignal <- struct{}{}
	return err
}

func (s *outboundTestSocket) WriteControl(messageType int, data []byte, deadline time.Time) error {
	s.mu.Lock()
	s.controlWrites = append(s.controlWrites, outboundTestControl{
		messageType: messageType,
		data:        append([]byte(nil), data...),
		deadline:    deadline,
	})
	s.mu.Unlock()
	return nil
}

func (s *outboundTestSocket) Close() error {
	s.mu.Lock()
	s.closeCalls++
	s.mu.Unlock()
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *outboundTestSocket) waitWrite(t *testing.T) {
	t.Helper()
	select {
	case <-s.writeSignal:
	case <-time.After(time.Second):
		require.FailNow(t, "runtime did not write queued JSON")
	}
}

func (s *outboundTestSocket) waitPing(t *testing.T) {
	t.Helper()
	select {
	case <-s.pingSignal:
	case <-time.After(time.Second):
		require.FailNow(t, "runtime did not write scheduled ping")
	}
}

func (s *outboundTestSocket) singleControl(t *testing.T) outboundTestControl {
	t.Helper()
	controls := s.controls()
	require.Len(t, controls, 1, "control writes")
	require.GreaterOrEqual(t, len(controls[0].data), 2, "close control payload")
	return controls[0]
}

func (s *outboundTestSocket) jsonWrites() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.writes...)
}

func (s *outboundTestSocket) deadlines() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.writeDeadlines...)
}

func (s *outboundTestSocket) controls() []outboundTestControl {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outboundTestControl(nil), s.controlWrites...)
}

func (s *outboundTestSocket) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
}

func (s *outboundTestSocket) pingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pings
}
