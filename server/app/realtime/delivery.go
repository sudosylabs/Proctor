// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost revision
// 10b780cb097b2ec94ab0f9df7ebcbd5b7850f13f, particularly
// server/channels/app/platform/cluster_handlers.go and
// server/channels/app/platform/web_hub.go. See doc.go and the server NOTICE for
// provenance and licensing details.

package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	clusterEventPublication        = "websocket.publish"
	clusterEventExamAttemptUnbound = "exam_attempt.connection_unbound"
)

// Sink delivers already-authorized events and connection closes to the local
// socket boundary. It deliberately contains no WebSocket wire types.
type Sink interface {
	PublishLocal(context.Context, RealtimeEvent)
	UnbindExamAttemptConnection(model.AttemptConnectionID)
	CloseSession(string, ConnectionCloseReason)
	CloseUser(string, ConnectionCloseReason)
	CloseAll(ConnectionCloseReason)
}

// ClusterFanout carries opaque application event names and payloads between
// nodes. Its adapter owns cluster envelopes, transport, and lifecycle.
type ClusterFanout interface {
	RegisterHandler(event string, handler func(context.Context, []byte) error) error
	Broadcast(ctx context.Context, event string, data []byte) error
}

// AuthenticationInvalidator applies the local, disposable authentication
// cache effects that accompany session and credential invalidation.
type AuthenticationInvalidator interface {
	InvalidateAccessCredentials(context.Context, []string)
	InvalidateSessionActivity(context.Context, []string)
}

// Diagnostics records bounded, non-fatal security propagation failures.
// Implementations must not enrich these reports with complete payloads or
// security-sensitive identifiers.
type Diagnostics interface {
	ErrorContext(context.Context, string, error)
}

// InvalidPublicationError reports a transport-neutral event validation
// failure. Callers decide how to expose the failure at their boundary.
type InvalidPublicationError struct {
	err error
}

func (e *InvalidPublicationError) Error() string { return e.err.Error() }
func (e *InvalidPublicationError) Unwrap() error { return e.err }

// DeliveryError reports a transport-neutral peer encoding or fan-out failure.
// Local delivery has already been attempted when Publish returns this error.
type DeliveryError struct {
	err error
}

func (e *DeliveryError) Error() string { return e.err.Error() }
func (e *DeliveryError) Unwrap() error { return e.err }

// Service owns ordinary realtime event delivery. It is inert after
// construction and borrows its attached collaborators.
type Service struct {
	mu                        sync.RWMutex
	sink                      Sink
	fanout                    ClusterFanout
	authenticationInvalidator AuthenticationInvalidator
	diagnostics               Diagnostics
}

// New constructs an inert Realtime module with its required security
// collaborators. Sink and peer fanout remain staged because composition builds
// those adapters after the application graph.
func New(
	invalidator AuthenticationInvalidator,
	diagnostics Diagnostics,
) (*Service, error) {
	if invalidator == nil {
		return nil, errors.New("realtime authentication invalidator is nil")
	}
	if diagnostics == nil {
		return nil, errors.New("realtime diagnostics is nil")
	}
	return &Service{
		authenticationInvalidator: invalidator,
		diagnostics:               diagnostics,
	}, nil
}

// SetSink attaches the local sink exactly once.
func (s *Service) SetSink(sink Sink) error {
	if sink == nil {
		return errors.New("realtime sink is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sink != nil {
		return errors.New("realtime sink is already attached")
	}
	s.sink = sink
	return nil
}

// SetClusterFanout attaches the peer fanout exactly once and registers the
// ordinary-publication peer handler. Registration failure is terminal for the
// instance: composition must unwind node construction rather than retry it.
func (s *Service) SetClusterFanout(fanout ClusterFanout) error {
	if fanout == nil {
		return errors.New("realtime cluster fan-out is nil")
	}
	s.mu.Lock()
	if s.fanout != nil {
		s.mu.Unlock()
		return errors.New("realtime cluster fan-out is already attached")
	}
	s.fanout = fanout
	s.mu.Unlock()

	handlers := []struct {
		event   string
		handler func(context.Context, []byte) error
	}{
		{event: clusterEventPublication, handler: s.handlePeerPublication},
		{event: clusterEventExamAttemptUnbound, handler: s.handlePeerExamAttemptUnbound},
		{event: clusterEventSessionRevoked, handler: s.handlePeerSessionRevocation},
		{event: clusterEventAuthorizationInvalidated, handler: s.handlePeerAuthorizationInvalidation},
	}
	for _, registration := range handlers {
		if err := fanout.RegisterHandler(registration.event, registration.handler); err != nil {
			return fmt.Errorf("register %s cluster handler: %w", registration.event, err)
		}
	}
	return nil
}

// UnbindExamAttemptConnection clears only the runtime binding for one durable
// Attempt Connection, locally first and then on peers. It deliberately leaves
// the generic WebSocket open for unrelated application use.
func (s *Service) UnbindExamAttemptConnection(ctx context.Context, connectionID model.AttemptConnectionID) error {
	if !connectionID.IsValid() {
		return &InvalidPublicationError{err: errors.New("Exam Attempt Connection identity is invalid")}
	}
	s.unbindExamAttemptConnectionLocal(connectionID)
	payload, err := json.Marshal(examAttemptUnboundMessage{AttemptConnectionID: connectionID.String()})
	if err != nil {
		return &DeliveryError{err: err}
	}
	if err = s.broadcast(ctx, clusterEventExamAttemptUnbound, payload); err != nil {
		return &DeliveryError{err: err}
	}
	return nil
}

func (s *Service) unbindExamAttemptConnectionLocal(connectionID model.AttemptConnectionID) {
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink != nil {
		sink.UnbindExamAttemptConnection(connectionID)
	}
}

type examAttemptUnboundMessage struct {
	AttemptConnectionID string `json:"attempt_connection_id"`
}

func (s *Service) handlePeerExamAttemptUnbound(_ context.Context, data []byte) error {
	var message examAttemptUnboundMessage
	if err := decodeStrictPayload(data, &message); err != nil {
		return err
	}
	connectionID, err := model.ParseAttemptConnectionID(message.AttemptConnectionID)
	if err != nil {
		return errors.New("cluster Exam Attempt Connection identity is invalid")
	}
	s.unbindExamAttemptConnectionLocal(connectionID)
	return nil
}

func decodeStrictPayload(data []byte, target any) error {
	if len(data) == 0 {
		return errors.New("realtime cluster payload is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("realtime cluster payload has trailing JSON")
		}
		return err
	}
	return nil
}

// Publish delivers a clone locally first and then fans out the stable peer
// payload. Callers must invoke it only after durable commit.
func (s *Service) Publish(ctx context.Context, event RealtimeEvent) error {
	candidate := event.Clone()
	if candidate.ID == "" {
		candidate.ID = model.NewId()
	}
	if err := candidate.ValidateForPublish(); err != nil {
		return &InvalidPublicationError{err: err}
	}

	// Same loop-prevention shape as Mattermost: publish locally once, then
	// send to peers. Peer handlers call only publishLocal.
	s.publishLocal(ctx, candidate)
	message := realtimeEventMessageFromModel(candidate)
	payload, err := json.Marshal(realtimePublication{Event: &message})
	if err != nil {
		return &DeliveryError{err: err}
	}
	if err := s.broadcast(ctx, clusterEventPublication, payload); err != nil {
		return &DeliveryError{err: err}
	}
	return nil
}

func (s *Service) publishLocal(ctx context.Context, event RealtimeEvent) {
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink != nil {
		sink.PublishLocal(ctx, event.Clone())
	}
}

func (s *Service) broadcast(ctx context.Context, event string, data []byte) error {
	s.mu.RLock()
	fanout := s.fanout
	s.mu.RUnlock()
	if fanout == nil {
		return errors.New("realtime cluster fan-out is not attached")
	}
	return fanout.Broadcast(ctx, event, data)
}

func (s *Service) handlePeerPublication(ctx context.Context, data []byte) error {
	var publication realtimePublication
	if err := decodePayload(data, &publication); err != nil {
		return err
	}
	if publication.Event == nil {
		return errors.New("cluster realtime publication has no event")
	}
	event := publication.Event.toModel()
	if err := event.ValidateForPublish(); err != nil {
		return err
	}
	s.publishLocal(ctx, event)
	return nil
}

type realtimePublication struct {
	Event *realtimeEventMessage `json:"event"`
}

// realtimeEventMessage is the explicit cluster-wire projection of a
// transport-neutral RealtimeEvent. It preserves the established snake_case
// payload independently of domain-model field names.
type realtimeEventMessage struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"event"`
	UserID   string                  `json:"user_id,omitempty"`
	Action   model.Action            `json:"action,omitempty"`
	Resource realtimeResourceMessage `json:"resource,omitempty"`
	Data     json.RawMessage         `json:"data,omitempty"`
}

type realtimeResourceMessage struct {
	Type model.ResourceType `json:"type"`
	ID   string             `json:"id"`
}

func realtimeEventMessageFromModel(event RealtimeEvent) realtimeEventMessage {
	return realtimeEventMessage{
		ID:       event.ID,
		Name:     event.Name,
		UserID:   event.UserID,
		Action:   event.Action,
		Resource: realtimeResourceMessage{Type: event.Resource.Type, ID: event.Resource.ID},
		Data:     append(json.RawMessage(nil), event.Data...),
	}
}

func (message realtimeEventMessage) toModel() RealtimeEvent {
	return RealtimeEvent{
		ID:       message.ID,
		Name:     message.Name,
		UserID:   message.UserID,
		Action:   message.Action,
		Resource: model.Resource{Type: message.Resource.Type, ID: message.Resource.ID},
		Data:     append(json.RawMessage(nil), message.Data...),
	}
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 {
		return errors.New("realtime cluster payload is empty")
	}
	return json.Unmarshal(data, target)
}
