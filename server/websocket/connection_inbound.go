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
	"encoding/json"
	"fmt"
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
			return
		}
		if err := request.Validate(); err != nil {
			c.enqueueError(request.Sequence, "websocket.request.invalid", "Invalid WebSocket request.")
			continue
		}
		c.handleRequest(ctx, &request)
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
				c.close(CloseSessionRevoked, "session no longer valid", false)
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
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", "Invalid subscription.")
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
			message := "WebSocket subscription denied."
			if failure, ok := app.As(err); ok {
				code = failure.Code()
				if code != "authorization.denied" {
					message = "WebSocket subscription failed."
				}
			}
			c.enqueueError(request.Sequence, code, message)
			return
		}
		c.mu.Lock()
		if _, exists := c.subscriptions[subscription.Key()]; !exists &&
			len(c.subscriptions) >= maximumSubscriptions {
			c.mu.Unlock()
			c.enqueueError(
				request.Sequence,
				"websocket.subscription.limit",
				"WebSocket subscription limit reached.",
			)
			return
		}
		c.subscriptions[subscription.Key()] = subscription
		c.mu.Unlock()
		c.enqueueResponse(request.Sequence, nil)
	case "unsubscribe":
		var subscription Subscription
		if err := json.Unmarshal(request.Data, &subscription); err != nil ||
			!subscription.IsValid() {
			c.enqueueError(request.Sequence, "websocket.subscription.invalid", "Invalid subscription.")
			return
		}
		c.mu.Lock()
		if c.attempt == nil || subscription != c.examAttemptSubscriptionLocked() {
			delete(c.subscriptions, subscription.Key())
		}
		c.mu.Unlock()
		c.enqueueResponse(request.Sequence, nil)
	case examAttemptConnectAction:
		c.handleExamAttemptConnect(ctx, request)
	case examAttemptRenewAction:
		c.handleExamAttemptRenew(ctx, request)
	case examAttemptFocusLossAction:
		c.handleExamAttemptFocusLoss(ctx, request)
	default:
		c.enqueueError(request.Sequence, "websocket.action.unknown", "Unknown WebSocket action.")
	}
}

func (c *connectionRuntime) handleExamAttemptConnect(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptConnectRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", "Invalid Exam Attempt connection request.")
		return
	}
	sittingID, _ := model.ParseExamSittingID(decoded.ExamSittingID)
	requestHash := sha256.Sum256([]byte(decoded.ExamSittingID + "\x00" + decoded.IdempotencyKey + "\x00" + decoded.ContinuityCredential))
	c.mu.Lock()
	if c.attempt != nil && (c.attempt.sittingID != sittingID || c.attempt.requestHash != requestHash) {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.already_connected", "Exam Attempt connection is already established.")
		return
	}
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt connection failed.")
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.ConnectExamAttempt(ctx, app.NewInvocation(c.principal, metadata), app.ConnectExamAttemptCommand{
		SittingID: sittingID, ContinuityCredential: decoded.ContinuityCredential, IdempotencyKey: decoded.IdempotencyKey,
	})
	if err != nil {
		code, message := examAttemptConnectError(err)
		c.enqueueError(request.Sequence, code, message)
		return
	}
	if result.Connection.State != model.AttemptConnectionOpen || result.Attempt.SittingID != sittingID ||
		result.Connection.AttemptID != result.Attempt.ID || result.Connection.ParticipationID != result.Participation.ID {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt connection failed.")
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
		c.enqueueError(request.Sequence, "exam.attempt.already_connected", "Exam Attempt connection is already established.")
		return
	}
	c.attempt = binding
	c.subscriptions[subscription.Key()] = subscription
	c.mu.Unlock()
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
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt connection failed.")
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func (c *connectionRuntime) handleExamAttemptRenew(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptRenewRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", "Invalid Exam Attempt renewal request.")
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", "Exam Attempt connection is not active.")
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt renewal failed.")
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.RenewExamAttemptParticipation(ctx, app.NewInvocation(c.principal, metadata), app.RenewExamAttemptParticipationCommand{
		AttemptID: binding.attemptID, ParticipationID: binding.participationID, ConnectionID: binding.connectionID,
		Generation: decoded.Generation, Sequence: decoded.Sequence, ContinuityCredential: decoded.ContinuityCredential,
	})
	if err != nil {
		code, message := examAttemptRenewError(err)
		c.enqueueError(request.Sequence, code, message)
		return
	}
	if result.AttemptID != binding.attemptID || result.ParticipationID != binding.participationID ||
		result.Generation != binding.generation || result.AcceptedSequence != decoded.Sequence || result.DatabaseTime.IsZero() ||
		result.LeaseExpiresAt.IsZero() {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt renewal failed.")
		return
	}
	encoded, err := json.Marshal(examAttemptRenewResponse{Generation: result.Generation, AcceptedSequence: result.AcceptedSequence,
		DatabaseTime: result.DatabaseTime.Format(time.RFC3339Nano), LeaseExpiresAt: result.LeaseExpiresAt.Format(time.RFC3339Nano), Duplicate: result.Duplicate})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Exam Attempt renewal failed.")
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func (c *connectionRuntime) handleExamAttemptFocusLoss(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptFocusLossRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", "Invalid Focus Loss signal.")
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", "Exam Attempt connection is not active.")
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Focus Loss signal could not be accepted.")
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
		code, message := examAttemptFocusLossError(err)
		c.enqueueError(request.Sequence, code, message)
		return
	}
	if result.AttemptID != binding.attemptID || result.ParticipationID != binding.participationID ||
		result.Generation != binding.generation || result.AcceptedSequence != decoded.Sequence || result.ReceivedAt.IsZero() ||
		(result.SuspensionCreated && !result.ConnectionClosed) {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Focus Loss signal could not be accepted.")
		return
	}
	encoded, err := json.Marshal(examAttemptFocusLossResponse{Generation: result.Generation,
		AcceptedSequence: result.AcceptedSequence, ReceivedAt: result.ReceivedAt.Format(time.RFC3339Nano),
		Duplicate: result.Duplicate, GapDetected: result.GapDetected, PolicyDisabled: result.PolicyDisabled,
		WarningCreated: result.CandidateWarningCreated, SuspensionCreated: result.SuspensionCreated,
		DiscrepancyRecorded: result.DiscrepancyRecorded})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", "Focus Loss signal could not be accepted.")
		return
	}
	if result.ConnectionClosed || result.SuspensionCreated {
		c.mu.Lock()
		if c.attempt != nil && *c.attempt == binding {
			delete(c.subscriptions, c.examAttemptSubscriptionLocked().Key())
			c.attempt = nil
		}
		c.mu.Unlock()
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
	defer c.mu.Unlock()
	if c.attempt == nil || c.attempt.connectionID != connectionID {
		return false
	}
	delete(c.subscriptions, c.examAttemptSubscriptionLocked().Key())
	c.attempt = nil
	return true
}

func (c *connectionRuntime) finalizeExamAttempt(ctx context.Context) {
	c.attemptClose.Do(func() {
		c.mu.Lock()
		binding := c.attempt
		if binding != nil {
			delete(c.subscriptions, c.examAttemptSubscriptionLocked().Key())
			c.attempt = nil
		}
		c.mu.Unlock()
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
