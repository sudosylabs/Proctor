// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket connection and replay-queue flow. See server/NOTICE for
// exact provenance.

package websocket

import (
	"context"
	"encoding/json"

	gorilla "github.com/gorilla/websocket"

	"github.com/sudosylabs/proctor/server/model"
)

func (c *connectionRuntime) writePump(ctx context.Context) {
	ticker := c.clock.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-c.send:
			_ = c.socket.SetWriteDeadline(c.clock.Now().Add(writeWait))
			var err error
			if message.event != nil {
				err = c.socket.WriteJSON(message.event)
			} else {
				err = c.socket.WriteJSON(message.response)
			}
			if err != nil {
				c.closeTransport()
				return
			}
		case <-ticker.Chan():
			_ = c.socket.SetWriteDeadline(c.clock.Now().Add(writeWait))
			if err := c.socket.WriteMessage(websocketPingMessage, nil); err != nil {
				c.closeTransport()
				return
			}
		}
	}
}

func (c *connectionRuntime) enqueueHello(resumed, resyncRequired bool) {
	data, _ := json.Marshal(Hello{
		ConnectionId: c.id, NodeId: c.nodeID, Resumed: resumed,
	})
	c.enqueueEvent(&Event{
		Id: model.NewId(), Event: string(EventHello),
		UserID: c.principal.UserID.String(), Data: data,
	})
	if resyncRequired {
		c.enqueueEvent(&Event{
			Id: model.NewId(), Event: string(EventResync),
			UserID: c.principal.UserID.String(),
		})
	}
}

func (c *connectionRuntime) enqueueEvent(event *Event) {
	c.mu.Lock()
	for _, sent := range c.history {
		if sent.Id == event.Id {
			c.mu.Unlock()
			return
		}
	}
	c.nextSequence++
	candidate := event.Clone()
	candidate.Sequence = c.nextSequence
	c.history = append(c.history, candidate.Clone())
	if len(c.history) > replayQueueSize {
		c.history = append([]*Event(nil), c.history[len(c.history)-replayQueueSize:]...)
	}
	queued := c.tryEnqueueOutbound(outboundMessage{event: candidate})
	c.mu.Unlock()
	if !queued {
		c.closeForBackpressure()
	}
}

func (c *connectionRuntime) enqueueResponse(sequence int64, data json.RawMessage) {
	response := &Response{
		Status: "ok", Sequence: sequence, Data: append(json.RawMessage(nil), data...),
	}
	c.enqueueOutbound(outboundMessage{response: response})
}

func (c *connectionRuntime) enqueueError(sequence int64, code, message string) {
	response := &Response{
		Status: "error", Sequence: sequence,
		Error: &Error{Code: code, Message: message},
	}
	c.enqueueOutbound(outboundMessage{response: response})
}

func (c *connectionRuntime) close(code int, reason string, replayable bool) {
	if !replayable {
		c.mu.Lock()
		c.replayable = false
		c.mu.Unlock()
	}
	c.closeOnce.Do(func() {
		message := gorilla.FormatCloseMessage(code, reason)
		_ = c.socket.WriteControl(
			websocketCloseMessage,
			message,
			c.clock.Now().Add(writeWait),
		)
		_ = c.socket.Close()
	})
}

func (c *connectionRuntime) closeTransport() {
	c.closeOnce.Do(func() {
		_ = c.socket.Close()
	})
}

func (c *connectionRuntime) enqueueOutbound(message outboundMessage) {
	if !c.tryEnqueueOutbound(message) {
		c.closeForBackpressure()
	}
}

func (c *connectionRuntime) closeForBackpressure() {
	c.close(CloseBackpressure, "client is too slow", false)
}

func (c *connectionRuntime) tryEnqueueOutbound(message outboundMessage) bool {
	select {
	case c.send <- message:
		return true
	default:
		return false
	}
}
