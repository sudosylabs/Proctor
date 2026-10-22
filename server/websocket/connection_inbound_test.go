// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
	mu                  sync.Mutex
	authorizeErr        error
	validationErr       error
	connectErr          error
	connectResult       attempt.ConnectionResult
	connectCalls        []attempt.ConnectCommand
	renewErr            error
	renewResult         attempt.ParticipationRenewal
	renewCalls          []attempt.RenewParticipationCommand
	focusLossErr        error
	focusLossResult     attempt.FocusLossEvaluation
	focusLossCalls      []attempt.FocusLossCommand
	browserStartErr     error
	browserStartResult  model.BrowserSourceStatus
	browserStartCalls   []attempt.StartBrowserActivityCommand
	browserAppendErr    error
	browserAppendResult model.BrowserActivityAcknowledgement
	browserAppendCalls  []attempt.AppendBrowserActivityCommand
	closeCalls          []attempt.CloseConnectionCommand
	closeContextErr     error
	closePrincipal      model.Principal
	closeMetadata       model.RequestMetadata
	authorizations      []inboundAuthorizationCall
	validations         []model.Principal
}

func (a *inboundTestApplication) RenewExamAttemptParticipation(_ context.Context, _ invocation.Invocation, command attempt.RenewParticipationCommand) (attempt.ParticipationRenewal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.renewCalls = append(a.renewCalls, command)
	return a.renewResult, a.renewErr
}

func (a *inboundTestApplication) EvaluateExamAttemptFocusLoss(_ context.Context, _ invocation.Invocation,
	command attempt.FocusLossCommand,
) (attempt.FocusLossEvaluation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.focusLossCalls = append(a.focusLossCalls, command)
	return a.focusLossResult, a.focusLossErr
}

func (a *inboundTestApplication) ConnectExamAttempt(_ context.Context, _ invocation.Invocation, command attempt.ConnectCommand) (attempt.ConnectionResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.connectCalls = append(a.connectCalls, command)
	return a.connectResult, a.connectErr
}

func (a *inboundTestApplication) StartExamAttemptBrowserActivity(_ context.Context, _ invocation.Invocation,
	command attempt.StartBrowserActivityCommand,
) (model.BrowserSourceStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.browserStartCalls = append(a.browserStartCalls, command)
	return a.browserStartResult, a.browserStartErr
}

func (a *inboundTestApplication) AppendExamAttemptBrowserActivity(_ context.Context, _ invocation.Invocation,
	command attempt.AppendBrowserActivityCommand,
) (model.BrowserActivityAcknowledgement, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.browserAppendCalls = append(a.browserAppendCalls, command)
	return a.browserAppendResult, a.browserAppendErr
}

func (a *inboundTestApplication) CloseExamAttemptConnection(ctx context.Context, invocation invocation.Invocation, command attempt.CloseConnectionCommand) (attempt.ConnectionClosedResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeCalls = append(a.closeCalls, command)
	a.closeContextErr = ctx.Err()
	a.closePrincipal = invocation.Principal()
	a.closeMetadata = invocation.RequestMetadata()
	return attempt.ConnectionClosedResult{}, nil
}

func (a *inboundTestApplication) AuthorizeWebSocketSubscription(
	_ context.Context,
	invocation invocation.Invocation,
	action model.Action,
	resource model.Resource,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorizations = append(a.authorizations, inboundAuthorizationCall{
		principal: invocation.Principal(),
		metadata:  invocation.RequestMetadata(),
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
	pongInstalled chan struct{}
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
		pongInstalled: make(chan struct{}, 1),
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
	s.pongInstalled <- struct{}{}
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
			CredentialID: model.PrincipalCredentialID(model.NewID()),
		},
		metadata:      model.RequestMetadata{RequestID: "upgrade-request"},
		id:            model.NewID(),
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
			ID:   model.NewID(),
		},
	}
}

func requestWithData(t *testing.T, sequence int64, action string, value any) *Request {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return &Request{Sequence: sequence, Action: action, Data: data}
}

func nextInboundResponse(t *testing.T, runtime *connectionRuntime) *Response {
	t.Helper()
	select {
	case message := <-runtime.send:
		require.NotNil(t, message.response, "runtime enqueued an event, want response")
		return message.response
	case <-time.After(time.Second):
		require.FailNow(t, "runtime did not enqueue a response")
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
	require.Equal(t, "error", invalid.Status)
	require.Equal(t, int64(0), invalid.Sequence)
	require.NotNil(t, invalid.Error)
	require.Equal(t, "websocket.request.invalid", invalid.Error.Code)
	require.Equal(t, "Invalid WebSocket request.", invalid.Error.Message)
	pong := nextInboundResponse(t, runtime)
	require.Equal(t, "ok", pong.Status)
	require.Equal(t, int64(2), pong.Sequence)
	require.Equal(t, `{"pong":true}`, string(pong.Data))
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
		require.FailNow(t, "runtime did not begin its read responsibility")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		require.FailNow(t, "runtime did not stop a blocked read after parent cancellation")
	}
}

func TestConnectionRuntimePongExtendsReadDeadline(t *testing.T) {
	t.Parallel()

	initial := time.Date(2026, time.August, 12, 18, 0, 0, 0, time.UTC)
	clock := newRuntimeTestClock(initial)
	socket := newInboundTestSocket()
	t.Cleanup(func() { _ = socket.Close() })
	runtime := newInboundRuntime(&inboundTestApplication{}, socket, clock)
	done := make(chan struct{})
	go func() {
		runtime.readPump(context.Background())
		close(done)
	}()

	deadline := <-socket.readDeadlines
	require.True(t, deadline.Equal(initial.Add(pongWait)), "initial read deadline = %s", deadline)
	// The initial deadline is published before handler installation. Wait for
	// the actual setup step instead of relying on goroutine scheduling.
	select {
	case <-socket.pongInstalled:
	case <-time.After(time.Second):
		require.FailNow(t, "runtime did not install a pong handler")
	}
	socket.mu.Lock()
	if socket.readLimit != MaxMessageBytes {
		socket.mu.Unlock()
		require.FailNow(t, fmt.Sprintf("read limit = %d, want %d", socket.readLimit, MaxMessageBytes))
	}
	pongHandler := socket.pongHandler
	socket.mu.Unlock()
	require.NotNil(t, pongHandler, "runtime did not install a pong handler")

	afterPong := initial.Add(17 * time.Second)
	clock.mu.Lock()
	clock.now = afterPong
	clock.mu.Unlock()
	require.NoError(t, pongHandler("client payload"))
	deadline = <-socket.readDeadlines
	require.True(t, deadline.Equal(afterPong.Add(pongWait)), "pong read deadline = %s", deadline)

	_ = socket.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "read responsibility did not stop after socket closure")
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
	require.Equal(t, "ok", response.Status)
	require.Equal(t, int64(7), response.Sequence)
	require.Nil(t, response.Error)
	require.True(t, runtime.hasSubscription(subscription), "authorized subscription is not visible to publication")

	runtime.handleRequest(context.Background(), requestWithData(t, 8, "subscribe", subscription))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Equal(t, int64(8), response.Sequence)
	runtime.mu.Lock()
	if got := len(runtime.subscriptions); got != 1 {
		runtime.mu.Unlock()
		require.FailNow(t, fmt.Sprintf("subscription count after duplicate = %d, want 1", got))
	}
	for len(runtime.subscriptions) < maximumSubscriptions {
		candidate := validInboundSubscription()
		runtime.subscriptions[candidate.Key()] = candidate
	}
	runtime.mu.Unlock()

	overLimit := validInboundSubscription()
	runtime.handleRequest(context.Background(), requestWithData(t, 9, "subscribe", overLimit))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.Equal(t, int64(9), response.Sequence)
	require.NotNil(t, response.Error)
	require.Equal(t, "websocket.subscription.limit", response.Error.Code)
	require.Equal(t, "WebSocket subscription limit reached.", response.Error.Message)
	require.False(t, runtime.hasSubscription(overLimit), "over-limit subscription was retained")

	runtime.handleRequest(context.Background(), requestWithData(t, 10, "subscribe", subscription))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Equal(t, int64(10), response.Sequence)

	runtime.handleRequest(context.Background(), requestWithData(t, 11, "unsubscribe", subscription))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status, "unsubscribe response = %#v, still subscribed", response)
	require.Equal(t, int64(11), response.Sequence, "unsubscribe response = %#v, still subscribed", response)
	require.False(t, runtime.hasSubscription(subscription), "unsubscribe response = %#v, still subscribed", response)

	application.mu.Lock()
	calls := append([]inboundAuthorizationCall(nil), application.authorizations...)
	application.mu.Unlock()
	require.Len(t, calls, 4, "authorization calls")
	first := calls[0]
	require.Equal(t, runtime.principal.UserID, first.principal.UserID)
	require.Equal(t, runtime.id+":7", first.metadata.RequestID)
	require.Equal(t, runtime.metadata.IPAddress, first.metadata.IPAddress)
	require.Equal(t, runtime.metadata.UserAgent, first.metadata.UserAgent)
	require.Equal(t, subscription.Action, first.action)
	require.Equal(t, subscription.Resource.model(), first.resource)
}

func TestConnectionRuntimeConnectsExamAttemptAndOwnsCandidateSubscription(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 17, 10, 0, 0, 0, time.UTC)
	examID, sittingID, classID := model.NewExamID(), model.NewExamSittingID(), model.NewClassID()
	examAttempt, err := model.NewExamAttempt(model.NewExamAttemptID(), examID, sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	require.NoError(t, err)
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), examAttempt.ID, at)
	require.NoError(t, err)
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, examAttempt.ID, participationID, model.NewSessionID(), at)
	require.NoError(t, err)
	application := &inboundTestApplication{connectResult: attempt.ConnectionResult{
		Attempt: *examAttempt, Workspace: *workspace, Participation: store.ExamAttemptParticipationView{
			ID: participationID, AttemptID: examAttempt.ID, State: model.AttemptParticipationActive, Generation: 1,
			StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second),
		}, Connection: *connection, ClassID: classID, FirstAdmission: true,
	}}
	application.connectResult.Security = inboundSecurityFixture(t, application.connectResult)
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID = examAttempt.CandidateUserID
	runtime.principal.SessionID = connection.SessionID
	credential := model.NewCredentialToken()
	connectRequest := validExamAttemptConnectRequest(t, sittingID, "admit-once", credential)
	runtime.handleRequest(context.Background(), requestWithData(t, 30, "exam_attempt.connect", connectRequest))
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status, "connect response = %#v error", response)
	require.Nil(t, response.Error, "connect response = %#v error", response)
	var body examAttemptConnectResponse
	err = json.Unmarshal(response.Data, &body)
	require.NoError(t, err)
	require.Equal(t, examAttempt.ID.String(), body.AttemptID)
	require.Equal(t, workspace.ID.String(), body.WorkspaceID)
	require.Equal(t, participationID.String(), body.ParticipationID)
	require.Equal(t, connectionID.String(), body.AttemptConnectionID)
	require.Equal(t, int64(1), body.Generation)
	require.Equal(t, int64(5), body.RenewalIntervalSeconds)
	require.True(t, body.FirstAdmission)
	for _, secret := range []string{credential, model.HashToken(credential), "continuity_credential", "credential_hash"} {
		require.NotContains(t, string(response.Data), secret, "connect response exposed")
	}
	candidate := Subscription{Action: model.ActionExamSittingParticipate,
		Resource: Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}}
	require.True(t, runtime.hasSubscription(candidate), "candidate relationship subscription was not installed")
	application.mu.Lock()
	calls := append([]attempt.ConnectCommand(nil), application.connectCalls...)
	application.mu.Unlock()
	require.Len(t, calls, 1)
	require.Equal(t, sittingID, calls[0].SittingID)
	require.Equal(t, "admit-once", calls[0].IdempotencyKey)
	require.Equal(t, credential, calls[0].ContinuityCredential)
	runtime.handleRequest(context.Background(), requestWithData(t, 31, "exam_attempt.connect", connectRequest))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Nil(t, response.Error)

	runtime.handleRequest(context.Background(), requestWithData(t, 32, "unsubscribe", candidate))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status, "protected candidate unsubscribe = %#v, subscribed", response)
	require.True(t, runtime.hasSubscription(candidate), "protected candidate unsubscribe = %#v, subscribed", response)

	runtime.handleRequest(context.Background(), requestWithData(t, 33, "exam_attempt.connect",
		validExamAttemptConnectRequest(t, model.NewExamSittingID(), "another-admission", model.NewCredentialToken())))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "exam.attempt.already_connected", response.Error.Code)
	application.mu.Lock()
	if got := len(application.connectCalls); got != 2 {
		application.mu.Unlock()
		require.FailNow(t, fmt.Sprintf("durable connect calls after exact replay and rejected binding = %d, want 2", got))
	}
	application.mu.Unlock()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.finalizeExamAttempt(canceled)
	runtime.finalizeExamAttempt(context.Background())
	require.False(t, runtime.hasSubscription(candidate), "candidate subscription survived transport finalization")
	application.mu.Lock()
	closeCalls := append([]attempt.CloseConnectionCommand(nil), application.closeCalls...)
	closeContextErr := application.closeContextErr
	closePrincipal := application.closePrincipal
	closeMetadata := application.closeMetadata
	application.mu.Unlock()
	require.Len(t, closeCalls, 1)
	require.Equal(t, examAttempt.ID, closeCalls[0].AttemptID)
	require.Equal(t, sittingID, closeCalls[0].SittingID)
	require.Equal(t, classID, closeCalls[0].ClassID)
	require.Equal(t, connectionID, closeCalls[0].ConnectionID)
	require.Equal(t, model.AttemptConnectionCloseTransport, closeCalls[0].Reason)
	require.NoError(t, closeContextErr, "transport close invocation = context %v, principal %#v, metadata %#v", closeContextErr, closePrincipal, closeMetadata)
	require.Equal(t, runtime.principal.UserID, closePrincipal.UserID, "transport close invocation = context %v, principal %#v, metadata %#v", closeContextErr, closePrincipal, closeMetadata)
	require.Equal(t, runtime.principal.SessionID, closePrincipal.SessionID, "transport close invocation = context %v, principal %#v, metadata %#v", closeContextErr, closePrincipal, closeMetadata)
	require.Equal(t, runtime.id+":attempt-close", closeMetadata.RequestID, "transport close invocation = context %v, principal %#v, metadata %#v", closeContextErr, closePrincipal, closeMetadata)
}

func TestConnectionRuntimeRejectsNonStrictExamAttemptConnectPayload(t *testing.T) {
	t.Parallel()
	runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.handleRequest(context.Background(), &Request{Sequence: 31, Action: "exam_attempt.connect",
		Data: json.RawMessage(`{"exam_sitting_id":"bad","idempotency_key":"one","idempotency_key":"two","continuity_credential":"secret"}`)})
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "websocket.request.invalid", response.Error.Code)
}

func TestConnectionRuntimeRenewsBoundParticipationWithoutTreatingPingAsRenewal(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	examAttempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	require.NoError(t, err)
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), examAttempt.ID, at)
	require.NoError(t, err)
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, examAttempt.ID, participationID, model.NewSessionID(), at)
	require.NoError(t, err)
	databaseNow := at.Add(5 * time.Second)
	application := &inboundTestApplication{connectResult: attempt.ConnectionResult{
		Attempt: *examAttempt, Workspace: *workspace, Participation: store.ExamAttemptParticipationView{
			ID: participationID, AttemptID: examAttempt.ID, State: model.AttemptParticipationActive, Generation: 4,
			StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(model.AttemptParticipationInitialLease),
		}, Connection: *connection, ClassID: model.NewClassID(), FirstAdmission: true,
	}, renewResult: attempt.ParticipationRenewal{SecurityCoverage: renewalCoverageResultFixture(),
		AttemptID: examAttempt.ID, ParticipationID: participationID, Generation: 4, AcceptedSequence: 1,
		DatabaseTime: databaseNow, LeaseExpiresAt: databaseNow.Add(model.AttemptParticipationInitialLease),
	}}
	application.connectResult.Security = inboundSecurityFixture(t, application.connectResult)
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID, runtime.principal.SessionID = examAttempt.CandidateUserID, connection.SessionID
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 40, examAttemptConnectAction,
		validExamAttemptConnectRequest(t, sittingID, "connect", credential)))
	require.Equal(t, "ok", nextInboundResponse(t, runtime).Status)
	runtime.handleRequest(context.Background(), &Request{Sequence: 41, Action: "ping", Data: json.RawMessage(`{}`)})
	require.Equal(t, "ok", nextInboundResponse(t, runtime).Status)
	application.mu.Lock()
	if len(application.renewCalls) != 0 {
		application.mu.Unlock()
		require.FailNow(t, "WebSocket ping renewed the Participation")
	}
	application.mu.Unlock()
	runtime.handleRequest(context.Background(), requestWithData(t, 42, examAttemptRenewAction, examAttemptRenewRequest{SecurityCoverage: renewalCoverageFixture(),
		Generation: 4, Sequence: 1, ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Nil(t, response.Error)
	var body examAttemptRenewResponse
	err = json.Unmarshal(response.Data, &body)
	require.NoError(t, err)
	require.Equal(t, int64(4), body.Generation)
	require.Equal(t, int64(1), body.AcceptedSequence)
	require.Equal(t, databaseNow.Format(time.RFC3339Nano), body.DatabaseTime)
	require.Equal(t, databaseNow.Add(model.AttemptParticipationInitialLease).Format(time.RFC3339Nano), body.LeaseExpiresAt)
	for _, secret := range []string{credential, model.HashToken(credential), "continuity_credential", "credential_hash"} {
		require.NotContains(t, string(response.Data), secret, "renew response exposed")
	}
	application.mu.Lock()
	calls := append([]attempt.RenewParticipationCommand(nil), application.renewCalls...)
	application.mu.Unlock()
	require.Len(t, calls, 1)
	require.Equal(t, examAttempt.ID, calls[0].AttemptID)
	require.Equal(t, participationID, calls[0].ParticipationID)
	require.Equal(t, connectionID, calls[0].ConnectionID)
	require.Equal(t, int64(4), calls[0].Generation)
	require.Equal(t, int64(1), calls[0].Sequence)
	require.Equal(t, credential, calls[0].ContinuityCredential)
}

func TestConnectionRuntimeBrowserActivityUsesAttemptBindingAndClosedWireContracts(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)
	attemptID, sittingID, classID := model.NewExamAttemptID(), model.NewExamSittingID(), model.NewClassID()
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	sourceID := model.BrowserSourceSessionID("018f47a0-6e53-4cc4-9d0b-97c9b6d98011")
	credential := model.NewCredentialToken()
	application := &inboundTestApplication{
		browserStartResult: model.BrowserSourceStatus{SourceSessionID: sourceID, BrowserDeliveryProgress: model.BrowserDeliveryProgress{MissingRanges: []model.SequenceRange{}}, ServerTime: at},
		browserAppendResult: model.BrowserActivityAcknowledgement{SourceSessionID: sourceID, HighestContiguous: 1, HighestSeen: 4,
			MissingRanges: []model.BrowserActivityMissingRange{{First: 2, Last: 3}}, ServerTime: at.Add(time.Second)},
	}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.attempt = &examAttemptBinding{attemptID: attemptID, sittingID: sittingID, classID: classID,
		connectionID: connectionID, participationID: participationID, generation: 2}

	start := examAttemptBrowserStartRequest{Generation: 2, PolicyRevisionID: model.NewExamRevisionID().String(), PolicyDigest: "sha256:" + strings.Repeat("a", 64), Transition: model.BrowserStartTransition{Kind: "initial"},
		ContinuityCredential: credential, ParticipationID: participationID.String(), SourceSessionID: string(sourceID)}
	runtime.handleRequest(context.Background(), requestWithData(t, 40, examAttemptBrowserStartAction, start))
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Nil(t, response.Error)
	var startAck examAttemptBrowserAcknowledgementResponse
	require.NoError(t, json.Unmarshal(response.Data, &startAck))
	require.Equal(t, string(sourceID), startAck.SourceSessionID)
	require.Equal(t, int64(0), startAck.HighestContiguous)
	require.Equal(t, int64(0), startAck.HighestSeen)
	require.NotNil(t, startAck.MissingRanges)
	require.Equal(t, at.Format(time.RFC3339Nano), startAck.ServerTime)
	require.NotContains(t, string(response.Data), credential, "Browser Activity start exposed continuity credential")

	eventDocument, err := json.Marshal(examAttemptBrowserEventRequest{Sequence: 1, Kind: string(model.BrowserActivityOpened),
		PolicyRevisionID: start.PolicyRevisionID, ClientOccurredAt: at.Format(time.RFC3339Nano)})
	require.NoError(t, err)
	appendRequest := examAttemptBrowserAppendRequest{Generation: 2, PolicyRevisionID: start.PolicyRevisionID, PolicyDigest: start.PolicyDigest,
		ContinuityCredential: credential, ParticipationID: participationID.String(), SourceSessionID: string(sourceID),
		Events: []json.RawMessage{eventDocument}}
	runtime.handleRequest(context.Background(), requestWithData(t, 41, examAttemptBrowserAppendAction, appendRequest))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Nil(t, response.Error)
	var appendAck examAttemptBrowserAcknowledgementResponse
	err = json.Unmarshal(response.Data, &appendAck)
	require.NoError(t, err)
	require.Equal(t, int64(1), appendAck.HighestContiguous)
	require.Equal(t, int64(4), appendAck.HighestSeen)
	require.Len(t, appendAck.MissingRanges, 1)
	require.Equal(t, browserActivityMissingRangeResponse{First: 2, Last: 3}, appendAck.MissingRanges[0])

	application.mu.Lock()
	startCalls := append([]attempt.StartBrowserActivityCommand(nil), application.browserStartCalls...)
	appendCalls := append([]attempt.AppendBrowserActivityCommand(nil), application.browserAppendCalls...)
	application.mu.Unlock()
	require.Len(t, startCalls, 1)
	require.Equal(t, attemptID, startCalls[0].AttemptID)
	require.Equal(t, connectionID, startCalls[0].ConnectionID)
	require.Equal(t, participationID, startCalls[0].ParticipationID)
	require.Equal(t, int64(2), startCalls[0].Generation)
	require.Equal(t, credential, startCalls[0].ContinuityCredential)
	require.Equal(t, sourceID, startCalls[0].SourceSessionID)
	require.Len(t, appendCalls, 1)
	require.Equal(t, attemptID, appendCalls[0].AttemptID)
	require.Equal(t, connectionID, appendCalls[0].ConnectionID)
	require.Equal(t, participationID, appendCalls[0].ParticipationID)
	require.Equal(t, int64(2), appendCalls[0].Generation)
	require.Equal(t, credential, appendCalls[0].ContinuityCredential)
	require.Equal(t, sourceID, appendCalls[0].SourceSessionID)
	require.Len(t, appendCalls[0].Events, 1)
	require.Equal(t, model.BrowserActivityOpened, appendCalls[0].Events[0].Kind)

	stale := appendRequest
	stale.Generation = 3
	runtime.handleRequest(context.Background(), requestWithData(t, 42, examAttemptBrowserAppendAction, stale))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "exam.attempt.connection_closed", response.Error.Code)

	application.mu.Lock()
	application.browserAppendErr = apperror.New("browser_activity.gap_too_large")
	application.mu.Unlock()
	runtime.handleRequest(context.Background(), requestWithData(t, 43, examAttemptBrowserAppendAction, appendRequest))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "browser_activity.gap_too_large", response.Error.Code)
	require.Equal(t, "Browser activity could not be accepted.", response.Error.Message)
}

func TestDecodeBrowserActivityAppendAcceptsSafeBlockedLocations(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)
	revisionID := model.NewExamRevisionID().String()
	blocked := []examAttemptBrowserEventRequest{
		{Sequence: 1, Kind: string(model.BrowserActivityBlockedNavigation), PolicyRevisionID: revisionID,
			ClientOccurredAt: at.Format(time.RFC3339Nano), Location: json.RawMessage(`{"scheme":"http","host":"example.edu","path":"/outside"}`),
			BlockReason: json.RawMessage(`"scheme_not_allowed"`)},
		{Sequence: 2, Kind: string(model.BrowserActivityBlockedNavigation), PolicyRevisionID: revisionID,
			ClientOccurredAt: at.Add(time.Millisecond).Format(time.RFC3339Nano), Location: json.RawMessage(`{"scheme":"file","host":"","path":""}`),
			BlockReason: json.RawMessage(`"scheme_not_allowed"`)},
		{Sequence: 3, Kind: string(model.BrowserActivityBlockedNavigation), PolicyRevisionID: revisionID,
			ClientOccurredAt: at.Add(2 * time.Millisecond).Format(time.RFC3339Nano), Location: json.RawMessage(`{"scheme":"","host":"","path":""}`),
			BlockReason: json.RawMessage(`"invalid_url"`)},
	}
	documents := make([]json.RawMessage, len(blocked))
	for index := range blocked {
		encoded, err := json.Marshal(blocked[index])
		require.NoError(t, err)
		documents[index] = encoded
	}
	request := examAttemptBrowserAppendRequest{Generation: 1, PolicyRevisionID: revisionID, PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		ContinuityCredential: model.NewCredentialToken(), ParticipationID: model.NewAttemptParticipationID().String(),
		SourceSessionID: "018f47a0-6e53-4cc4-9d0b-97c9b6d98011", Events: documents}
	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	_, events, err := decodeExamAttemptBrowserAppendRequest(encoded)
	require.NoError(t, err, "decodeExamAttemptBrowserAppendRequest() events")
	require.Len(t, events, 3, "decodeExamAttemptBrowserAppendRequest() events")
	require.NotNil(t, events[0].Location, "decodeExamAttemptBrowserAppendRequest() events")
	require.Equal(t, "http", events[0].Location.Scheme, "decodeExamAttemptBrowserAppendRequest() events")
	require.NotNil(t, events[1].Location, "decodeExamAttemptBrowserAppendRequest() events")
	require.Equal(t, "file", events[1].Location.Scheme, "decodeExamAttemptBrowserAppendRequest() events")
	require.NotNil(t, events[2].Location, "decodeExamAttemptBrowserAppendRequest() events")
	require.Equal(t, model.BrowserLocation{}, *events[2].Location, "decodeExamAttemptBrowserAppendRequest() events")

	unsafe := blocked[1]
	unsafe.Location = json.RawMessage(`{"scheme":"javascript","host":"","path":"/alert(secret)"}`)
	unsafeDocument, err := json.Marshal(unsafe)
	require.NoError(t, err)
	request.Events = []json.RawMessage{unsafeDocument}
	encoded, err = json.Marshal(request)
	require.NoError(t, err)
	_, _, err = decodeExamAttemptBrowserAppendRequest(encoded)
	require.Error(t, err, "decodeExamAttemptBrowserAppendRequest() accepted a retained hostless-scheme payload")

	invalidLocations := []struct {
		eventIndex int
		location   json.RawMessage
	}{
		{2, json.RawMessage(`{}`)},
		{2, json.RawMessage(`{"scheme":"file"}`)},
		{2, json.RawMessage(`{"scheme":"","host":""}`)},
		{2, json.RawMessage(`{"scheme":null,"host":"","path":""}`)},
		{0, json.RawMessage(`{"scheme":"http","host":"example.edu","port":null,"path":"/"}`)},
		{0, json.RawMessage(`{"scheme":"http","host":"example.edu","port":"","path":"/"}`)},
	}
	for _, test := range invalidLocations {
		t.Run(fmt.Sprintf("eventIndex=%v", test.eventIndex), func(t *testing.T) {
			incomplete := blocked[test.eventIndex]
			incomplete.Location = test.location
			incompleteDocument, marshalErr := json.Marshal(incomplete)
			require.NoError(t, marshalErr)
			request.Events = []json.RawMessage{incompleteDocument}
			encoded, marshalErr = json.Marshal(request)
			require.NoError(t, marshalErr)
			_, _, err = decodeExamAttemptBrowserAppendRequest(encoded)
			require.Error(t, err, "decodeExamAttemptBrowserAppendRequest() accepted incomplete location %s", test.location)
		})
	}
}

func TestConnectionRuntimeRenewalUsesStrictPayloadAndSafeConnectionLossMessage(t *testing.T) {
	t.Parallel()
	application := &inboundTestApplication{}
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.handleRequest(context.Background(), &Request{Sequence: 50, Action: examAttemptRenewAction,
		Data: json.RawMessage(`{"generation":1,"sequence":1,"sequence":2,"continuity_credential":"bad"}`)})
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "websocket.request.invalid", response.Error.Code)
	application.renewErr = apperror.New("exam.attempt.connection_lost")
	runtime.attempt = &examAttemptBinding{attemptID: model.NewExamAttemptID(), participationID: model.NewAttemptParticipationID(),
		connectionID: model.NewAttemptConnectionID(), generation: 1}
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 51, examAttemptRenewAction, examAttemptRenewRequest{SecurityCoverage: renewalCoverageFixture(),
		Generation: 1, Sequence: 2, ContinuityCredential: credential,
	}))
	response = nextInboundResponse(t, runtime)
	require.Equal(t, "error", response.Status)
	require.NotNil(t, response.Error)
	require.Equal(t, "exam.attempt.connection_lost", response.Error.Code)
	require.Equal(t, "Secure connectivity could not be renewed. Ask a manager to re-allow access.", response.Error.Message)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), credential)
	require.NotContains(t, string(encoded), model.HashToken(credential))
}

func TestConnectionRuntimeSubmitsBoundFocusLossAndClearsPolicySuspendedBinding(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	sittingID := model.NewExamSittingID()
	examAttempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), sittingID, model.NewUserID(), model.NewExamRevisionID(), at)
	require.NoError(t, err)
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), examAttempt.ID, at)
	require.NoError(t, err)
	participationID, connectionID := model.NewAttemptParticipationID(), model.NewAttemptConnectionID()
	connection, err := model.NewAttemptConnection(connectionID, examAttempt.ID, participationID, model.NewSessionID(), at)
	require.NoError(t, err)
	receivedAt := at.Add(2 * time.Second)
	application := &inboundTestApplication{connectResult: attempt.ConnectionResult{Attempt: *examAttempt, Workspace: *workspace,
		Participation: store.ExamAttemptParticipationView{ID: participationID, AttemptID: examAttempt.ID,
			State: model.AttemptParticipationActive, Generation: 4, StartedAt: at, UpdatedAt: at,
			LeaseExpiresAt: at.Add(model.AttemptParticipationInitialLease)}, Connection: *connection, ClassID: model.NewClassID()},
		focusLossResult: attempt.FocusLossEvaluation{AttemptID: examAttempt.ID, ParticipationID: participationID,
			Generation: 4, AcceptedSequence: 3, ReceivedAt: receivedAt, Qualified: true,
			FlagCreated: true, SuspensionCreated: true, ConnectionClosed: true}}
	application.connectResult.Security = inboundSecurityFixture(t, application.connectResult)
	runtime := newInboundRuntime(application, newInboundTestSocket(), newRuntimeTestClock(at))
	runtime.principal.UserID, runtime.principal.SessionID = examAttempt.CandidateUserID, connection.SessionID
	credential := model.NewCredentialToken()
	runtime.handleRequest(context.Background(), requestWithData(t, 60, examAttemptConnectAction,
		validExamAttemptConnectRequest(t, sittingID, "connect", credential)))
	require.Equal(t, "ok", nextInboundResponse(t, runtime).Status)
	runtime.handleRequest(context.Background(), requestWithData(t, 61, examAttemptFocusLossAction, examAttemptFocusLossRequest{
		SchemaVersion: model.FocusLossSignalSchemaVersion,
		Generation:    4, Sequence: 3, DurationMilliseconds: 2500, Source: "fullscreen_exited",
		ContinuityCredential: credential,
	}))
	response := nextInboundResponse(t, runtime)
	require.Equal(t, "ok", response.Status)
	require.Nil(t, response.Error)
	var body examAttemptFocusLossResponse
	err = json.Unmarshal(response.Data, &body)
	require.NoError(t, err, "Focus Loss body=%#v error", body)
	require.Equal(t, int64(4), body.Generation, "Focus Loss body=%#v error", body)
	require.Equal(t, int64(3), body.AcceptedSequence, "Focus Loss body=%#v error", body)
	require.Equal(t, receivedAt.Format(time.RFC3339Nano), body.ReceivedAt, "Focus Loss body=%#v error", body)
	require.True(t, body.SuspensionCreated, "Focus Loss body=%#v error", body)
	encoded := string(response.Data)
	for _, forbidden := range []string{credential, model.HashToken(credential), "duration", "source", "outcome", "threshold", "qualified", "flag_created", "session"} {
		require.NotContains(t, strings.ToLower(encoded), strings.ToLower(forbidden), "Focus Loss response exposed")
	}
	application.mu.Lock()
	calls := append([]attempt.FocusLossCommand(nil), application.focusLossCalls...)
	application.mu.Unlock()
	require.Len(t, calls, 1)
	require.Equal(t, model.FocusLossSignalSchemaVersion, calls[0].SchemaVersion)
	require.Equal(t, examAttempt.ID, calls[0].AttemptID)
	require.Equal(t, participationID, calls[0].ParticipationID)
	require.Equal(t, connectionID, calls[0].ConnectionID)
	require.Equal(t, int64(4), calls[0].Generation)
	require.Equal(t, int64(3), calls[0].Sequence)
	require.Equal(t, int64(2500), calls[0].DurationMilliseconds)
	require.Equal(t, model.FocusLossSourceFullscreenExited, calls[0].Source)
	require.Equal(t, credential, calls[0].ContinuityCredential)

	runtime.handleRequest(context.Background(), requestWithData(t, 62, examAttemptRenewAction, examAttemptRenewRequest{SecurityCoverage: renewalCoverageFixture(),
		Generation: 4, Sequence: 4, ContinuityCredential: credential,
	}))
	rejected := nextInboundResponse(t, runtime)
	require.Equal(t, "error", rejected.Status)
	require.NotNil(t, rejected.Error)
	require.Equal(t, "exam.attempt.connection_closed", rejected.Error.Code)
	runtime.handleRequest(context.Background(), requestWithData(t, 63, examAttemptFocusLossAction, examAttemptFocusLossRequest{
		SchemaVersion: model.FocusLossSignalSchemaVersion,
		Generation:    4, Sequence: 4, DurationMilliseconds: 2500, ContinuityCredential: credential,
	}))
	rejected = nextInboundResponse(t, runtime)
	require.Equal(t, "error", rejected.Status)
	require.NotNil(t, rejected.Error)
	require.Equal(t, "exam.attempt.connection_closed", rejected.Error.Code)
	runtime.mu.Lock()
	binding, subscriptions := runtime.attempt, len(runtime.subscriptions)
	runtime.mu.Unlock()
	application.mu.Lock()
	defer application.mu.Unlock()
	require.Empty(t, application.renewCalls, "post-suspension renewals")
	require.Len(t, application.focusLossCalls, 1, "post-suspension renewals")
	require.Nil(t, binding, "post-suspension renewals")
	require.Equal(t, 0, subscriptions, "post-suspension renewals")
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
			authorize:   apperror.New("authorization.denied"),
			wantCode:    "authorization.denied",
			wantMessage: "WebSocket subscription denied.",
		},
		{
			name: "authorization failure",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 25, "subscribe", validInboundSubscription())
			},
			authorize:   apperror.New("authorization.unavailable"),
			wantCode:    "authorization.unavailable",
			wantMessage: "WebSocket subscription failed.",
		},
		{
			name: "concealed exam subscription",
			request: func(t *testing.T) *Request {
				return requestWithData(t, 27, "subscribe", validInboundSubscription())
			},
			authorize:   apperror.New("resource.not_found"),
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
			require.Equal(t, "error", response.Status, "response = %#v, want code %q and message", response, test.wantCode)
			require.Equal(t, request.Sequence, response.Sequence, "response = %#v, want code %q and message", response, test.wantCode)
			require.NotNil(t, response.Error, "response = %#v, want code %q and message", response, test.wantCode)
			require.Equal(t, test.wantCode, response.Error.Code, "response = %#v, want code %q and message", response, test.wantCode)
			require.Equal(t, test.wantMessage, response.Error.Message, "response = %#v, want code %q and message", response, test.wantCode)
			runtime.mu.Lock()
			count := len(runtime.subscriptions)
			runtime.mu.Unlock()
			require.Equal(t, 0, count, "failed command retained")
		})
	}
}

func TestConnectionRuntimeRevokesInvalidPrincipal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 12, 19, 0, 0, 0, time.UTC)
	clock := newRuntimeTestClock(now)
	socket := newInboundTestSocket()
	application := &inboundTestApplication{
		validationErr: apperror.New("authentication.session.invalid"),
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
		require.FailNow(t, "invalid principal did not close the connection")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "principal-validation responsibility did not stop")
	}

	socket.mu.Lock()
	code, reason := socket.closeCode, socket.closeReason
	socket.mu.Unlock()
	require.Equal(t, CloseSessionRevoked, code, "close")
	require.Equal(t, "session no longer valid", reason, "close")
	runtime.mu.Lock()
	replayable := runtime.replayable
	runtime.mu.Unlock()
	require.False(t, replayable, "session-revoked connection remained replayable")
	application.mu.Lock()
	validations := append([]model.Principal(nil), application.validations...)
	application.mu.Unlock()
	require.Len(t, validations, 1)
	require.Equal(t, runtime.principal.UserID, validations[0].UserID)
	require.Equal(t, runtime.principal.SessionID, validations[0].SessionID)
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

	require.True(t, runtime.hasSubscription(subscription), "final authorized subscription is not visible to publication")
}

func TestConnectionRuntimeJoinsSimultaneousInboundTermination(t *testing.T) {
	t.Parallel()

	for iteration := range 20 {
		now := time.Date(2026, time.August, 12, 20, iteration, 0, 0, time.UTC)
		clock := newRuntimeTestClock(now)
		socket := newInboundTestSocket()
		runtime := newInboundRuntime(
			&inboundTestApplication{validationErr: apperror.New("authentication.session.invalid")},
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
			require.FailNow(t, fmt.Sprintf("iteration %d: runtime did not begin reading", iteration))
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
			require.FailNow(t, fmt.Sprintf("iteration %d: simultaneous termination leaked a responsibility", iteration))
		}
	}
}

var _ Application = (*inboundTestApplication)(nil)
var _ connectionSocket = (*inboundTestSocket)(nil)

func validExamAttemptConnectRequest(t *testing.T, sittingID model.ExamSittingID, key, credential string) examAttemptConnectRequest {
	t.Helper()
	manifest := model.CurrentAttemptConfigurationManifestFingerprint()
	configuration := model.AttemptConfigurationCandidate{ManifestFingerprint: manifest, RegistryFingerprint: "fnv1a64:" + strings.Repeat("b", 16), UserSettingsRevision: model.NewUserSettingsRevision(), DesktopBuild: "test-build", DesktopTarget: "darwin-arm64", Presentation: model.AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}
	err := configuration.Validate()
	require.NoError(t, err)
	return examAttemptConnectRequest{Security: model.ConnectSecurity{Kind: "preflight", PreflightID: "fixture-preflight", ReportDigest: model.SHA256Fingerprint([]byte("fixture-report"))}, ExamSittingID: sittingID.String(), IdempotencyKey: key,
		ContinuityCredential: credential, ConfigurationManifestFingerprint: manifest,
		InitialConfiguration: &configuration}
}

func syntheticNativeAgreement(t *testing.T, build model.DesktopBuildTuple) *model.DesktopNativeAgreement {
	t.Helper()
	digest := model.SHA256Fingerprint([]byte("synthetic-source-schema"))
	definitions := []model.NativeCoverageDefinition{}
	matrix := model.NativeCapabilityMatrix{RegistryDigest: model.NativeRegistryDigest, MatrixID: build.CapabilityMatrixIdentity, ReleaseID: "synthetic-release", TargetTuple: build.DesktopTarget}
	for _, item := range []struct {
		key    string
		source model.NativeSourceID
	}{{"baseline.capture", model.NativeSourceCapture}, {"baseline.display", model.NativeSourceDisplay}, {"baseline.window", model.NativeSourceWindow}} {
		definitions = append(definitions, model.NativeCoverageDefinition{CoverageKey: item.key, CapabilityID: "baseline", SourceID: item.source, SourceSchemaDigest: digest, PermittedClaims: []model.NativeCapabilityClaim{model.NativeClaimEnforce}, RequiredPermissions: []string{}, Baseline: true})
		matrix.Entries = append(matrix.Entries, model.NativeCapabilityMatrixEntry{CoverageKey: item.key, SourceSchemaDigest: digest, Claim: model.NativeClaimEnforce, ComponentID: "synthetic-component", AdapterVersion: "synthetic-adapter", RequiredPermissions: []string{}, Limitations: []string{}, Verification: "passed"})
	}
	agreement, err := model.NewDesktopNativeAgreement(model.NativeRegistryDigest, model.SHA256Fingerprint([]byte("synthetic-manifest")), model.SHA256Fingerprint([]byte("synthetic-matrix")), model.SHA256Fingerprint([]byte("synthetic-detectors")), definitions, matrix, []model.NativeDetectorDefinition{})
	require.NoError(t, err)
	return agreement
}

func inboundSecurityFixture(t *testing.T, result attempt.ConnectionResult) model.AdmittedSecurity {
	t.Helper()
	manifest := model.EmptyAttemptConfigurationManifest()
	build := model.DesktopBuildTuple{DesktopRelease: "1.0.0", DesktopBuildID: "synthetic-build", Platform: model.DesktopPlatformDarwin, Architecture: model.DesktopArchitectureARM64, RealtimeProtocol: 1, AttemptConfigurationManifestFingerprint: manifest.Fingerprint(), DesktopSettingsRegistryFingerprint: "fnv1a64:aaaaaaaaaaaaaaaa", ConfigurationManifest: manifest, DesktopTarget: "darwin-arm64", CapabilityMatrixIdentity: "synthetic-matrix"}
	build.NativeAgreement = syntheticNativeAgreement(t, build)
	resolved, err := model.ResolveNativePolicy(model.NativePolicyResolution{PolicyID: "fixture-policy", Revision: result.Attempt.AdmissionRevisionID.String(), Ordinal: result.Participation.Generation, InstitutionID: model.NewInstitutionID(), ExamRevisionID: result.Attempt.AdmissionRevisionID, SittingID: result.Attempt.SittingID, Scope: model.SecurityPolicyScope{Kind: "admission", AdmissionScopeID: "fixture-preflight"}, IssuedAt: result.Participation.StartedAt, ActiveFrom: result.Participation.StartedAt, ActivationTime: result.Participation.StartedAt, Selections: model.DefaultExamPolicySet(), Build: build})
	require.NoError(t, err)
	policy, err := resolved.Policy.RebindAttempt(result.Attempt.ID, result.Participation.StartedAt)
	require.NoError(t, err)
	return model.AdmittedSecurity{Policy: policy, PolicyContentDigest: resolved.PolicyContentDigest, PreflightID: "fixture-preflight", PreflightReportDigest: model.SHA256Fingerprint([]byte("fixture-report")), PreflightPolicyDigest: resolved.Policy.Digest, ParticipationID: result.Participation.ID, Generation: result.Participation.Generation, SecuritySessionID: "fixture-security-session", DeliveryStreamID: "fixture-delivery", RenewalIntervalSeconds: 5}
}

func (a *inboundTestApplication) UpdateExamSecurityCoverage(context.Context, invocation.Invocation, attempt.UpdateSecurityCoverageCommand) (model.SecurityCoverageResult, error) {
	return model.SecurityCoverageResult{}, errors.New("unexpected security update")
}

func renewalCoverageFixture() model.SecurityCoverageRenewal {
	return model.SecurityCoverageRenewal{ControlSequence: 1, PolicyDigest: model.SHA256Fingerprint([]byte("renewal-policy")), SecuritySessionID: model.NewID(), StreamID: model.NewID(), Posture: "compliant", Sources: []model.NativeSourceCoverage{}, Coverage: []model.NativeCoverageClaim{}, SourceResets: []model.NativeSourceReset{}, DeliveryWatermarks: []model.DeliveryWatermark{}}
}
func renewalCoverageResultFixture() model.SecurityCoverageResult {
	digest := model.SHA256Fingerprint([]byte("renewal-control"))
	return model.SecurityCoverageResult{ProcessedControlSequence: 1, ProcessedControlDigest: &digest, CoverageResult: "accepted", SourceResetReceipts: []model.SourceResetReceipt{}, SecurityInteractionAllowed: true, DeliveryWatermarkRejections: []model.DeliveryWatermarkRejection{}}
}

type deliveryRecoveryTestError struct {
	error
	value *model.DeliveryRecovery
}

func (e *deliveryRecoveryTestError) DeliveryRecovery() *model.DeliveryRecovery { return e.value }
func TestDeliveryErrorHasBoundedPrivateRecoveryAndRetryDelay(t *testing.T) {
	for _, name := range []string{"valid", "invalid", "unrelated"} {
		t.Run(name, func(t *testing.T) {
			at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(at))
			state := &model.DeliveryRecovery{Family: "browser", Budget: model.DeliveryBudgetSnapshot{ParticipationID: model.NewAttemptParticipationID(), Generation: 1, ServerTime: at, Native: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(true, false), Attempt: model.NewDeliveryQuotaUsage(true, true), PendingByteLimit: model.DeliveryPendingByteLimit}, Browser: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(false, false), Attempt: model.NewDeliveryQuotaUsage(false, true), PendingByteLimit: model.DeliveryPendingByteLimit}}}
			code := apperror.Code("exam.delivery.pending_capacity")
			if name == "invalid" {
				state.Budget.Native.PendingBytes = -1
			}
			if name == "unrelated" {
				code = "exam.attempt.not_found"
			}
			runtime.enqueueDeliveryError(3, code, apperror.New(code).Wrap(&deliveryRecoveryTestError{error: errors.New("private-native-payload"), value: state}))
			response := nextInboundResponse(t, runtime)
			require.NotNil(t, response.Error, "invalid delivery error extension")
			require.Equal(t, int64(3), response.Sequence, "invalid delivery error extension")
			require.Equal(t, name == "valid", response.Error.Delivery != nil, "invalid delivery error extension")
			if name != "unrelated" {
				require.Equal(t, 1, response.Error.RetryAfterSeconds, "missing retry delay")
			}
			raw, err := json.Marshal(response)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private-native-payload", "private cause leaked")
		})
	}
}

// A transport-level blocked append must not monopolize the renewal reader.
type blockedAppendApplication struct {
	*inboundTestApplication
	entered  chan struct{}
	finished chan struct{}
}

func (a *blockedAppendApplication) AppendExamAttemptBrowserActivity(ctx context.Context, _ invocation.Invocation, _ attempt.AppendBrowserActivityCommand) (model.BrowserActivityAcknowledgement, error) {
	close(a.entered)
	<-ctx.Done()
	close(a.finished)
	return model.BrowserActivityAcknowledgement{}, ctx.Err()
}

func TestConnectionRuntimeAppendBackpressurePreservesRenewalAndCancelsWorker(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.September, 8, 1, 0, 0, 0, time.UTC)
	socket := newInboundTestSocket()
	application := &blockedAppendApplication{inboundTestApplication: &inboundTestApplication{}, entered: make(chan struct{}), finished: make(chan struct{})}
	runtime := newInboundRuntime(application.inboundTestApplication, socket, newRuntimeTestClock(at))
	runtime.application = application
	attemptID, participationID := model.NewExamAttemptID(), model.NewAttemptParticipationID()
	runtime.attempt = &examAttemptBinding{attemptID: attemptID, participationID: participationID, connectionID: model.NewAttemptConnectionID(), generation: 1}
	application.renewResult = attempt.ParticipationRenewal{SecurityCoverage: renewalCoverageResultFixture(), AttemptID: attemptID,
		ParticipationID: participationID, Generation: 1, AcceptedSequence: 1, DatabaseTime: at, LeaseExpiresAt: at.Add(model.AttemptParticipationInitialLease)}
	credential, revision := model.NewCredentialToken(), model.NewExamRevisionID().String()
	event, err := json.Marshal(examAttemptBrowserEventRequest{Sequence: 1, Kind: string(model.BrowserActivityOpened), PolicyRevisionID: revision, ClientOccurredAt: at.Format(time.RFC3339Nano)})
	require.NoError(t, err)
	body := examAttemptBrowserAppendRequest{Generation: 1, PolicyRevisionID: revision, PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		ContinuityCredential: credential, ParticipationID: participationID.String(), SourceSessionID: "018f47a0-6e53-4cc4-9d0b-97c9b6d98011", Events: []json.RawMessage{event}}
	done := make(chan struct{})
	go func() { runtime.readPump(context.Background()); close(done) }()
	t.Cleanup(func() {
		_ = socket.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			assert.Fail(t, "append worker did not stop")
		}
	})
	socket.reads <- inboundReadResult{request: *requestWithData(t, 1, examAttemptBrowserAppendAction, body)}
	select {
	case <-application.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "append did not enter application")
	}
	// The second request occupies the single waiting slot; the third is refused.
	socket.reads <- inboundReadResult{request: *requestWithData(t, 2, examAttemptBrowserAppendAction, body)}
	socket.reads <- inboundReadResult{request: *requestWithData(t, 3, examAttemptBrowserAppendAction, body)}
	socket.reads <- inboundReadResult{request: *requestWithData(t, 4, examAttemptRenewAction, examAttemptRenewRequest{Generation: 1, Sequence: 1, ContinuityCredential: credential, SecurityCoverage: renewalCoverageFixture()})}
	refused := nextInboundResponse(t, runtime)
	require.Equal(t, int64(3), refused.Sequence)
	require.NotNil(t, refused.Error)
	require.Equal(t, "exam.delivery.append_rate_limited", refused.Error.Code)
	require.Equal(t, 1, refused.Error.RetryAfterSeconds)
	require.Nil(t, refused.Error.Delivery)
	renewed := nextInboundResponse(t, runtime)
	require.Equal(t, int64(4), renewed.Sequence)
	require.Equal(t, "ok", renewed.Status)
	_ = socket.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "reader failed to join canceled append")
	}
	select {
	case <-application.finished:
	default:
		require.FailNow(t, "reader returned before append cancellation completed")
	}
}

func TestControlRateRefusalHasRetryAdviceWithoutClosingBinding(t *testing.T) {
	runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	binding := &examAttemptBinding{attemptID: model.NewExamAttemptID()}
	runtime.attempt = binding
	runtime.enqueueError(77, "exam.delivery.control_rate_limited", websocketErrorAttemptRenewalFailed)
	response := nextInboundResponse(t, runtime)
	require.Equal(t, int64(77), response.Sequence, "control retry refusal changed authority or lost retry advice")
	require.NotNil(t, response.Error, "control retry refusal changed authority or lost retry advice")
	require.Equal(t, 1, response.Error.RetryAfterSeconds, "control retry refusal changed authority or lost retry advice")
	require.Nil(t, response.Error.Delivery, "control retry refusal changed authority or lost retry advice")
	require.Same(t, binding, runtime.attempt, "control retry refusal changed authority or lost retry advice")
}
