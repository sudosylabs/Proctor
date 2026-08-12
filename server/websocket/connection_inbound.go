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
	"encoding/json"
	"fmt"

	"github.com/sudosylabs/proctor/server/app"
)

func (c *connection) readPump(ctx context.Context) {
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

func (c *connection) sessionPump(ctx context.Context) {
	ticker := c.clock.NewTicker(sessionCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			if appErr := c.hub.application.ValidateWebSocketPrincipal(
				ctx,
				c.principal,
			); appErr != nil {
				c.close(CloseSessionRevoked, "session no longer valid", false)
				return
			}
		}
	}
}

func (c *connection) handleRequest(
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
		if err := c.hub.application.AuthorizeWebSocketSubscription(
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
		delete(c.subscriptions, subscription.Key())
		c.mu.Unlock()
		c.enqueueResponse(request.Sequence, nil)
	default:
		c.enqueueError(request.Sequence, "websocket.action.unknown", "Unknown WebSocket action.")
	}
}

func (c *connection) hasSubscription(
	subscription Subscription,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.subscriptions[subscription.Key()]
	return exists
}
