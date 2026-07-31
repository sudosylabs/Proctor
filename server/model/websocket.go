// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket message contracts. See server/NOTICE for exact provenance.

package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	MaxWebSocketMessageBytes = 256 << 10
	MaxWebSocketEventBytes   = 128
	MaxWebSocketActionBytes  = 128
)

type WebSocketEventType string

const (
	WebSocketEventHello  WebSocketEventType = "hello"
	WebSocketEventResync WebSocketEventType = "resync_required"
)

// WebSocketEvent is an application event delivered to one or more subscribed
// connections. Sequence is assigned independently by each owning node and is
// therefore never serialized through the cluster transport.
type WebSocketEvent struct {
	Id       string          `json:"id"`
	Event    string          `json:"event"`
	Sequence int64           `json:"sequence"`
	UserId   string          `json:"user_id,omitempty"`
	Action   Action          `json:"action,omitempty"`
	Resource Resource        `json:"resource,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func (e *WebSocketEvent) Clone() *WebSocketEvent {
	if e == nil {
		return nil
	}
	cloned := *e
	cloned.Data = append(json.RawMessage(nil), e.Data...)
	return &cloned
}

func (e *WebSocketEvent) ValidateForPublish() error {
	if e == nil {
		return errors.New("websocket event is nil")
	}
	if e.Id != "" && !IsValidId(e.Id) {
		return errors.New("websocket event ID is invalid")
	}
	if len(e.Event) == 0 || len(e.Event) > MaxWebSocketEventBytes ||
		!validClusterName(e.Event) {
		return fmt.Errorf("invalid websocket event %q", e.Event)
	}
	if e.Sequence != 0 {
		return errors.New("websocket event sequence must be assigned by the connection")
	}
	if e.UserId != "" && !IsValidId(e.UserId) {
		return errors.New("websocket event user ID is invalid")
	}
	if e.Action == "" && e.Resource == (Resource{}) {
		if e.UserId == "" {
			return errors.New("websocket event requires a user target or authorized resource")
		}
	} else {
		definition, ok := DefinitionForAction(e.Action)
		if !ok || !e.Resource.IsValid() || definition.ResourceType != e.Resource.Type {
			return errors.New("websocket event authorization target is invalid")
		}
	}
	if len(e.Data) > MaxWebSocketMessageBytes {
		return fmt.Errorf("websocket event data exceeds %d bytes", MaxWebSocketMessageBytes)
	}
	if len(e.Data) != 0 && !json.Valid(e.Data) {
		return errors.New("websocket event data is not valid JSON")
	}
	return nil
}

type WebSocketSubscription struct {
	Action   Action   `json:"action"`
	Resource Resource `json:"resource"`
}

func (s WebSocketSubscription) IsValid() bool {
	definition, ok := DefinitionForAction(s.Action)
	return ok && s.Resource.IsValid() && definition.ResourceType == s.Resource.Type
}

func (s WebSocketSubscription) Key() string {
	return string(s.Action) + "\x00" + string(s.Resource.Type) + "\x00" + s.Resource.Id
}

type WebSocketRequest struct {
	Sequence int64           `json:"sequence"`
	Action   string          `json:"action"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func (r *WebSocketRequest) Validate() error {
	if r == nil {
		return errors.New("websocket request is nil")
	}
	if r.Sequence <= 0 {
		return errors.New("websocket request sequence must be greater than zero")
	}
	if len(r.Action) == 0 || len(r.Action) > MaxWebSocketActionBytes ||
		!utf8.ValidString(r.Action) || !validClusterName(r.Action) {
		return errors.New("websocket request action is invalid")
	}
	if len(r.Data) > MaxWebSocketMessageBytes {
		return fmt.Errorf("websocket request data exceeds %d bytes", MaxWebSocketMessageBytes)
	}
	if len(r.Data) != 0 && !json.Valid(r.Data) {
		return errors.New("websocket request data is not valid JSON")
	}
	return nil
}

type WebSocketResponse struct {
	Status   string          `json:"status"`
	Sequence int64           `json:"sequence"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *WebSocketError `json:"error,omitempty"`
}

type WebSocketError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WebSocketHello struct {
	ConnectionId string `json:"connection_id"`
	NodeId       string `json:"node_id"`
	Resumed      bool   `json:"resumed"`
}
