// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// This file contains substantially modified code adapted from Mattermost's
// public WebSocket connection and router flow. See server/NOTICE for exact
// provenance.

package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func (c *connectionRuntime) readPump(ctx context.Context) {
	// One running append and one queued request bound per-connection detail work.
	// The reader continues processing lease/control requests while intake waits.
	appendCtx, cancelAppends := context.WithCancel(ctx)
	appends := make(chan Request, 1)
	appendDone := make(chan struct{})
	go func() {
		defer close(appendDone)
		for {
			select {
			case <-appendCtx.Done():
				return
			case request := <-appends:
				if appendCtx.Err() != nil {
					return
				}
				c.handleExamAttemptBrowserAppend(appendCtx, &request)
			}
		}
	}()
	defer func() {
		cancelAppends()
		<-appendDone
	}()
	c.socket.SetReadLimit(MaxMessageBytes)
	_ = c.socket.SetReadDeadline(c.clock.Now().Add(pongWait))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(c.clock.Now().Add(pongWait))
	})
	for {
		if ctx.Err() != nil {
			return
		}
		var request Request
		if err := c.socket.ReadJSON(&request); err != nil {
			if c.recorder != nil {
				c.recorder.ObserveWebSocketMessage("inbound", "request", streamResult(err), 0)
			}
			return
		}
		if ctx.Err() != nil {
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
		if request.Action == examAttemptBrowserAppendAction {
			select {
			case appends <- request:
			default:
				// No application call occurred, so no private recovery snapshot is
				// available. The normal shared append bucket still applies on retry.
				c.enqueueDeliveryError(request.Sequence, "exam.delivery.append_rate_limited", nil)
			}
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
	case examAttemptSecurityUpdateAction:
		c.handleExamAttemptSecurityUpdate(ctx, request)
	case examAttemptRenewAction:
		c.handleExamAttemptRenew(ctx, request)
	case examAttemptFocusLossAction:
		c.handleExamAttemptFocusLoss(ctx, request)
	case examAttemptBrowserStartAction:
		c.handleExamAttemptBrowserStart(ctx, request)
	case examAttemptBrowserAppendAction:
		c.handleExamAttemptBrowserAppend(ctx, request)
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
	canonicalRequest, marshalErr := json.Marshal(decoded)
	if marshalErr != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorAttemptConnectRequestInvalid)
		return
	}
	requestHash := sha256.Sum256(canonicalRequest)
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
	result, err := attempts.ConnectExamAttempt(ctx, app.NewInvocation(c.principal, metadata), app.ConnectExamAttemptCommand{Security: decoded.Security,
		SittingID: sittingID, ContinuityCredential: decoded.ContinuityCredential,
		ConfigurationManifestFingerprint: decoded.ConfigurationManifestFingerprint,
		InitialConfiguration:             decoded.InitialConfiguration, IdempotencyKey: decoded.IdempotencyKey,
	})
	if err != nil {
		code, presentation := examAttemptConnectError(err)
		var capacity *model.DeliveryMetadataCapacity
		if code == "exam.delivery.metadata_capacity" && errors.As(err, &capacity) && capacity.Validate() == nil {
			value := *capacity
			c.enqueueOutbound(outboundMessage{response: &Response{Status: "error", Sequence: request.Sequence, Error: &Error{DeliveryMetadataCapacity: &value, Code: code, Message: localizedText(c.localizer, c.locale, websocketErrorMessage(presentation))}}})
		} else {
			c.enqueueError(request.Sequence, code, presentation)
		}
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
		Security:  result.Security,
		AttemptID: result.Attempt.ID.String(), WorkspaceID: result.Workspace.ID.String(),
		ParticipationID: result.Participation.ID.String(), AttemptConnectionID: result.Connection.ID.String(),
		Generation:             result.Participation.Generation,
		RenewalIntervalSeconds: int64(model.AttemptParticipationRenewalInterval / time.Second),
		StartedAt:              result.Participation.StartedAt.Format(time.RFC3339Nano),
		LeaseExpiresAt:         result.Participation.LeaseExpiresAt.Format(time.RFC3339Nano),
		FirstAdmission:         result.FirstAdmission, Replayed: result.Replayed,
		CandidateRuntimeCapabilities: candidateRuntimeCapabilitiesWire(result.RuntimeCapabilities),
		BrowserPolicy:                candidateBrowserPolicyWire(result.BrowserPolicy),
		LiveCorrections:              candidateLiveCorrectionsWire(result.LiveCorrections),
	})
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptConnectionFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func boundedWebSocketAction(action string) string {
	switch action {
	case "ping", "subscribe", "unsubscribe", examAttemptConnectAction, examAttemptRenewAction, examAttemptSecurityUpdateAction,
		examAttemptFocusLossAction, examAttemptBrowserStartAction, examAttemptBrowserAppendAction:
		return action
	default:
		return "unknown"
	}
}

func (c *connectionRuntime) handleExamAttemptBrowserStart(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptBrowserStartRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorBrowserActivityInvalid)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation || decoded.ParticipationID != c.attempt.participationID.String() {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorBrowserActivityFailed)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.StartExamAttemptBrowserActivity(ctx, app.NewInvocation(c.principal, metadata), app.StartBrowserActivityCommand{
		CandidateAccess: app.CandidateExamAttemptAccess{AttemptID: binding.attemptID, ConnectionID: binding.connectionID,
			ContinuityCredential: decoded.ContinuityCredential}, ParticipationID: binding.participationID, Generation: binding.generation,
		SourceSessionID: model.BrowserSourceSessionID(decoded.SourceSessionID), PolicyRevisionID: model.ExamRevisionID(decoded.PolicyRevisionID), PolicyDigest: decoded.PolicyDigest, Transition: decoded.Transition,
	})
	if err != nil {
		code := "exam.attempt.unavailable"
		if failure, ok := app.As(err); ok {
			code = failure.Code()
		}
		var refusal *app.BrowserSourceRefusal
		if errors.As(err, &refusal) && refusal.Status.Validate() == nil && refusal.Capabilities != nil && refusal.Capabilities.Validate() == nil {
			caps := candidateRuntimeCapabilitiesWire(*refusal.Capabilities)
			c.enqueueOutbound(outboundMessage{response: &Response{Status: "error", Sequence: request.Sequence, Error: &Error{Code: code, Message: localizedText(c.localizer, c.locale, websocketErrorMessage(websocketErrorBrowserActivityFailed)), BrowserSourceStatus: &refusal.Status, CandidateRuntimeCapabilities: &caps, DeliveryMetadataCapacity: refusal.Capacity}}})
			return
		}
		c.enqueueError(request.Sequence, code, websocketErrorBrowserActivityFailed)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorBrowserActivityFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

func (c *connectionRuntime) handleExamAttemptBrowserAppend(ctx context.Context, request *Request) {
	decoded, events, err := decodeExamAttemptBrowserAppendRequest(request.Data)
	if err != nil {
		c.enqueueError(request.Sequence, "websocket.request.invalid", websocketErrorBrowserActivityInvalid)
		return
	}
	c.mu.Lock()
	if c.attempt == nil || decoded.Generation != c.attempt.generation || decoded.ParticipationID != c.attempt.participationID.String() {
		c.mu.Unlock()
		c.enqueueError(request.Sequence, "exam.attempt.connection_closed", websocketErrorAttemptConnectionInactive)
		return
	}
	binding := *c.attempt
	c.mu.Unlock()
	attempts, ok := c.application.(examAttemptApplication)
	if !ok {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorBrowserActivityFailed)
		return
	}
	metadata := c.metadata
	metadata.RequestID = fmt.Sprintf("%s:%d", c.id, request.Sequence)
	result, err := attempts.AppendExamAttemptBrowserActivity(ctx, app.NewInvocation(c.principal, metadata), app.AppendBrowserActivityCommand{
		CandidateAccess: app.CandidateExamAttemptAccess{AttemptID: binding.attemptID, ConnectionID: binding.connectionID,
			ContinuityCredential: decoded.ContinuityCredential}, ParticipationID: binding.participationID, Generation: binding.generation,
		SourceSessionID: model.BrowserSourceSessionID(decoded.SourceSessionID), PolicyRevisionID: model.ExamRevisionID(decoded.PolicyRevisionID), PolicyDigest: decoded.PolicyDigest, Events: events,
	})
	if err != nil {
		code := "exam.attempt.unavailable"
		if failure, ok := app.As(err); ok {
			code = failure.Code()
		}
		c.enqueueDeliveryError(request.Sequence, code, err)
		return
	}
	encoded, err := json.Marshal(browserActivityAcknowledgementWire(result))
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorBrowserActivityFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
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
	result, err := attempts.RenewExamAttemptParticipation(ctx, app.NewInvocation(c.principal, metadata), app.RenewExamAttemptParticipationCommand{SecurityCoverage: decoded.SecurityCoverage,
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
	encoded, err := json.Marshal(examAttemptRenewResponse{SecurityCoverage: result.SecurityCoverage, Generation: result.Generation, AcceptedSequence: result.AcceptedSequence,
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
		removedSubscription := false
		if c.attempt != nil && *c.attempt == binding {
			key := c.examAttemptSubscriptionLocked().Key()
			_, removedSubscription = c.subscriptions[key]
			delete(c.subscriptions, key)
			c.attempt = nil
		}
		c.mu.Unlock()
		if removedSubscription && c.recorder != nil {
			c.recorder.AddWebSocketSubscriptions(-1)
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
	c.mu.Unlock()
	if removedSubscription && c.recorder != nil {
		c.recorder.AddWebSocketSubscriptions(-1)
	}
	return true
}

func (c *connectionRuntime) finalizeExamAttempt(ctx context.Context) {
	c.attemptClose.Do(func() {
		c.mu.Lock()
		binding := c.attempt
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
		if binding == nil {
			return
		}
		attempts, ok := c.application.(examAttemptApplication)
		if !ok {
			return
		}
		closeCtx, cancel := c.finalizationContext(ctx)
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

func (c *connectionRuntime) handleExamAttemptSecurityUpdate(ctx context.Context, request *Request) {
	decoded, err := decodeExamAttemptSecurityUpdateRequest(request.Data)
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
	result, err := attempts.UpdateExamSecurityCoverage(ctx, app.NewInvocation(c.principal, metadata), app.UpdateSecurityCoverageCommand{Access: app.CandidateExamAttemptAccess{AttemptID: binding.attemptID, ConnectionID: binding.connectionID, ContinuityCredential: decoded.ContinuityCredential}, ParticipationID: binding.participationID, Generation: binding.generation, Coverage: decoded.SecurityCoverage})
	if err != nil {
		code, presentation := examAttemptRenewError(err)
		c.enqueueError(request.Sequence, code, presentation)
		return
	}
	if result.Validate() != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptRenewalFailed)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		c.enqueueError(request.Sequence, "exam.attempt.unavailable", websocketErrorAttemptRenewalFailed)
		return
	}
	c.enqueueResponse(request.Sequence, encoded)
}

// Live and historical delivery share the same bounded recovery projection.
func (c *connectionRuntime) enqueueDeliveryError(sequence int64, code string, err error) {
	value := &Error{Code: code, Message: localizedText(c.localizer, c.locale, websocketErrorMessage(websocketErrorBrowserActivityFailed))}
	var recovery interface {
		DeliveryRecovery() *model.DeliveryRecovery
	}
	if app.SupportsDeliveryRecovery(code) && errors.As(err, &recovery) {
		if state := recovery.DeliveryRecovery(); state != nil && state.Validate() == nil {
			value.Delivery = state
		}
	}
	if code == "exam.delivery.pending_capacity" || code == "exam.delivery.append_rate_limited" || code == "exam.delivery.summary_rate_limited" {
		value.RetryAfterSeconds = 1
	}
	c.enqueueOutbound(outboundMessage{response: &Response{Status: "error", Sequence: sequence, Error: value}})
}
