// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost's public application WebSocket publication and
// cluster-handler flow. Proctor keeps the local-first, peer-local-only
// delivery invariant while using transport-neutral event intents and
// composition-owned wire adapters.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// RealtimeSink delivers already-authorized events and connection closes to the
// local socket boundary. It deliberately contains no HTTP or WebSocket wire
// types so application publication remains independently testable.
type RealtimeSink interface {
	PublishLocal(context.Context, RealtimeEvent)
	CloseSession(string, ConnectionCloseReason)
	CloseUser(string, ConnectionCloseReason)
	CloseAll(ConnectionCloseReason)
}

// RealtimeClusterFanout is the composition-owned inter-node publication port.
// Application code supplies opaque event names and payloads; the adapter owns
// cluster wire envelopes and handler registration. Delivery is always
// best-effort (ADR-0026).
type RealtimeClusterFanout interface {
	RegisterHandler(event string, handler func(context.Context, []byte) error) error
	Broadcast(ctx context.Context, event string, data []byte) error
}

// RealtimeDiagnostics reports non-fatal publication failures without depending
// on a concrete logger package.
type RealtimeDiagnostics interface {
	ErrorContext(ctx context.Context, message string, err error)
	ErrorContextWithEvent(ctx context.Context, message, event string, err error)
}

// RealtimeService owns local-first, loop-free realtime publication policy and
// security invalidation fan-out. It does not import platform, WebSocket wire,
// or cluster wire contracts.
type RealtimeService struct {
	authentication *AuthenticationService
	diagnostics    RealtimeDiagnostics
	mu             sync.RWMutex
	sink           RealtimeSink
	cluster        RealtimeClusterFanout
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

type sessionRevocationMessage struct {
	UserID            string   `json:"user_id"`
	SessionIds        []string `json:"session_ids"`
	AccessTokenHashes []string `json:"access_token_hashes"`
	CloseConnections  bool     `json:"close_connections"`
}

type authorizationInvalidationMessage struct {
	UserID string `json:"user_id,omitempty"`
}

func newRealtimeService(
	authentication *AuthenticationService,
	diagnostics RealtimeDiagnostics,
) *RealtimeService {
	return &RealtimeService{
		authentication: authentication,
		diagnostics:    diagnostics,
	}
}

// SetClusterFanout attaches the composition adapter and registers peer
// handlers. Peer handlers apply only local effects so Broadcast never loops.
func (s *RealtimeService) SetClusterFanout(fanout RealtimeClusterFanout) error {
	if fanout == nil {
		return errors.New("realtime cluster fan-out is nil")
	}
	s.mu.Lock()
	if s.cluster != nil {
		s.mu.Unlock()
		return errors.New("realtime cluster fan-out is already attached")
	}
	s.cluster = fanout
	s.mu.Unlock()

	handlers := map[string]func(context.Context, []byte) error{
		realtimeClusterEventPublication:              s.handlePeerPublication,
		realtimeClusterEventSessionRevoked:           s.handlePeerSessionRevocation,
		realtimeClusterEventAuthorizationInvalidated: s.handlePeerAuthorizationInvalidation,
	}
	for event, handler := range handlers {
		if err := fanout.RegisterHandler(event, handler); err != nil {
			return fmt.Errorf("register %s cluster handler: %w", event, err)
		}
	}
	return nil
}

func (s *RealtimeService) SetSink(sink RealtimeSink) error {
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

// Publish delivers a transport-neutral event locally first, then fans it out
// to peers best-effort. Callers must invoke this only after durable commit.
func (s *RealtimeService) Publish(ctx context.Context, event RealtimeEvent) error {
	candidate := event.Clone()
	if candidate.ID == "" {
		candidate.ID = model.NewId()
	}
	if err := candidate.ValidateForPublish(); err != nil {
		return invalidRealtimeRequest(err.Error())
	}

	// Same loop-prevention shape as Mattermost: publish locally once, then
	// send to peers. Peer handlers call only publishLocal.
	s.publishLocal(ctx, candidate)
	message := realtimeEventMessageFromModel(candidate)
	payload, err := json.Marshal(realtimePublication{Event: &message})
	if err != nil {
		return internalRealtimeError(err)
	}
	if err := s.broadcast(ctx, realtimeClusterEventPublication, payload); err != nil {
		return internalRealtimeError(err)
	}
	return nil
}

func (s *RealtimeService) publishLocal(ctx context.Context, event RealtimeEvent) {
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink != nil {
		sink.PublishLocal(ctx, event.Clone())
	}
}

func (s *RealtimeService) reportTransientFailure(
	ctx context.Context,
	event string,
	err error,
) {
	if s.diagnostics == nil {
		return
	}
	s.diagnostics.ErrorContextWithEvent(ctx, "transient realtime publication failed", event, err)
}

func (s *RealtimeService) PropagateSessionRevocation(
	ctx context.Context,
	userID string,
	sessionIDs []string,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserID:            userID,
		SessionIds:        append([]string(nil), sessionIDs...),
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
		CloseConnections:  true,
	}
	if err := validateSessionRevocation(message); err != nil {
		s.reportInvalidPropagation(ctx, "refusing invalid session revocation propagation", err)
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastSecurityInvalidation(ctx, realtimeClusterEventSessionRevoked, message)
}

func (s *RealtimeService) PropagateAuthenticationCacheInvalidation(
	ctx context.Context,
	userID string,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserID:            userID,
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
	}
	if err := validateSessionRevocation(message); err != nil {
		s.reportInvalidPropagation(ctx, "refusing invalid authentication-cache invalidation", err)
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastSecurityInvalidation(ctx, realtimeClusterEventSessionRevoked, message)
}

func (s *RealtimeService) InvalidateAuthorization(ctx context.Context, userID string) {
	if userID != "" && !model.IsValidId(userID) {
		s.reportInvalidPropagation(
			ctx,
			"refusing invalid authorization invalidation",
			fmt.Errorf("user_id %q", userID),
		)
		return
	}
	s.applyAuthorizationInvalidation(userID)
	s.broadcastSecurityInvalidation(
		ctx,
		realtimeClusterEventAuthorizationInvalidated,
		authorizationInvalidationMessage{UserID: userID},
	)
}

func (s *RealtimeService) reportInvalidPropagation(ctx context.Context, message string, err error) {
	if s.diagnostics == nil {
		return
	}
	s.diagnostics.ErrorContext(ctx, message, err)
}

// broadcastSecurityInvalidation fans out session or authorization invalidation
// best-effort. Correctness recovers from PostgreSQL and bounded cache TTLs when
// peers miss the message.
func (s *RealtimeService) broadcastSecurityInvalidation(ctx context.Context, event string, value any) {
	payload, err := json.Marshal(value)
	if err == nil {
		err = s.broadcast(ctx, event, payload)
	}
	if err != nil {
		s.reportInvalidPropagation(ctx, "security invalidation broadcast failed", err)
	}
}

func (s *RealtimeService) broadcast(
	ctx context.Context,
	event string,
	data []byte,
) error {
	s.mu.RLock()
	cluster := s.cluster
	s.mu.RUnlock()
	if cluster == nil {
		return errors.New("realtime cluster fan-out is not attached")
	}
	return cluster.Broadcast(ctx, event, data)
}

func (s *RealtimeService) handlePeerPublication(ctx context.Context, data []byte) error {
	var publication realtimePublication
	if err := decodeRealtimePayload(data, &publication); err != nil {
		return err
	}
	if publication.Event == nil {
		return errors.New("cluster realtime publication has no event")
	}
	// Peer payloads apply only locally and must not rebroadcast.
	event := publication.Event.toModel()
	if err := event.ValidateForPublish(); err != nil {
		return err
	}
	s.publishLocal(ctx, event)
	return nil
}

func (s *RealtimeService) handlePeerSessionRevocation(ctx context.Context, data []byte) error {
	var revocation sessionRevocationMessage
	if err := decodeRealtimePayload(data, &revocation); err != nil {
		return err
	}
	if err := validateSessionRevocation(revocation); err != nil {
		return err
	}
	s.applySessionRevocation(ctx, revocation)
	return nil
}

func (s *RealtimeService) applySessionRevocation(
	ctx context.Context,
	revocation sessionRevocationMessage,
) {
	s.authentication.deleteAuthenticationCache(ctx, revocation.AccessTokenHashes)
	for _, sessionID := range revocation.SessionIds {
		s.authentication.deleteActivityCache(ctx, sessionID)
	}
	if !revocation.CloseConnections {
		return
	}
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink == nil {
		return
	}
	if len(revocation.SessionIds) == 0 {
		sink.CloseUser(revocation.UserID, ConnectionCloseSessionRevoked)
		return
	}
	for _, sessionID := range revocation.SessionIds {
		sink.CloseSession(sessionID, ConnectionCloseSessionRevoked)
	}
}

func (s *RealtimeService) handlePeerAuthorizationInvalidation(_ context.Context, data []byte) error {
	var invalidation authorizationInvalidationMessage
	if err := decodeRealtimePayload(data, &invalidation); err != nil {
		return err
	}
	if invalidation.UserID != "" && !model.IsValidId(invalidation.UserID) {
		return errors.New("cluster authorization invalidation user ID is invalid")
	}
	s.applyAuthorizationInvalidation(invalidation.UserID)
	return nil
}

func (s *RealtimeService) applyAuthorizationInvalidation(userID string) {
	s.mu.RLock()
	sink := s.sink
	s.mu.RUnlock()
	if sink == nil {
		return
	}
	if userID == "" {
		sink.CloseAll(ConnectionCloseAuthorizationChanged)
		return
	}
	sink.CloseUser(userID, ConnectionCloseAuthorizationChanged)
}

func decodeRealtimePayload(data []byte, target any) error {
	if len(data) == 0 {
		return errors.New("realtime cluster payload is empty")
	}
	return json.Unmarshal(data, target)
}

func validateSessionRevocation(message sessionRevocationMessage) error {
	if !model.IsValidId(message.UserID) {
		return errors.New("session revocation user ID is invalid")
	}
	if len(message.SessionIds) > 1024 || len(message.AccessTokenHashes) > 2048 {
		return errors.New("session revocation exceeds bounded entries")
	}
	for _, sessionID := range message.SessionIds {
		if !model.IsValidId(sessionID) {
			return errors.New("session revocation session ID is invalid")
		}
	}
	for _, hash := range message.AccessTokenHashes {
		if !model.IsValidTokenHash(hash) {
			return errors.New("session revocation access-token hash is invalid")
		}
	}
	return nil
}

func sessionIds(sessions []*model.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session != nil {
			ids = append(ids, session.ID.String())
		}
	}
	return ids
}

func invalidRealtimeRequest(field string) error {
	return NewError("websocket.request.invalid").WithField("field", field)
}

func internalRealtimeError(err error) error {
	return NewError("websocket.internal").Wrap(err)
}

func (a *App) AttachRealtimeSink(sink RealtimeSink) error {
	return a.realtime.SetSink(sink)
}

// AttachRealtimeClusterFanout wires the composition-owned cluster adapter and
// registers peer handlers. It must be called once before the node becomes ready.
func (a *App) AttachRealtimeClusterFanout(fanout RealtimeClusterFanout) error {
	return a.realtime.SetClusterFanout(fanout)
}

// PublishRealtimeEvent publishes a transport-neutral application event after
// durable commit. Prefer this over any transport-shaped construction.
func (a *App) PublishRealtimeEvent(ctx context.Context, event RealtimeEvent) error {
	return a.realtime.Publish(ctx, event)
}

func (a *App) AuthorizeWebSocketSubscription(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	action model.Action,
	resource model.Resource,
) error {
	definition, ok := model.DefinitionForAction(action)
	if !ok || resource.Validate() != nil || definition.ResourceType != resource.Type {
		return invalidRealtimeRequest("subscription")
	}
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		action,
		resource,
		metadata,
	)
}

func (a *App) ValidateWebSocketPrincipal(
	ctx context.Context,
	principal model.Principal,
) error {
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return invalidTokenAppError()
	}
	session, err := a.Store().Session().Get(ctx, principal.SessionID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return authenticationUnavailable(err)
	}
	if session.UserID != principal.UserID ||
		session.IsExpiredAt(time.Now().UTC()) {
		return invalidTokenAppError()
	}
	user, err := a.Store().User().Get(ctx, principal.UserID.String())
	if err != nil || !user.IsActive() {
		return invalidTokenAppError()
	}
	return nil
}
