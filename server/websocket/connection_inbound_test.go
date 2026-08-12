// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type inboundAuthorizationCall struct {
	principal model.Principal
	metadata  model.RequestMetadata
	action    model.Action
	resource  model.Resource
}

type inboundTestApplication struct {
	mu             sync.Mutex
	authorizeErr   error
	validationErr  error
	authorizations []inboundAuthorizationCall
	validations    []model.Principal
}

func (a *inboundTestApplication) AuthorizeWebSocketSubscription(
	_ context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorizations = append(a.authorizations, inboundAuthorizationCall{
		principal: principal,
		metadata:  metadata,
		action:    action,
		resource:  resource,
	})
	return a.authorizeErr
}

func (a *inboundTestApplication) ValidateWebSocketPrincipal(
	_ context.Context,
	principal model.Principal,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.validations = append(a.validations, principal)
	return a.validationErr
}

type inboundReadResult struct {
	request Request
	err     error
}

type inboundTestSocket struct {
	reads         chan inboundReadResult
	readDeadlines chan time.Time
	closed        chan struct{}
	closeOnce     sync.Once

	mu          sync.Mutex
	readLimit   int64
	pongHandler func(string) error
	closeCode   int
	closeReason string
}

func newInboundTestSocket() *inboundTestSocket {
	return &inboundTestSocket{
		reads:         make(chan inboundReadResult, 8),
		readDeadlines: make(chan time.Time, 8),
		closed:        make(chan struct{}),
	}
}

func (s *inboundTestSocket) SetReadLimit(limit int64) {
	s.mu.Lock()
	s.readLimit = limit
	s.mu.Unlock()
}

func (s *inboundTestSocket) SetReadDeadline(deadline time.Time) error {
	s.readDeadlines <- deadline
	return nil
}

func (s *inboundTestSocket) SetPongHandler(handler func(string) error) {
	s.mu.Lock()
	s.pongHandler = handler
	s.mu.Unlock()
}

func (s *inboundTestSocket) ReadJSON(value any) error {
	select {
	case result := <-s.reads:
		if result.err == nil {
			request, ok := value.(*Request)
			if !ok {
				return errors.New("unexpected WebSocket read target")
			}
			*request = result.request
		}
		return result.err
	case <-s.closed:
		return io.EOF
	}
}

func (*inboundTestSocket) SetWriteDeadline(time.Time) error { return nil }
func (*inboundTestSocket) WriteJSON(any) error              { return nil }
func (*inboundTestSocket) WriteMessage(int, []byte) error   { return nil }

func (s *inboundTestSocket) WriteControl(_ int, data []byte, _ time.Time) error {
	if len(data) >= 2 {
		s.mu.Lock()
		s.closeCode = int(data[0])<<8 | int(data[1])
		s.closeReason = string(data[2:])
		s.mu.Unlock()
	}
	return nil
}

func (s *inboundTestSocket) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func newInboundRuntime(
	application Application,
	socket connectionSocket,
	clock runtimeClock,
) *connectionRuntime {
	return &connectionRuntime{
		application: application,
		socket:      socket,
		clock:       clock,
		principal: model.Principal{
			UserID:       model.NewUserID(),
			SessionID:    model.NewSessionID(),
			CredentialID: model.PrincipalCredentialID(model.NewId()),
		},
		metadata:      model.RequestMetadata{RequestID: "upgrade-request"},
		id:            model.NewId(),
		subscriptions: make(map[string]Subscription),
		replayable:    true,
		send:          make(chan outboundMessage, maximumSubscriptions+8),
	}
}

func validInboundSubscription() Subscription {
	return Subscription{
		Action: model.ActionClassView,
		Resource: Resource{
			Type: model.ResourceClass,
			ID:   model.NewId(),
		},
	}
}

func requestWithData(t *testing.T, sequence int64, action string, value any) *Request {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal request data: %v", err)
	}
	return &Request{Sequence: sequence, Action: action, Data: data}
}

func nextInboundResponse(t *testing.T, runtime *connectionRuntime) *Response {
	t.Helper()
	select {
	case message := <-runtime.send:
		if message.response == nil {
			t.Fatal("runtime enqueued an event, want response")
		}
		return message.response
	case <-time.After(time.Second):
		t.Fatal("runtime did not enqueue a response")
		return nil
	}
}

func TestConnectionRuntimeReadPumpRejectsInvalidRequestAndContinues(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 17, 0, 0, 0, time.UTC)
	clock := newRuntimeTestClock(now)
	socket := newInboundTestSocket()
	runtime := newInboundRuntime(&inboundTestApplication{}, socket, clock)
	socket.reads <- inboundReadResult{request: Request{Action: "ping"}}
	socket.reads <- inboundReadResult{request: Request{Sequence: 2, Action: "ping"}}
	socket.reads <- inboundReadResult{err: io.EOF}

	runtime.readPump(context.Background())

	invalid := nextInboundResponse(t, runtime)
	if invalid.Status != "error" || invalid.Sequence != 0 ||
		invalid.Error == nil || invalid.Error.Code != "websocket.request.invalid" ||
		invalid.Error.Message != "Invalid WebSocket request." {
		t.Fatalf("invalid request response = %#v", invalid)
	}
	pong := nextInboundResponse(t, runtime)
	if pong.Status != "ok" || pong.Sequence != 2 || string(pong.Data) != `{"pong":true}` {
		t.Fatalf("ping response = %#v", pong)
	}
}

func TestConnectionRuntimeParentCancellationStopsBlockedRead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 17, 30, 0, 0, time.UTC)
	clock := newRuntimeTestClock(now)
	socket := newInboundTestSocket()
	runtime := newInboundRuntime(&inboundTestApplication{}, socket, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runtime.run(ctx)
		close(done)
	}()

	select {
	case <-socket.readDeadlines:
	case <-time.After(time.Second):
		t.Fatal("runtime did not begin its read responsibility")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("runtime did not stop a blocked read after parent cancellation")
	}
}

func TestConnectionRuntimePongExtendsReadDeadline(t *testing.T) {
	t.Parallel()

	initial := time.Date(2026, time.August, 12, 18, 0, 0, 0, time.UTC)
	clock := newRuntimeTestClock(initial)
	socket := newInboundTestSocket()
	runtime := newInboundRuntime(&inboundTestApplication{}, socket, clock)
	done := make(chan struct{})
	go func() {
		runtime.readPump(context.Background())
		close(done)
	}()

	if deadline := <-socket.readDeadlines; !deadline.Equal(initial.Add(pongWait)) {
		t.Fatalf("initial read deadline = %s, want %s", deadline, initial.Add(pongWait))
	}
	socket.mu.Lock()
	if socket.readLimit != MaxMessageBytes {
		socket.mu.Unlock()
		t.Fatalf("read limit = %d, want %d", socket.readLimit, MaxMessageBytes)
	}
	pongHandler := socket.pongHandler
	socket.mu.Unlock()
	if pongHandler == nil {
		t.Fatal("runtime did not install a pong handler")
	}

	afterPong := initial.Add(17 * time.Second)
	clock.mu.Lock()
	clock.now = afterPong
	clock.mu.Unlock()
	if err := pongHandler("client payload"); err != nil {
		t.Fatalf("pong handler returned %v", err)
	}
	if deadline := <-socket.readDeadlines; !deadline.Equal(afterPong.Add(pongWait)) {
		t.Fatalf("pong read deadline = %s, want %s", deadline, afterPong.Add(pongWait))
	}

	_ = socket.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read responsibility did not stop after socket closure")
	}
}

func TestConnectionRuntimeOwnsAuthorizedSubscriptionSet(t *testing.T) {
	t.Parallel()

	application := &inboundTestApplication{}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.metadata = model.RequestMetadata{
		RequestID: "upgrade-request",
		IPAddress: "192.0.2.10",
		UserAgent: "proctor-test",
	}
	subscription := validInboundSubscription()

	runtime.handleRequest(context.Background(), requestWithData(t, 7, "subscribe", subscription))
	response := nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Sequence != 7 || response.Error != nil {
		t.Fatalf("subscribe response = %#v", response)
	}
	if !runtime.hasSubscription(subscription) {
		t.Fatal("authorized subscription is not visible to publication")
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 8, "subscribe", subscription))
	response = nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Sequence != 8 {
		t.Fatalf("duplicate subscribe response = %#v", response)
	}
	runtime.mu.Lock()
	if got := len(runtime.subscriptions); got != 1 {
		runtime.mu.Unlock()
		t.Fatalf("subscription count after duplicate = %d, want 1", got)
	}
	for len(runtime.subscriptions) < maximumSubscriptions {
		candidate := validInboundSubscription()
		runtime.subscriptions[candidate.Key()] = candidate
	}
	runtime.mu.Unlock()

	overLimit := validInboundSubscription()
	runtime.handleRequest(context.Background(), requestWithData(t, 9, "subscribe", overLimit))
	response = nextInboundResponse(t, runtime)
	if response.Status != "error" || response.Sequence != 9 || response.Error == nil ||
		response.Error.Code != "websocket.subscription.limit" ||
		response.Error.Message != "WebSocket subscription limit reached." {
		t.Fatalf("subscription-limit response = %#v", response)
	}
	if runtime.hasSubscription(overLimit) {
		t.Fatal("over-limit subscription was retained")
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 10, "subscribe", subscription))
	response = nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Sequence != 10 {
		t.Fatalf("duplicate at limit response = %#v", response)
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 11, "unsubscribe", subscription))
	response = nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Sequence != 11 || runtime.hasSubscription(subscription) {
		t.Fatalf("unsubscribe response = %#v, still subscribed = %t", response, runtime.hasSubscription(subscription))
	}

	application.mu.Lock()
	calls := append([]inboundAuthorizationCall(nil), application.authorizations...)
	application.mu.Unlock()
	if len(calls) != 4 {
		t.Fatalf("authorization calls = %d, want 4", len(calls))
	}
	first := calls[0]
	if first.principal.UserID != runtime.principal.UserID ||
		first.metadata.RequestID != runtime.id+":7" ||
		first.metadata.IPAddress != runtime.metadata.IPAddress ||
		first.metadata.UserAgent != runtime.metadata.UserAgent ||
		first.action != subscription.Action ||
		first.resource != subscription.Resource.model() {
		t.Fatalf("authorization call = %#v", first)
	}
}

func TestConnectionRuntimePreservesInboundErrorContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		request     func(*testing.T) *Request
		authorize   error
		wantCode    string
		wantMessage string
	}{
		{
			name: "invalid subscription",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 21, "subscribe", Subscription{})
			},
			wantCode:    "websocket.subscription.invalid",
			wantMessage: "Invalid subscription.",
		},
		{
			name: "invalid unsubscribe",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 22, "unsubscribe", Subscription{})
			},
			wantCode:    "websocket.subscription.invalid",
			wantMessage: "Invalid subscription.",
		},
		{
			name: "authorization denial",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 23, "subscribe", validInboundSubscription())
			},
			authorize:   errors.New("policy lookup denied"),
			wantCode:    "authorization.denied",
			wantMessage: "WebSocket subscription denied.",
		},
		{
			name: "typed authorization denial",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 24, "subscribe", validInboundSubscription())
			},
			authorize:   app.NewError("authorization.denied"),
			wantCode:    "authorization.denied",
			wantMessage: "WebSocket subscription denied.",
		},
		{
			name: "authorization failure",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 25, "subscribe", validInboundSubscription())
			},
			authorize:   app.NewError("authorization.unavailable"),
			wantCode:    "authorization.unavailable",
			wantMessage: "WebSocket subscription failed.",
		},
		{
			name: "unknown action",
			request: func(*testing.T) *Request {
				return &Request{Sequence: 26, Action: "future-command"}
			},
			wantCode:    "websocket.action.unknown",
			wantMessage: "Unknown WebSocket action.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &inboundTestApplication{authorizeErr: test.authorize}
			runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
			request := test.request(t)

			runtime.handleRequest(context.Background(), request)

			response := nextInboundResponse(t, runtime)
			if response.Status != "error" || response.Sequence != request.Sequence ||
				response.Error == nil || response.Error.Code != test.wantCode ||
				response.Error.Message != test.wantMessage {
				t.Fatalf("response = %#v, want code %q and message %q", response, test.wantCode, test.wantMessage)
			}
			runtime.mu.Lock()
			count := len(runtime.subscriptions)
			runtime.mu.Unlock()
			if count != 0 {
				t.Fatalf("failed command retained %d subscriptions", count)
			}
		})
	}
}

func TestConnectionRuntimeRevokesInvalidPrincipal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 19, 0, 0, 0, time.UTC)
	clock := newRuntimeTestClock(now)
	socket := newInboundTestSocket()
	application := &inboundTestApplication{
		validationErr: app.NewError("authentication.session.invalid"),
	}
	runtime := newInboundRuntime(application, socket, clock)
	done := make(chan struct{})
	go func() {
		runtime.sessionPump(context.Background())
		close(done)
	}()

	ticker := clock.waitTicker(t, sessionCheck)
	ticker.ticks <- now.Add(sessionCheck)
	select {
	case <-socket.closed:
	case <-time.After(time.Second):
		t.Fatal("invalid principal did not close the connection")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("principal-validation responsibility did not stop")
	}

	socket.mu.Lock()
	code, reason := socket.closeCode, socket.closeReason
	socket.mu.Unlock()
	if code != CloseSessionRevoked || reason != "session no longer valid" {
		t.Fatalf("close = (%d, %q), want (%d, %q)", code, reason, CloseSessionRevoked, "session no longer valid")
	}
	runtime.mu.Lock()
	replayable := runtime.replayable
	runtime.mu.Unlock()
	if replayable {
		t.Fatal("session-revoked connection remained replayable")
	}
	application.mu.Lock()
	validations := append([]model.Principal(nil), application.validations...)
	application.mu.Unlock()
	if len(validations) != 1 || validations[0].UserID != runtime.principal.UserID ||
		validations[0].SessionID != runtime.principal.SessionID {
		t.Fatalf("validated principals = %#v", validations)
	}
}

func TestConnectionRuntimeSubscriptionMembershipIsConcurrentSafe(t *testing.T) {
	t.Parallel()

	runtime := newInboundRuntime(
		&inboundTestApplication{},
		newInboundTestSocket(),
		newRuntimeTestClock(time.Now()),
	)
	runtime.send = make(chan outboundMessage, 2048)
	subscription := validInboundSubscription()
	subscribe := requestWithData(t, 31, "subscribe", subscription)
	unsubscribe := requestWithData(t, 32, "unsubscribe", subscription)

	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 1000 {
				_ = runtime.hasSubscription(subscription)
			}
		}()
	}
	for range 500 {
		runtime.handleRequest(context.Background(), subscribe)
		runtime.handleRequest(context.Background(), unsubscribe)
	}
	readers.Wait()
	runtime.handleRequest(context.Background(), subscribe)

	if !runtime.hasSubscription(subscription) {
		t.Fatal("final authorized subscription is not visible to publication")
	}
}

func TestConnectionRuntimeJoinsSimultaneousInboundTermination(t *testing.T) {
	t.Parallel()

	for iteration := range 20 {
		now := time.Date(2026, time.August, 12, 20, iteration, 0, 0, time.UTC)
		clock := newRuntimeTestClock(now)
		socket := newInboundTestSocket()
		runtime := newInboundRuntime(
			&inboundTestApplication{validationErr: app.NewError("authentication.session.invalid")},
			socket,
			clock,
		)
		done := make(chan struct{})
		go func() {
			runtime.run(context.Background())
			close(done)
		}()

		select {
		case <-socket.readDeadlines:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: runtime did not begin reading", iteration)
		}
		validationTicker := clock.waitTicker(t, sessionCheck)
		start := make(chan struct{})
		go func() {
			<-start
			socket.reads <- inboundReadResult{err: io.EOF}
		}()
		go func() {
			<-start
			validationTicker.ticks <- now.Add(sessionCheck)
		}()
		close(start)

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: simultaneous termination leaked a responsibility", iteration)
		}
	}
}

var _ Application = (*inboundTestApplication)(nil)
var _ connectionSocket = (*inboundTestSocket)(nil)
