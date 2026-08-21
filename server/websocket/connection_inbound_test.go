// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type inboundAuthorizationCall struct {
	principal model.Principal
	metadata  model.RequestMetadata
	action    model.Action
	resource  model.Resource
}

type inboundTestApplication struct {
	mu              sync.Mutex
	authorizeErr    error
	validationErr   error
	connectErr      error
	connectResult   app.ExamAttemptConnection
	connectCalls    []app.ConnectExamAttemptCommand
	renewErr        error
	renewResult     app.ExamAttemptParticipationRenewal
	renewCalls      []app.RenewExamAttemptParticipationCommand
	focusLossErr    error
	focusLossResult app.ExamAttemptFocusLossEvaluation
	focusLossCalls  []app.EvaluateExamAttemptFocusLossCommand
	closeCalls      []app.CloseExamAttemptConnectionCommand
	closeContextErr error
	closePrincipal  model.Principal
	closeMetadata   model.RequestMetadata
	terminal        app.CandidateExamTerminal
	terminalCommand app.OpenCandidateExamTerminalCommand
	authorizations  []inboundAuthorizationCall
	validations     []model.Principal
}

func (a *inboundTestApplication) RenewExamAttemptParticipation(_ context.Context, _ app.Invocation, command app.RenewExamAttemptParticipationCommand) (app.ExamAttemptParticipationRenewal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.renewCalls = append(a.renewCalls, command)
	return a.renewResult, a.renewErr
}

func (a *inboundTestApplication) EvaluateExamAttemptFocusLoss(_ context.Context, _ app.Invocation,
	command app.EvaluateExamAttemptFocusLossCommand,
) (app.ExamAttemptFocusLossEvaluation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.focusLossCalls = append(a.focusLossCalls, command)
	return a.focusLossResult, a.focusLossErr
}

func (a *inboundTestApplication) ConnectExamAttempt(_ context.Context, _ app.Invocation, command app.ConnectExamAttemptCommand) (app.ExamAttemptConnection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.connectCalls = append(a.connectCalls, command)
	return a.connectResult, a.connectErr
}

func (a *inboundTestApplication) OpenCandidateExamTerminal(_ context.Context, _ app.Invocation, command app.OpenCandidateExamTerminalCommand) (app.CandidateExamTerminal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminalCommand = command
	if a.terminal == nil {
		return nil, app.NewError("exam.attempt.terminal_unavailable")
	}
	return a.terminal, nil
}

func (a *inboundTestApplication) CloseExamAttemptConnection(ctx context.Context, invocation app.Invocation, command app.CloseExamAttemptConnectionCommand) (app.ExamAttemptConnectionClosed, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeCalls = append(a.closeCalls, command)
	a.closeContextErr = ctx.Err()
	a.closePrincipal = invocation.Principal()
	a.closeMetadata = invocation.RequestMetadata()
	return app.ExamAttemptConnectionClosed{}, nil
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

func TestConnectionRuntimeConnectsExamAttemptAndOwnsCandidateSubscription(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	examID, sittingID, classID := model.NewExamID(), model.NewExamSittingID(), model.NewClassID()
	attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), examID, sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), attempt.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, attempt.ID, participationID, model.NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	application := &inboundTestApplication{connectResult: app.ExamAttemptConnection{
		Attempt: *attempt, Workspace: *workspace, Participation: store.ExamAttemptParticipationView{
			ID: participationID, AttemptID: attempt.ID, State: model.AttemptParticipationActive, Generation: 1,
			StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second),
		}, Connection: *connection, ClassID: classID, FirstAdmission: true,
	}}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID = attempt.CandidateUserID
	runtime.principal.SessionID = connection.SessionID
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 30, "exam_attempt.connect", examAttemptConnectRequest{
		ExamSittingID: sittingID.String(), IdempotencyKey: "admit-once", ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("connect response = %#v", response)
	}
	var body examAttemptConnectResponse
	if err = json.Unmarshal(response.Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.AttemptID != attempt.ID.String() || body.WorkspaceID != workspace.ID.String() ||
		body.ParticipationID != participationID.String() || body.AttemptConnectionID != connectionID.String() ||
		body.Generation != 1 || body.RenewalIntervalSeconds != 5 || !body.FirstAdmission {
		t.Fatalf("connect response body = %#v", body)
	}
	for _, secret := range []string{credential, model.HashToken(credential), "continuity_credential", "credential_hash"} {
		if strings.Contains(string(response.Data), secret) {
			t.Fatalf("connect response exposed %q: %s", secret, response.Data)
		}
	}
	candidate := Subscription{Action: model.ActionExamSittingParticipate,
		Resource: Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}}
	if !runtime.hasSubscription(candidate) {
		t.Fatal("candidate relationship subscription was not installed")
	}
	application.mu.Lock()
	calls := append([]app.ConnectExamAttemptCommand(nil), application.connectCalls...)
	application.mu.Unlock()
	if len(calls) != 1 || calls[0].SittingID != sittingID || calls[0].IdempotencyKey != "admit-once" ||
		calls[0].ContinuityCredential != credential {
		t.Fatalf("connect calls = %#v", calls)
	}
	runtime.handleRequest(context.Background(), requestWithData(t, 31, "exam_attempt.connect", examAttemptConnectRequest{
		ExamSittingID: sittingID.String(), IdempotencyKey: "admit-once", ContinuityCredential: credential,
	}))
	response = nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("exact connect replay response = %#v", response)
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 32, "unsubscribe", candidate))
	response = nextInboundResponse(t, runtime)
	if response.Status != "ok" || !runtime.hasSubscription(candidate) {
		t.Fatalf("protected candidate unsubscribe = %#v, subscribed = %t", response, runtime.hasSubscription(candidate))
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 33, "exam_attempt.connect", examAttemptConnectRequest{
		ExamSittingID: model.NewExamSittingID().String(), IdempotencyKey: "another-admission",
		ContinuityCredential: model.NewCredentialToken(),
	}))
	response = nextInboundResponse(t, runtime)
	if response.Status != "error" || response.Error == nil || response.Error.Code != "exam.attempt.already_connected" {
		t.Fatalf("second binding response = %#v", response)
	}
	application.mu.Lock()
	if got := len(application.connectCalls); got != 2 {
		application.mu.Unlock()
		t.Fatalf("durable connect calls after exact replay and rejected binding = %d, want 2", got)
	}
	application.mu.Unlock()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.finalizeExamAttempt(canceled)
	runtime.finalizeExamAttempt(context.Background())
	if runtime.hasSubscription(candidate) {
		t.Fatal("candidate subscription survived transport finalization")
	}
	application.mu.Lock()
	closeCalls := append([]app.CloseExamAttemptConnectionCommand(nil), application.closeCalls...)
	closeContextErr := application.closeContextErr
	closePrincipal := application.closePrincipal
	closeMetadata := application.closeMetadata
	application.mu.Unlock()
	if len(closeCalls) != 1 || closeCalls[0].AttemptID != attempt.ID || closeCalls[0].SittingID != sittingID ||
		closeCalls[0].ClassID != classID || closeCalls[0].ConnectionID != connectionID ||
		closeCalls[0].Reason != model.AttemptConnectionCloseTransport {
		t.Fatalf("transport close calls = %#v", closeCalls)
	}
	if closeContextErr != nil || closePrincipal.UserID != runtime.principal.UserID ||
		closePrincipal.SessionID != runtime.principal.SessionID || closeMetadata.RequestID != runtime.id+":attempt-close" {
		t.Fatalf("transport close invocation = context %v, principal %#v, metadata %#v", closeContextErr, closePrincipal, closeMetadata)
	}
}

func TestConnectionRuntimeRejectsNonStrictExamAttemptConnectPayload(t *testing.T) {
	t.Parallel()
	runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.handleRequest(context.Background(), &Request{Sequence: 31, Action: "exam_attempt.connect",
		Data: json.RawMessage(`{"exam_sitting_id":"bad","idempotency_key":"one","idempotency_key":"two","continuity_credential":"secret"}`)})
	response := nextInboundResponse(t, runtime)
	if response.Status != "error" || response.Error == nil || response.Error.Code != "websocket.request.invalid" {
		t.Fatalf("duplicate connect response = %#v", response)
	}
}

func TestConnectionRuntimeRenewsBoundParticipationWithoutTreatingPingAsRenewal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), attempt.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, attempt.ID, participationID, model.NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	databaseNow := at.Add(5 * time.Second)
	application := &inboundTestApplication{connectResult: app.ExamAttemptConnection{
		Attempt: *attempt, Workspace: *workspace, Participation: store.ExamAttemptParticipationView{
			ID: participationID, AttemptID: attempt.ID, State: model.AttemptParticipationActive, Generation: 4,
			StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(model.AttemptParticipationInitialLease),
		}, Connection: *connection, ClassID: model.NewClassID(), FirstAdmission: true,
	}, renewResult: app.ExamAttemptParticipationRenewal{
		AttemptID: attempt.ID, ParticipationID: participationID, Generation: 4, AcceptedSequence: 1,
		DatabaseTime: databaseNow, LeaseExpiresAt: databaseNow.Add(model.AttemptParticipationInitialLease),
	}}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID, runtime.principal.SessionID = attempt.CandidateUserID, connection.SessionID
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 40, examAttemptConnectAction, examAttemptConnectRequest{
		ExamSittingID: sittingID.String(), IdempotencyKey: "connect", ContinuityCredential: credential,
	}))
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("connect response = %#v", response)
	}
	runtime.handleRequest(context.Background(), &Request{Sequence: 41, Action: "ping", Data: json.RawMessage(`{}`)})
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("ping response = %#v", response)
	}
	application.mu.Lock()
	if len(application.renewCalls) != 0 {
		application.mu.Unlock()
		t.Fatal("WebSocket ping renewed the Participation")
	}
	application.mu.Unlock()
	runtime.handleRequest(context.Background(), requestWithData(t, 42, examAttemptRenewAction, examAttemptRenewRequest{
		Generation: 4, Sequence: 1, ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("renew response = %#v", response)
	}
	var body examAttemptRenewResponse
	if err = json.Unmarshal(response.Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Generation != 4 || body.AcceptedSequence != 1 || body.DatabaseTime != databaseNow.Format(time.RFC3339Nano) ||
		body.LeaseExpiresAt != databaseNow.Add(model.AttemptParticipationInitialLease).Format(time.RFC3339Nano) {
		t.Fatalf("renew body = %#v", body)
	}
	for _, secret := range []string{credential, model.HashToken(credential), "continuity_credential", "credential_hash"} {
		if strings.Contains(string(response.Data), secret) {
			t.Fatalf("renew response exposed %q: %s", secret, response.Data)
		}
	}
	application.mu.Lock()
	calls := append([]app.RenewExamAttemptParticipationCommand(nil), application.renewCalls...)
	application.mu.Unlock()
	if len(calls) != 1 || calls[0].AttemptID != attempt.ID || calls[0].ParticipationID != participationID ||
		calls[0].ConnectionID != connectionID || calls[0].Generation != 4 || calls[0].Sequence != 1 ||
		calls[0].ContinuityCredential != credential {
		t.Fatalf("renew calls = %#v", calls)
	}
}

func TestConnectionRuntimeRenewalUsesStrictPayloadAndSafeConnectionLossMessage(t *testing.T) {
	t.Parallel()
	application := &inboundTestApplication{}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.handleRequest(context.Background(), &Request{Sequence: 50, Action: examAttemptRenewAction,
		Data: json.RawMessage(`{"generation":1,"sequence":1,"sequence":2,"continuity_credential":"bad"}`)})
	if response := nextInboundResponse(t, runtime); response.Status != "error" || response.Error == nil || response.Error.Code != "websocket.request.invalid" {
		t.Fatalf("strict renewal response = %#v", response)
	}
	application.renewErr = app.NewError("exam.attempt.connection_lost")
	runtime.attempt = &examAttemptBinding{attemptID: model.NewExamAttemptID(), participationID: model.NewAttemptParticipationID(),
		connectionID: model.NewAttemptConnectionID(), generation: 1}
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 51, examAttemptRenewAction, examAttemptRenewRequest{
		Generation: 1, Sequence: 2, ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	if response.Status != "error" || response.Error == nil || response.Error.Code != "exam.attempt.connection_lost" ||
		response.Error.Message != "Secure connectivity could not be renewed. Ask a manager to re-allow access." {
		t.Fatalf("connection-loss response = %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential) || strings.Contains(string(encoded), model.HashToken(credential)) {
		t.Fatalf("connection-loss response exposed credential material: %s", encoded)
	}
}

func TestConnectionRuntimeSubmitsBoundFocusLossAndClearsPolicySuspendedBinding(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), attempt.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, attempt.ID, participationID, model.NewSessionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := at.Add(2 * time.Second)
	application := &inboundTestApplication{connectResult: app.ExamAttemptConnection{Attempt: *attempt, Workspace: *workspace,
		Participation: store.ExamAttemptParticipationView{ID: participationID, AttemptID: attempt.ID,
			State: model.AttemptParticipationActive, Generation: 4, StartedAt: at, UpdatedAt: at,
			LeaseExpiresAt: at.Add(model.AttemptParticipationInitialLease)}, Connection: *connection, ClassID: model.NewClassID()},
		focusLossResult: app.ExamAttemptFocusLossEvaluation{AttemptID: attempt.ID, ParticipationID: participationID,
			Generation: 4, AcceptedSequence: 3, ReceivedAt: receivedAt, Qualified: true,
			FlagCreated: true, SuspensionCreated: true, ConnectionClosed: true}}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID, runtime.principal.SessionID = attempt.CandidateUserID, connection.SessionID
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 60, examAttemptConnectAction, examAttemptConnectRequest{
		ExamSittingID: sittingID.String(), IdempotencyKey: "connect", ContinuityCredential: credential,
	}))
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("connect response=%#v", response)
	}
	runtime.handleRequest(context.Background(), requestWithData(t, 61, examAttemptFocusLossAction, examAttemptFocusLossRequest{
		SchemaVersion: model.FocusLossSignalSchemaVersion,
		Generation:    4, Sequence: 3, DurationMilliseconds: 2500, Source: "fullscreen_exited",
		ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("Focus Loss response=%#v", response)
	}
	var body examAttemptFocusLossResponse
	if err = json.Unmarshal(response.Data, &body); err != nil || body.Generation != 4 || body.AcceptedSequence != 3 ||
		body.ReceivedAt != receivedAt.Format(time.RFC3339Nano) || !body.SuspensionCreated {
		t.Fatalf("Focus Loss body=%#v error=%v", body, err)
	}
	encoded := string(response.Data)
	for _, forbidden := range []string{credential, model.HashToken(credential), "duration", "source", "outcome", "threshold", "qualified", "flag_created", "session"} {
		if strings.Contains(strings.ToLower(encoded), strings.ToLower(forbidden)) {
			t.Fatalf("Focus Loss response exposed %q: %s", forbidden, encoded)
		}
	}
	application.mu.Lock()
	calls := append([]app.EvaluateExamAttemptFocusLossCommand(nil), application.focusLossCalls...)
	application.mu.Unlock()
	if len(calls) != 1 || calls[0].SchemaVersion != model.FocusLossSignalSchemaVersion ||
		calls[0].AttemptID != attempt.ID || calls[0].ParticipationID != participationID ||
		calls[0].ConnectionID != connectionID || calls[0].Generation != 4 || calls[0].Sequence != 3 ||
		calls[0].DurationMilliseconds != 2500 || calls[0].Source != model.FocusLossSourceFullscreenExited ||
		calls[0].ContinuityCredential != credential {
		t.Fatalf("Focus Loss calls=%#v", calls)
	}

	runtime.handleRequest(context.Background(), requestWithData(t, 62, examAttemptRenewAction, examAttemptRenewRequest{
		Generation: 4, Sequence: 4, ContinuityCredential: credential,
	}))
	if rejected := nextInboundResponse(t, runtime); rejected.Status != "error" || rejected.Error == nil ||
		rejected.Error.Code != "exam.attempt.connection_closed" {
		t.Fatalf("post-suspension renewal=%#v", rejected)
	}
	runtime.handleRequest(context.Background(), requestWithData(t, 63, examAttemptFocusLossAction, examAttemptFocusLossRequest{
		SchemaVersion: model.FocusLossSignalSchemaVersion,
		Generation:    4, Sequence: 4, DurationMilliseconds: 2500, ContinuityCredential: credential,
	}))
	if rejected := nextInboundResponse(t, runtime); rejected.Status != "error" || rejected.Error == nil ||
		rejected.Error.Code != "exam.attempt.connection_closed" {
		t.Fatalf("post-suspension Focus Loss signal=%#v", rejected)
	}
	runtime.mu.Lock()
	binding, subscriptions := runtime.attempt, len(runtime.subscriptions)
	runtime.mu.Unlock()
	application.mu.Lock()
	defer application.mu.Unlock()
	if len(application.renewCalls) != 0 || len(application.focusLossCalls) != 1 || binding != nil || subscriptions != 0 {
		t.Fatalf("post-suspension renewals=%#v Focus Loss calls=%#v binding=%#v",
			application.renewCalls, application.focusLossCalls, binding)
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
			name: "concealed exam subscription",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 27, "subscribe", validInboundSubscription())
			},
			authorize:   app.NewError("resource.not_found"),
			wantCode:    "resource.not_found",
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

type inboundTerminalFake struct {
	mu     sync.Mutex
	writes []byte
	window app.CandidateExamTerminalWindow
	closed chan struct{}
	once   sync.Once
}

func newInboundTerminalFake() *inboundTerminalFake {
	return &inboundTerminalFake{closed: make(chan struct{})}
}
func (terminal *inboundTerminalFake) Read([]byte) (int, error) { <-terminal.closed; return 0, io.EOF }
func (terminal *inboundTerminalFake) Write(data []byte) (int, error) {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	terminal.writes = append(terminal.writes, data...)
	return len(data), nil
}
func (terminal *inboundTerminalFake) Resize(_ context.Context, window app.CandidateExamTerminalWindow) error {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	terminal.window = window
	return nil
}
func (terminal *inboundTerminalFake) Close() error {
	terminal.once.Do(func() { close(terminal.closed) })
	return nil
}

func TestConnectionRuntimeBridgesBoundCandidateTerminal(t *testing.T) {
	t.Parallel()
	terminal := newInboundTerminalFake()
	application := &inboundTestApplication{terminal: terminal}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	binding := &examAttemptBinding{attemptID: model.NewExamAttemptID(), sittingID: model.NewExamSittingID(),
		classID: model.NewClassID(), connectionID: model.NewAttemptConnectionID(),
		participationID: model.NewAttemptParticipationID(), generation: 3}
	runtime.attempt = binding
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 100, examAttemptTerminalOpenAction,
		examAttemptTerminalOpenRequest{Generation: 3, ContinuityCredential: credential, Cols: 120, Rows: 40}))
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("terminal open response = %#v", response)
	}
	application.mu.Lock()
	command := application.terminalCommand
	application.mu.Unlock()
	if command.Access.AttemptID != binding.attemptID || command.Access.ConnectionID != binding.connectionID ||
		command.Access.ContinuityCredential != credential || command.ParticipationID != binding.participationID ||
		command.SittingID != binding.sittingID || command.ClassID != binding.classID || command.Generation != 3 ||
		command.Window != (app.CandidateExamTerminalWindow{Cols: 120, Rows: 40}) {
		t.Fatalf("terminal command = %#v", command)
	}
	runtime.handleRequest(context.Background(), requestWithData(t, 101, examAttemptTerminalInputAction,
		examAttemptTerminalInputRequest{Data: base64.StdEncoding.EncodeToString([]byte("go test\n"))}))
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("terminal input response = %#v", response)
	}
	runtime.handleRequest(context.Background(), requestWithData(t, 102, examAttemptTerminalResizeAction,
		examAttemptTerminalResizeRequest{Cols: 90, Rows: 30}))
	if response := nextInboundResponse(t, runtime); response.Status != "ok" {
		t.Fatalf("terminal resize response = %#v", response)
	}
	terminal.mu.Lock()
	writes, window := string(terminal.writes), terminal.window
	terminal.mu.Unlock()
	if writes != "go test\n" || window != (app.CandidateExamTerminalWindow{Cols: 90, Rows: 30}) {
		t.Fatalf("terminal writes=%q window=%#v", writes, window)
	}
	runtime.closeTerminal()
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
