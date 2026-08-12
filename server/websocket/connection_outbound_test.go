// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

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
				Id:     model.NewId(),
				Event:  "exam.updated",
				UserID: model.NewId(),
				Data:   []byte(`{"index":` + string(rune('0'+index%10)) + `}`),
			})
		}()
	}
	close(start)
	publishers.Wait()

	for want := int64(1); want <= eventCount; want++ {
		message := <-runtime.send
		if message.event == nil {
			t.Fatalf("outbound message %d is not an event", want)
		}
		if got := message.event.Sequence; got != want {
			t.Fatalf("outbound sequence = %d, want %d", got, want)
		}
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
					Id:     model.NewId(),
					Event:  "exam.updated",
					UserID: model.NewId(),
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
				runtime.enqueueError(8, "websocket.request.invalid", "Invalid WebSocket request.")
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
			if control.messageType != websocketCloseMessage {
				t.Fatalf("control message type = %d, want %d", control.messageType, websocketCloseMessage)
			}
			if got := int(binary.BigEndian.Uint16(control.data[:2])); got != CloseBackpressure {
				t.Fatalf("close code = %d, want %d", got, CloseBackpressure)
			}
			if got := string(control.data[2:]); got != "client is too slow" {
				t.Fatalf("close reason = %q, want %q", got, "client is too slow")
			}
			if got := socket.closeCount(); got != 1 {
				t.Fatalf("socket close calls = %d, want 1", got)
			}
			if runtime.replayable {
				t.Fatal("backpressure closure must not be replayable")
			}
			if got := len(runtime.send); got != 1 {
				t.Fatalf("queue length = %d, want the existing message retained", got)
			}
		})
	}
}

func TestConnectionRuntimeWritesOutboundMessagesInQueueOrder(t *testing.T) {
	t.Parallel()

	socket := newOutboundTestSocket()
	runtime := newOutboundTestRuntime(socket, 3)
	runtime.enqueueEvent(&Event{
		Id:     model.NewId(),
		Event:  "exam.updated",
		UserID: model.NewId(),
	})
	runtime.enqueueResponse(7, json.RawMessage(`{"accepted":true}`))
	runtime.enqueueError(8, "websocket.request.invalid", "Invalid WebSocket request.")

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
	if len(writes) != 3 {
		t.Fatalf("JSON writes = %d, want 3", len(writes))
	}
	event, ok := writes[0].(*Event)
	if !ok || event.Sequence != 1 || event.Event != "exam.updated" {
		t.Fatalf("first write = %#v, want sequenced exam event", writes[0])
	}
	response, ok := writes[1].(*Response)
	if !ok || response.Status != "ok" || response.Sequence != 7 || string(response.Data) != `{"accepted":true}` {
		t.Fatalf("second write = %#v, want successful response", writes[1])
	}
	failure, ok := writes[2].(*Response)
	if !ok || failure.Status != "error" || failure.Sequence != 8 || failure.Error == nil ||
		failure.Error.Code != "websocket.request.invalid" || failure.Error.Message != "Invalid WebSocket request." {
		t.Fatalf("third write = %#v, want protocol error", writes[2])
	}
	for index, deadline := range socket.deadlines() {
		if want := runtime.clock.Now().Add(writeWait); !deadline.Equal(want) {
			t.Fatalf("write deadline %d = %s, want %s", index, deadline, want)
		}
	}
}

func TestConnectionRuntimeEnqueuesHelloAndResyncInSequence(t *testing.T) {
	t.Parallel()

	runtime := newOutboundTestRuntime(newOutboundTestSocket(), 2)
	runtime.id = model.NewId()
	runtime.hub = &Hub{nodeID: "node-a"}
	runtime.principal = model.Principal{UserID: model.NewUserID()}

	runtime.enqueueHello(false, true)

	helloEvent := (<-runtime.send).event
	resyncEvent := (<-runtime.send).event
	if helloEvent == nil || helloEvent.Event != string(EventHello) || helloEvent.Sequence != 1 {
		t.Fatalf("hello event = %#v", helloEvent)
	}
	var hello Hello
	if err := json.Unmarshal(helloEvent.Data, &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if hello.ConnectionId != runtime.id || hello.NodeId != "node-a" || hello.Resumed {
		t.Fatalf("hello = %#v", hello)
	}
	if resyncEvent == nil || resyncEvent.Event != string(EventResync) || resyncEvent.Sequence != 2 {
		t.Fatalf("resync event = %#v", resyncEvent)
	}
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

	if got := socket.pingCount(); got != 1 {
		t.Fatalf("ping writes = %d, want 1", got)
	}
	deadlines := socket.deadlines()
	if len(deadlines) != 1 || !deadlines[0].Equal(now.Add(writeWait)) {
		t.Fatalf("ping deadlines = %v, want [%s]", deadlines, now.Add(writeWait))
	}
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
			if got := socket.closeCount(); got != 1 {
				t.Fatalf("socket close calls = %d, want 1", got)
			}
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

	if got := socket.closeCount(); got != 1 {
		t.Fatalf("socket close calls = %d, want 1", got)
	}
	if got := len(socket.controls()); got != 0 {
		t.Fatalf("terminal close controls after transport failure = %d, want 0", got)
	}
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

	if got := len(socket.controls()); got != 1 {
		t.Fatalf("terminal close controls = %d, want 1", got)
	}
	if got := socket.closeCount(); got != 1 {
		t.Fatalf("socket close calls = %d, want 1", got)
	}
}

func TestConnectionRuntimeBackpressureDoesNotAffectHealthyConnection(t *testing.T) {
	t.Parallel()

	slowSocket := newOutboundTestSocket()
	slow := newOutboundTestRuntime(slowSocket, 1)
	slow.send <- outboundMessage{response: &Response{Status: "ok", Sequence: 1}}
	healthySocket := newOutboundTestSocket()
	healthy := newOutboundTestRuntime(healthySocket, 1)
	event := &Event{Id: model.NewId(), Event: "exam.updated", UserID: model.NewId()}

	slow.enqueueEvent(event)
	healthy.enqueueEvent(event)

	if got := slowSocket.closeCount(); got != 1 {
		t.Fatalf("slow socket close calls = %d, want 1", got)
	}
	select {
	case message := <-healthy.send:
		if message.event == nil || message.event.Id != event.Id || message.event.Sequence != 1 {
			t.Fatalf("healthy delivery = %#v", message.event)
		}
	default:
		t.Fatal("healthy connection did not receive the event")
	}
	if got := healthySocket.closeCount(); got != 0 {
		t.Fatalf("healthy socket close calls = %d, want 0", got)
	}
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
		t.Fatal(failure)
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
		t.Fatal("write pump did not create its ticker")
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
		t.Fatal("runtime did not write queued JSON")
	}
}

func (s *outboundTestSocket) waitPing(t *testing.T) {
	t.Helper()
	select {
	case <-s.pingSignal:
	case <-time.After(time.Second):
		t.Fatal("runtime did not write scheduled ping")
	}
}

func (s *outboundTestSocket) singleControl(t *testing.T) outboundTestControl {
	t.Helper()
	controls := s.controls()
	if len(controls) != 1 {
		t.Fatalf("control writes = %d, want 1", len(controls))
	}
	if len(controls[0].data) < 2 {
		t.Fatalf("close control payload = %v, want code and reason", controls[0].data)
	}
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
