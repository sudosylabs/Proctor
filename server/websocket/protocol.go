// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public WebSocket message contracts. See server/NOTICE for exact provenance.

package websocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	MaxMessageBytes = 256 << 10
	MaxEventBytes   = 128
	MaxActionBytes  = 128
)

// EventType is a versioned WebSocket wire event name.
type EventType string

const (
	EventHello  EventType = "hello"
	EventResync EventType = "resync_required"
)

// Event is a wire event delivered to one or more subscribed connections.
// Sequence is assigned independently by each owning connection and is never
// serialized through the cluster transport.
type Event struct {
	Id       string          `json:"id"`
	Event    string          `json:"event"`
	Sequence int64           `json:"sequence"`
	UserID   string          `json:"user_id,omitempty"`
	Action   model.Action    `json:"action,omitempty"`
	Resource Resource        `json:"resource,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Resource is the WebSocket wire projection of a domain authorization
// resource. Keeping this DTO here prevents domain field names from silently
// changing the versioned protocol.
type Resource struct {
	Type model.ResourceType `json:"type"`
	ID   string             `json:"id"`
}

func resourceFromModel(resource model.Resource) Resource {
	return Resource{Type: resource.Type, ID: resource.ID}
}

func (r Resource) model() model.Resource {
	return model.Resource{Type: r.Type, ID: r.ID}
}

// Clone returns a deep copy safe for concurrent delivery.
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	cloned := *e
	cloned.Data = append(json.RawMessage(nil), e.Data...)
	return &cloned
}

// ValidateForPublish checks wire publication invariants. Sequence must be zero
// until the owning connection stamps it.
func (e *Event) ValidateForPublish() error {
	if e == nil {
		return errors.New("websocket event is nil")
	}
	if e.Id != "" && !model.IsValidId(e.Id) {
		return errors.New("websocket event ID is invalid")
	}
	if len(e.Event) == 0 || len(e.Event) > MaxEventBytes || !validName(e.Event) {
		return fmt.Errorf("invalid websocket event %q", e.Event)
	}
	if e.Sequence != 0 {
		return errors.New("websocket event sequence must be assigned by the connection")
	}
	if e.UserID != "" && !model.IsValidId(e.UserID) {
		return errors.New("websocket event user ID is invalid")
	}
	if e.Action == "" && e.Resource == (Resource{}) {
		if e.UserID == "" {
			return errors.New("websocket event requires a user target or authorized resource")
		}
	} else {
		definition, ok := model.DefinitionForAction(e.Action)
		if !ok || e.Resource.model().Validate() != nil || definition.ResourceType != e.Resource.Type {
			return errors.New("websocket event authorization target is invalid")
		}
	}
	if len(e.Data) > MaxMessageBytes {
		return fmt.Errorf("websocket event data exceeds %d bytes", MaxMessageBytes)
	}
	if len(e.Data) != 0 && !json.Valid(e.Data) {
		return errors.New("websocket event data is not valid JSON")
	}
	return nil
}

// Subscription is the wire form of an action/resource subscription.
type Subscription struct {
	Action   model.Action `json:"action"`
	Resource Resource     `json:"resource"`
}

// IsValid reports whether the subscription targets a recognized action/resource.
func (s Subscription) IsValid() bool {
	definition, ok := model.DefinitionForAction(s.Action)
	return ok && s.Resource.model().Validate() == nil && definition.ResourceType == s.Resource.Type
}

// Key uniquely identifies a subscription for connection-local storage.
func (s Subscription) Key() string {
	return string(s.Action) + "\x00" + string(s.Resource.Type) + "\x00" + s.Resource.ID
}

// Request is a client-to-server WebSocket command envelope.
type Request struct {
	Sequence int64           `json:"sequence"`
	Action   string          `json:"action"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Validate checks the client request envelope.
func (r *Request) Validate() error {
	if r == nil {
		return errors.New("websocket request is nil")
	}
	if r.Sequence <= 0 {
		return errors.New("websocket request sequence must be greater than zero")
	}
	if len(r.Action) == 0 || len(r.Action) > MaxActionBytes ||
		!utf8.ValidString(r.Action) || !validName(r.Action) {
		return errors.New("websocket request action is invalid")
	}
	if len(r.Data) > MaxMessageBytes {
		return fmt.Errorf("websocket request data exceeds %d bytes", MaxMessageBytes)
	}
	if len(r.Data) != 0 && !json.Valid(r.Data) {
		return errors.New("websocket request data is not valid JSON")
	}
	return nil
}

// Response is a server-to-client command result envelope.
type Response struct {
	Status   string          `json:"status"`
	Sequence int64           `json:"sequence"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    *Error          `json:"error,omitempty"`
}

// Error is the versioned WebSocket protocol error body.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Hello is the first-event payload identifying a connection.
type Hello struct {
	ConnectionId string `json:"connection_id"`
	NodeId       string `json:"node_id"`
	Resumed      bool   `json:"resumed"`
}

func validName(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' && character != ':' {
			return false
		}
	}
	return true
}
