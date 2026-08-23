// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket connection and router flow. See server/NOTICE for exact
// provenance.

package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func (c *connectionRuntime) readPump(ctx context.Context) {
	c.socket.SetReadLimit(MaxMessageBytes)
	_ = c.socket.SetReadDeadline(c.clock.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(c.clock.Now().Add(pongWait))
	})
	for {
		var request Request
		if err := c.socket.ReadJSON(&request); err != nil {
			if c.recorder != nil {
				c.recorder.ObserveWebSocketMessage("inbound", "request", streamResult(err), 0)
			}
			return
		}
		if err := request.Validate(); err != nil {
			if c.recorder != nil {
				c.recorder.ObserveWebSocketMessage("inbound", boundedWebSocketAction(request.Action), "invalid", len(request.Data))
			}
			c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorRequestInvalid)
			continue
		}
		if c.recorder != nil {
			c.recorder.ObserveWebSocketMessage("inbound", boundedWebSocketAction(request.Action), "accepted", len(request.Data))
		}
		c.handleRequest(ctx, &request)
	}
}

func (c *connectionRuntime) handleExamAttemptTerminalOpen(ctx context.Context, request *Request) {
	decoded, err := decodeStrictExamAttemptObject[examAttemptTerminalOpenRequest](request.Data, "Exam Attempt terminal open request", 4)
	if err != nil || decoded.Generation < 1 || decoded.Cols < 1 || decoded.Rows < 1 ||
		!model.IsValidCredentialToken(decoded.ContinuityCredential) {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorTerminalOpenRequestInvalid)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || c.attempt.generation != decoded.Generation {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	if c.terminal != nil {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.terminal_conflict", websocketErrorTerminalAlreadyOpen)
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.terminal_unavailable", websocketErrorTerminalUnavailable)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	terminal, err := attempts.OpenCandidateExamTerminal(ctx, app.NewInvocation(c.principal, metadata), app.OpenCandidateExamTerminalCommand{
		Access: app.CandidateExamAttemptAccess{AttemptID: binding.attemptID, ConnectionID: binding.connectionID,
			ContinuityCredential: decoded.ContinuityCredential},
		SittingID: binding.sittingID, ClassID: binding.classID,
		ParticipationID: binding.participationID, Generation: binding.generation,
		Window: app.CandidateExamTerminalWindow{Cols: decoded.Cols, Rows: decoded.Rows},
	})
	if err != nil {
		code := "exam.attempt.terminal_unavailable"
		if failure, exists := app.As(err); exists {
			code = failure.Code()
		}
		c.enqueueError(request.Sequence, code, websocketErrorTerminalOpenFailed)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || *c.attempt != binding || c.terminal != nil {
		c.mu.Unlock()
		_ = terminal.Close()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	c.terminal = terminal
	c.mu.Unlock()
	c.enqueueResponse(request.Sequence, json.RawMessage(`{"opened":true}`))
	go c.readExamAttemptTerminal(terminal)
}

func (c *connectionRuntime) handleExamAttemptTerminalInput(_ context.Context, request *Request) {
	decoded, err := decodeStrictExamAttemptObject[examAttemptTerminalInputRequest](request.Data, "Exam Attempt terminal input request", 1)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorTerminalInputInvalid)
		return
	}
	data, err := base64.StdEncoding.DecodeString(decoded.Data)
	if err != nil || len(data) == 0 || len(data) > examAttemptTerminalChunkMaximum {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorTerminalInputInvalid)
		return
	}
	c.mu.Lock()
	terminal := c.terminal
	c.mu.Unlock()
	if terminal == nil {
		c.enqueueError(request.Sequence, "exam.attempt.terminal_closed", websocketErrorTerminalNotOpen)
		return
	}
	for len(data) > 0 {
		written, writeErr := terminal.Write(data)
		if writeErr != nil || written < 1 || written > len(data) {
			c.closeTerminal()
			c.enqueueError(request.Sequence, "exam.attempt.terminal_unavailable", websocketErrorTerminalInputFailed)
			return
		}
		data = data[written:]
	}
	c.enqueueResponse(request.Sequence, nil)
}

func (c *connectionRuntime) handleExamAttemptTerminalResize(ctx context.Context, request *Request) {
	decoded, err := decodeStrictExamAttemptObject[examAttemptTerminalResizeRequest](request.Data, "Exam Attempt terminal resize request", 2)
	if err != nil || decoded.Cols < 1 || decoded.Rows < 1 {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorTerminalSizeInvalid)
		return
	}
	c.mu.Lock()
	terminal := c.terminal
	c.mu.Unlock()
	if terminal == nil {
		c.enqueueError(request.Sequence, "exam.attempt.terminal_closed", websocketErrorTerminalNotOpen)
		return
	}
	if err := terminal.Resize(ctx, app.CandidateExamTerminalWindow{Cols: decoded.Cols, Rows: decoded.Rows}); err != nil {
		c.closeTerminal()
		c.enqueueError(request.Sequence, "exam.attempt.terminal_unavailable", websocketErrorTerminalResizeFailed)
		return
	}
	c.enqueueResponse(request.Sequence, nil)
}

func (c *connectionRuntime) handleExamAttemptTerminalClose(request *Request) {
	if _, err := decodeStrictExamAttemptObject[struct{}](request.Data, "Exam Attempt terminal close request", 0); err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorTerminalCloseRequestInvalid)
		return
	}
	c.closeTerminal()
	c.enqueueResponse(request.Sequence, nil)
}

func (c *connectionRuntime) readExamAttemptTerminal(terminal app.CandidateExamTerminal) {
	buffer := make([]byte, examAttemptTerminalChunkMaximum)
	reason := "closed"
	for {
		count, err := terminal.Read(buffer)
		if count > 0 {
			data, marshalErr := json.Marshal(examAttemptTerminalOutput{Data: base64.StdEncoding.EncodeToString(buffer[:count])})
			if marshalErr != nil {
				reason = "unavailable"
				break
			}
			c.enqueueEphemeralEvent(&Event{Id: model.NewId(), Event: examAttemptTerminalOutputEvent,
				UserID: c.principal.UserID.String(), Data: data})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				reason = "unavailable"
			}
			break
		}
		if count == 0 {
			reason = "unavailable"
			break
		}
	}
	c.mu.Lock()
	if c.terminal == terminal {
		c.terminal = nil
	}
	c.mu.Unlock()
	_ = terminal.Close()
	data, _ := json.Marshal(examAttemptTerminalClosed{Reason: reason})
	c.enqueueEphemeralEvent(&Event{Id: model.NewId(), Event: examAttemptTerminalClosedEvent,
		UserID: c.principal.UserID.String(), Data: data})
}

func (c *connectionRuntime) closeTerminal() {
	c.mu.Lock()
	terminal := c.terminal
	c.terminal = nil
	c.mu.Unlock()
	if terminal != nil {
		_ = terminal.Close()
	}
}

func (c *connectionRuntime) sessionPump(ctx context.Context) {
	ticker := c.clock.NewTicker(sessionCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.closeTransport()
			return
		case <-ticker.Chan():
			if appErr := c.application.ValidateWebSocketPrincipal(
				ctx,
				c.principal,
			); appErr != nil {
				c.close(CloseSessionRevoked, localizedCloseReason(c.localizer, c.locale, websocketCloseMessages["session_revoked"]), false)
				return
			}
		}
	}
}

func (c *connectionRuntime) handleRequest(
	ctx context.Context,
	request *Request,
) {
	switch request.Action {
	case "ping":
		c.enqueueResponse(request.Sequence, json.RawMessage(`{"pong":true}`))
	case "subscribe":
		var subscription Subscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", websocketErrorSubscriptionInvalid)
			return
		}
		metadata := c.metadata
		metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
		if err := c.application.AuthorizeWebSocketSubscription(
			ctx,
			c.principal,
			metadata,
			subscription.Action,
			subscription.Resource.model(),
		); err != nil {
			code := "authorization.denied"
			presentation := websocketErrorSubscriptionDenied
			if failure, ok := app.As(err); ok {
				code = failure.Code()
				if code != "authorization.denied" {
					presentation = websocketErrorSubscriptionFailed
				}
			}
			c.enqueueError(request.Sequence, code, presentation)
			return
		}
		c.mu.Lock()
		_, existed := c.subscriptions[subscription.Key()]
		if !existed &&
			len(c.subscriptions) >= maximumSubscriptions {
			c.mu.Unlock()
			c.enqueueError(
				request.Sequence,
				"websocket.subscription.limit",
				websocketErrorSubscriptionLimit,
			)
			return
		}
		c.subscriptions[subscription.Key()] = subscription
		c.mu.Unlock()
		if !existed && c.recorder != nil {
			c.recorder.AddWebSocketSubscriptions(1)
		}
		c.enqueueResponse(request.Sequence, nil)
	case "unsubscribe":
		var subscription Subscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", websocketErrorSubscriptionInvalid)
			return
		}
		c.mu.Lock()
		_, existed := c.subscriptions[subscription.Key()]
		if c.attempt == nil || subscription != c.examAttemptSubscriptionLocked() {
			delete(c.subscriptions, subscription.Key())
		} else {
			existed = false
		}
		c.mu.Unlock()
		if existed && c.recorder != nil {
			c.recorder.AddWebSocketSubscriptions(-1)
		}
		c.enqueueResponse(request.Sequence, nil)
	case examAttemptConnectAction:
		c.handleExamAttemptConnect(ctx, request)
	case examAttemptRenewAction:
		c.handleExamAttemptRenew(ctx, request)
	case examAttemptFocusLossAction:
		c.handleExamAttemptFocusLoss(ctx, request)
	case examAttemptTerminalOpenAction:
		c.handleExamAttemptTerminalOpen(ctx, request)
	case examAttemptTerminalInputAction:
		c.handleExamAttemptTerminalInput(ctx, request)
	case examAttemptTerminalResizeAction:
		c.handleExamAttemptTerminalResize(ctx, request)
	case examAttemptTerminalCloseAction:
		c.handleExamAttemptTerminalClose(request)
	default:
		c.enqueueError(request.Sequence, "websocket.action.unknown", websocketErrorActionUnknown)
	}
}

func (c *connectionRuntime) handleExamAttemptConnect(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptConnectRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorAttemptConnectRequestInvalid)
		return
	}
	sittingID, _ := model.ParseExamSittingID(decoded.ExamSittingID)
	requestHash := sha256.Sum256([]byte(decoded.ExamSittingID + "\x00" + decoded.IdempotencyKey + "\x00" + decoded.ContinuityCredential))
	c.mu.Lock()
	if c.attempt != nil && (c.attempt.sittingID != sittingID || c.attempt.requestHash != requestHash) {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.already_connected", websocketErrorAttemptAlreadyConnected)
		return
	}
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptConnectionFailed)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.ConnectExamAttempt(ctx, app.NewInvocation(c.principal, metadata), app.ConnectExamAttemptCommand{
		SittingID: sittingID, ContinuityCredential: decoded.ContinuityCredential, IdempotencyKey: decoded.IdempotencyKey,
	})
	if err != nil {
		code, presentation := examAttemptConnectError(err)
		c.enqueueError(request.Sequence, code, presentation)
		return
	}
	if result.Connection.State != model.AttemptConnectionOpen || result.Attempt.SittingID != sittingID ||
		result.Connection.AttemptID != result.Attempt.ID || result.Connection.ParticipationID != result.Participation.ID {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptConnectionFailed)
		return
	}
	binding := &examAttemptBinding{attemptID: result.Attempt.ID, sittingID: sittingID,
		classID: result.ClassID, connectionID: result.Connection.ID, participationID: result.Participation.ID,
		generation: result.Participation.Generation, requestHash: requestHash}
	subscription := Subscription{Action: model.ActionExamSittingParticipate,
		Resource: Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}}
	c.mu.Lock()
	if c.attempt != nil && *c.attempt != *binding {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.already_connected", websocketErrorAttemptAlreadyConnected)
		return
	}
	_, subscriptionExisted := c.subscriptions[subscription.Key()]
	c.attempt = binding
	c.subscriptions[subscription.Key()] = subscription
	c.mu.Unlock()
	if !subscriptionExisted && c.recorder != nil {
		c.recorder.AddWebSocketSubscriptions(1)
	}
	encoded, err := json.Marshal(examAttemptConnectResponse{
		AttemptID: result.Attempt.ID.String(), WorkspaceID: result.Workspace.ID.String(),
		ParticipationID: result.Participation.ID.String(), AttemptConnectionID: result.Connection.ID.String(),
		Generation:             result.Participation.Generation,
		RenewalIntervalSeconds: int64(model.AttemptParticipationRenewalInterval / time.Second),
		StartedAt:              result.Participation.StartedAt.Format(time.RFC3339Nano),
		LeaseExpiresAt:         result.Participation.LeaseExpiresAt.Format(time.RFC3339Nano),
		FirstAdmission:         result.FirstAdmission, Replayed: result.Replayed,
	})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptConnectionFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func boundedWebSocketAction(action string) string {
	switch action {
	case "ping", "subscribe", "unsubscribe", examAttemptConnectAction, examAttemptRenewAction,
		examAttemptFocusLossAction, examAttemptTerminalOpenAction, examAttemptTerminalInputAction,
		examAttemptTerminalResizeAction, examAttemptTerminalCloseAction:
		return action
	default:
		return "unknown"
	}
}

func streamResult(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, io.EOF):
		return "closed"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

func (c *connectionRuntime) handleExamAttemptRenew(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptRenewRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorAttemptRenewalRequestInvalid)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptRenewalFailed)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.RenewExamAttemptParticipation(ctx, app.NewInvocation(c.principal, metadata), app.RenewExamAttemptParticipationCommand{
		AttemptID: binding.attemptID, ParticipationID: binding.participationID, ConnectionID: binding.connectionID,
		Generation: decoded.Generation, Sequence: decoded.Sequence, ContinuityCredential: decoded.ContinuityCredential,
	})
	if err != nil {
		code, presentation := examAttemptRenewError(err)
		c.enqueueError(request.Sequence, code, presentation)
		return
	}
	if result.AttemptID != binding.attemptID || result.ParticipationID != binding.participationID ||
		result.Generation != binding.generation || result.AcceptedSequence != decoded.Sequence || result.DatabaseTime.IsZero() ||
		result.LeaseExpiresAt.IsZero() {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptRenewalFailed)
		return
	}
	encoded, err := json.Marshal(examAttemptRenewResponse{Generation: result.Generation, AcceptedSequence: result.AcceptedSequence,
		DatabaseTime: result.DatabaseTime.Format(time.RFC3339Nano), LeaseExpiresAt: result.LeaseExpiresAt.Format(time.RFC3339Nano), Duplicate: result.Duplicate})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptRenewalFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func (c *connectionRuntime) handleExamAttemptFocusLoss(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptFocusLossRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorFocusLossSignalInvalid)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorFocusLossFailed)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.EvaluateExamAttemptFocusLoss(ctx, app.NewInvocation(c.principal, metadata),
		app.EvaluateExamAttemptFocusLossCommand{SchemaVersion: decoded.SchemaVersion,
			AttemptID: binding.attemptID, ParticipationID: binding.participationID,
			ConnectionID: binding.connectionID, Generation: decoded.Generation, Sequence: decoded.Sequence,
			DurationMilliseconds: decoded.DurationMilliseconds, Source: model.FocusLossSource(decoded.Source),
			ContinuityCredential: decoded.ContinuityCredential})
	if err != nil {
		code, presentation := examAttemptFocusLossError(err)
		c.enqueueError(request.Sequence, code, presentation)
		return
	}
	if result.AttemptID != binding.attemptID || result.ParticipationID != binding.participationID ||
		result.Generation != binding.generation || result.AcceptedSequence != decoded.Sequence || result.ReceivedAt.IsZero() ||
		(result.SuspensionCreated && !result.ConnectionClosed) {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorFocusLossFailed)
		return
	}
	encoded, err := json.Marshal(examAttemptFocusLossResponse{Generation: result.Generation,
		AcceptedSequence: result.AcceptedSequence, ReceivedAt: result.ReceivedAt.Format(time.RFC3339Nano),
		Duplicate: result.Duplicate, GapDetected: result.GapDetected, PolicyDisabled: result.PolicyDisabled,
		WarningCreated: result.CandidateWarningCreated, SuspensionCreated: result.SuspensionCreated,
		DiscrepancyRecorded: result.DiscrepancyRecorded})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorFocusLossFailed)
		return
	}
	if result.ConnectionClosed || result.SuspensionCreated {
		c.mu.Lock()
		var terminal app.CandidateExamTerminal
		removedSubscription := false
		if c.attempt != nil && *c.attempt == binding {
			key := c.examAttemptSubscriptionLocked().Key()
			_, removedSubscription = c.subscriptions[key]
			delete(c.subscriptions, key)
			c.attempt = nil
			terminal = c.terminal
			c.terminal = nil
		}
		c.mu.Unlock()
		if removedSubscription && c.recorder != nil {
			c.recorder.AddWebSocketSubscriptions(-1)
		}
		if terminal != nil {
			_ = terminal.Close()
		}
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func (c *connectionRuntime) examAttemptSubscriptionLocked() Subscription {
	if c.attempt == nil {
		return Subscription{}
	}
	return Subscription{Action: model.ActionExamSittingParticipate,
		Resource: Resource{Type: model.ResourceExamSitting, ID: c.attempt.sittingID.String()}}
}

func (c *connectionRuntime) unbindExamAttemptConnection(connectionID model.AttemptConnectionID) bool {
	c.mu.Lock()
	if c.attempt == nil || c.attempt.connectionID != connectionID {
		c.mu.Unlock()
		return false
	}
	key := c.examAttemptSubscriptionLocked().Key()
	_, removedSubscription := c.subscriptions[key]
	delete(c.subscriptions, key)
	c.attempt = nil
	terminal := c.terminal
	c.terminal = nil
	c.mu.Unlock()
	if removedSubscription && c.recorder != nil {
		c.recorder.AddWebSocketSubscriptions(-1)
	}
	if terminal != nil {
		_ = terminal.Close()
	}
	return true
}

func (c *connectionRuntime) finalizeExamAttempt(ctx context.Context) {
	c.attemptClose.Do(func() {
		c.mu.Lock()
		binding := c.attempt
		terminal := c.terminal
		c.terminal = nil
		removedSubscription := false
		if binding != nil {
			key := c.examAttemptSubscriptionLocked().Key()
			_, removedSubscription = c.subscriptions[key]
			delete(c.subscriptions, key)
			c.attempt = nil
		}
		c.mu.Unlock()
		if removedSubscription && c.recorder != nil {
			c.recorder.AddWebSocketSubscriptions(-1)
		}
		if terminal != nil {
			_ = terminal.Close()
		}
		if binding == nil {
			return
		}
		attempts, ok := c.application.(examAttemptApplication)
		if !ok {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		metadata := c.metadata
		metadata.RequestID = c.id + ":attempt-close"
		_, err := attempts.CloseExamAttemptConnection(closeCtx, app.NewInvocation(c.principal, metadata), app.CloseExamAttemptConnectionCommand{
			AttemptID: binding.attemptID, SittingID: binding.sittingID, ClassID: binding.classID,
			ConnectionID: binding.connectionID, Reason: model.AttemptConnectionCloseTransport,
		})
		if err != nil && c.logger != nil {
			c.logger.WarnContext(closeCtx, "Exam Attempt connection close failed", err)
		}
	})
}

func (c *connectionRuntime) hasSubscription(
	subscription Subscription,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.subscriptions[subscription.Key()]
	return exists
}
