// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost's public application WebSocket publication and
// cluster-handler flow. Proctor keeps the local-first, peer-local-only
// delivery invariant while using its own event and authorization domains.

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	WebSocketCloseSessionRevoked       = 4001
	WebSocketCloseAuthorizationChanged = 4003
)

// RealtimeSink is implemented by the socket boundary. It deliberately
// contains no HTTP or concrete WebSocket type so application publication is
// independently testable.
type RealtimeSink interface {
	PublishLocal(context.Context, *model.WebSocketEvent)
	CloseSession(string, int, string)
	CloseUser(string, int, string)
	CloseAll(int, string)
}

type RealtimeService struct {
	platform       *platform.Service
	authentication *AuthenticationService
	mu             sync.RWMutex
	sink           RealtimeSink
}

type webSocketPublication struct {
	Event *model.WebSocketEvent `json:"event"`
}

type sessionRevocationMessage struct {
	UserId            string   `json:"user_id"`
	SessionIds        []string `json:"session_ids"`
	AccessTokenHashes []string `json:"access_token_hashes"`
	CloseConnections  bool     `json:"close_connections"`
}

type authorizationInvalidationMessage struct {
	UserId string `json:"user_id,omitempty"`
}

func newRealtimeService(
	applicationPlatform *platform.Service,
	authentication *AuthenticationService,
) (*RealtimeService, error) {
	service := &RealtimeService{
		platform: applicationPlatform, authentication: authentication,
	}
	for event, handler := range map[model.ClusterEvent]platform.ClusterMessageHandler{
		model.ClusterEventWebSocketPublish:         service.handleClusterPublication,
		model.ClusterEventSessionRevoked:           service.handleClusterSessionRevocation,
		model.ClusterEventAuthorizationInvalidated: service.handleClusterAuthorizationInvalidation,
	} {
		if err := applicationPlatform.Cluster().RegisterMessageHandler(event, handler); err != nil {
			return nil, fmt.Errorf("register %s cluster handler: %w", event, err)
		}
	}
	return service, nil
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

func (s *RealtimeService) Publish(
	ctx context.Context,
	event *model.WebSocketEvent,
	sendType model.ClusterSendType,
) *model.AppError {
	if event == nil {
		return invalidRealtimeRequest("RealtimeService.Publish", "event")
	}
	candidate := event.Clone()
	if candidate.Id == "" {
		candidate.Id = model.NewId()
	}
	if err := candidate.ValidateForPublish(); err != nil {
		return invalidRealtimeRequest("RealtimeService.Publish", err.Error())
	}
	switch sendType {
	case model.ClusterSendBestEffort, model.ClusterSendReliable:
	default:
		return invalidRealtimeRequest("RealtimeService.Publish", "send_type")
	}

	// This is the same loop-prevention shape as Mattermost: publish locally
	// once, then send to peers. The peer handler calls only publishLocal.
	s.publishLocal(ctx, candidate)
	payload, err := json.Marshal(webSocketPublication{Event: candidate})
	if err != nil {
		return internalRealtimeError("RealtimeService.Publish.marshal", err)
	}
	if err := s.platform.Cluster().Broadcast(ctx, &model.ClusterMessage{
		Event:    model.ClusterEventWebSocketPublish,
		SendType: sendType,
		Data:     payload,
	}); err != nil {
		return internalRealtimeError("RealtimeService.Publish.cluster", err)
	}
	return nil
}

func (s *RealtimeService) publishLocal(
	ctx context.Context,
	event *model.WebSocketEvent,
) {
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
	s.platform.Log().ErrorContext(
		ctx,
		"transient realtime publication failed",
		mlog.String("event", event),
		mlog.Err(err),
	)
}

func (s *RealtimeService) PropagateSessionRevocation(
	ctx context.Context,
	userID string,
	sessionIDs []string,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserId:            userID,
		SessionIds:        append([]string(nil), sessionIDs...),
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
		CloseConnections:  true,
	}
	if err := validateSessionRevocation(message); err != nil {
		s.platform.Log().ErrorContext(ctx, "refusing invalid session revocation propagation", mlog.Err(err))
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastReliable(ctx, model.ClusterEventSessionRevoked, message)
}

func (s *RealtimeService) PropagateAuthenticationCacheInvalidation(
	ctx context.Context,
	userID string,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserId:            userID,
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
	}
	if err := validateSessionRevocation(message); err != nil {
		s.platform.Log().ErrorContext(
			ctx,
			"refusing invalid authentication-cache invalidation",
			mlog.Err(err),
		)
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastReliable(ctx, model.ClusterEventSessionRevoked, message)
}

func (s *RealtimeService) InvalidateAuthorization(
	ctx context.Context,
	userID string,
) {
	if userID != "" && !model.IsValidId(userID) {
		s.platform.Log().ErrorContext(
			ctx,
			"refusing invalid authorization invalidation",
			mlog.String("user_id", userID),
		)
		return
	}
	s.applyAuthorizationInvalidation(userID)
	s.broadcastReliable(
		ctx,
		model.ClusterEventAuthorizationInvalidated,
		authorizationInvalidationMessage{UserId: userID},
	)
}

func (s *RealtimeService) broadcastReliable(
	ctx context.Context,
	event model.ClusterEvent,
	value any,
) {
	payload, err := json.Marshal(value)
	if err == nil {
		err = s.platform.Cluster().Broadcast(ctx, &model.ClusterMessage{
			Event: event, SendType: model.ClusterSendReliable, Data: payload,
		})
	}
	if err != nil {
		s.platform.Log().ErrorContext(
			ctx,
			"reliable security invalidation broadcast failed",
			mlog.String("event", string(event)),
			mlog.Err(err),
		)
	}
}

func (s *RealtimeService) handleClusterPublication(
	ctx context.Context,
	message *model.ClusterMessage,
) error {
	var publication webSocketPublication
	if err := decodeClusterData(message, &publication); err != nil {
		return err
	}
	if publication.Event == nil {
		return errors.New("cluster WebSocket publication has no event")
	}
	if err := publication.Event.ValidateForPublish(); err != nil {
		return err
	}
	s.publishLocal(ctx, publication.Event)
	return nil
}

func (s *RealtimeService) handleClusterSessionRevocation(
	ctx context.Context,
	message *model.ClusterMessage,
) error {
	var revocation sessionRevocationMessage
	if err := decodeClusterData(message, &revocation); err != nil {
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
		sink.CloseUser(
			revocation.UserId,
			WebSocketCloseSessionRevoked,
			"session revoked",
		)
		return
	}
	for _, sessionID := range revocation.SessionIds {
		sink.CloseSession(
			sessionID,
			WebSocketCloseSessionRevoked,
			"session revoked",
		)
	}
}

func (s *RealtimeService) handleClusterAuthorizationInvalidation(
	_ context.Context,
	message *model.ClusterMessage,
) error {
	var invalidation authorizationInvalidationMessage
	if err := decodeClusterData(message, &invalidation); err != nil {
		return err
	}
	if invalidation.UserId != "" && !model.IsValidId(invalidation.UserId) {
		return errors.New("cluster authorization invalidation user ID is invalid")
	}
	s.applyAuthorizationInvalidation(invalidation.UserId)
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
		sink.CloseAll(
			WebSocketCloseAuthorizationChanged,
			"authorization changed",
		)
		return
	}
	sink.CloseUser(
		userID,
		WebSocketCloseAuthorizationChanged,
		"authorization changed",
	)
}

func decodeClusterData(message *model.ClusterMessage, target any) error {
	if message == nil {
		return errors.New("cluster message is nil")
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if len(message.Data) == 0 {
		return errors.New("cluster message data is empty")
	}
	return json.Unmarshal(message.Data, target)
}

func validateSessionRevocation(message sessionRevocationMessage) error {
	if !model.IsValidId(message.UserId) {
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
			ids = append(ids, session.Id)
		}
	}
	return ids
}

func invalidRealtimeRequest(where, field string) *model.AppError {
	return model.NewAppError(
		where,
		"websocket.request.invalid",
		nil,
		"",
		http.StatusBadRequest,
	).WithSafeFields(map[string]string{"field": field})
}

func internalRealtimeError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"websocket.internal",
		nil,
		"",
		http.StatusInternalServerError,
	).Wrap(err)
}

func (a *App) AttachRealtimeSink(sink RealtimeSink) error {
	return a.realtime.SetSink(sink)
}

func (a *App) PublishWebSocketEvent(
	ctx context.Context,
	event *model.WebSocketEvent,
	sendType model.ClusterSendType,
) *model.AppError {
	return a.realtime.Publish(ctx, event, sendType)
}

func (a *App) AuthorizeWebSocketSubscription(
	ctx context.Context,
	principal model.Principal,
	metadata model.RequestMetadata,
	subscription model.WebSocketSubscription,
) *model.AppError {
	if !subscription.IsValid() {
		return invalidRealtimeRequest("AuthorizeWebSocketSubscription", "subscription")
	}
	return a.AuthorizePrincipalTo(
		ctx,
		principal,
		subscription.Action,
		subscription.Resource,
		metadata,
	)
}

func (a *App) ValidateWebSocketPrincipal(
	ctx context.Context,
	principal model.Principal,
) *model.AppError {
	if !principal.IsValid() || principal.CredentialType != model.CredentialSessionAccess {
		return invalidTokenError("ValidateWebSocketPrincipal")
	}
	session, err := a.Store().Session().Get(ctx, principal.SessionId)
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenError("ValidateWebSocketPrincipal.session")
		}
		return internalAuthenticationError("ValidateWebSocketPrincipal.session", err)
	}
	if session.UserId != principal.UserId ||
		session.IsExpiredAt(time.Now().UnixMilli()) {
		return invalidTokenError("ValidateWebSocketPrincipal.session")
	}
	user, err := a.Store().User().Get(ctx, principal.UserId)
	if err != nil || !user.IsActive() {
		return invalidTokenError("ValidateWebSocketPrincipal.user")
	}
	return nil
}
