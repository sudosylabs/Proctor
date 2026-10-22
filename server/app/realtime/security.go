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
// server/channels/app/platform/cluster_handlers.go. See doc.go and the server
// NOTICE for provenance and licensing details.

package realtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	clusterEventSessionRevoked           = "authentication.session_revoked"
	clusterEventAuthorizationInvalidated = "authorization.invalidated"
	securityOperationSessionRevocation   = "session revocation"
	securityOperationAuthenticationCache = "authentication-cache invalidation"
	securityOperationAuthorization       = "authorization invalidation"
)

type sessionRevocationMessage struct {
	UserID            string   `json:"user_id"`
	SessionIDs        []string `json:"session_ids"`
	AccessTokenHashes []string `json:"access_token_hashes"`
	CloseConnections  bool     `json:"close_connections"`
}

type authorizationInvalidationMessage struct {
	UserID string `json:"user_id,omitempty"`
}

// SessionsRevoked applies local cache and connection effects before
// best-effort peer propagation. Invalid input is rejected without effects.
func (s *Service) SessionsRevoked(
	ctx context.Context,
	userID model.UserID,
	sessionIDs []model.SessionID,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserID:            userID.String(),
		SessionIDs:        sessionIDStrings(sessionIDs),
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
		CloseConnections:  true,
	}
	if err := validateSessionRevocation(message); err != nil {
		s.reportSecurityFailure(ctx, "refusing invalid session revocation propagation", "invalid security propagation")
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastSecurityInvalidation(
		ctx, securityOperationSessionRevocation, clusterEventSessionRevoked, message,
	)
}

// AuthenticationCacheInvalidated clears the available local authentication
// cache entries before best-effort peer propagation without closing sockets.
func (s *Service) AuthenticationCacheInvalidated(
	ctx context.Context,
	userID model.UserID,
	accessTokenHashes []string,
) {
	message := sessionRevocationMessage{
		UserID:            userID.String(),
		AccessTokenHashes: append([]string(nil), accessTokenHashes...),
	}
	if err := validateSessionRevocation(message); err != nil {
		s.reportSecurityFailure(ctx, "refusing invalid authentication-cache invalidation", "invalid security propagation")
		return
	}
	s.applySessionRevocation(ctx, message)
	s.broadcastSecurityInvalidation(
		ctx, securityOperationAuthenticationCache, clusterEventSessionRevoked, message,
	)
}

// InvalidateAuthorization closes the affected local connections before
// best-effort peer propagation. An empty user ID targets every connection.
func (s *Service) InvalidateAuthorization(ctx context.Context, userID model.UserID) {
	if userID != "" && !model.UserID(userID).IsValid() {
		s.reportSecurityFailure(ctx, "refusing invalid authorization invalidation", "invalid security propagation")
		return
	}
	s.applyAuthorizationInvalidation(userID.String())
	s.broadcastSecurityInvalidation(
		ctx,
		securityOperationAuthorization,
		clusterEventAuthorizationInvalidated,
		authorizationInvalidationMessage{UserID: userID.String()},
	)
}

func (s *Service) applySessionRevocation(
	ctx context.Context,
	revocation sessionRevocationMessage,
) {
	s.mu.RLock()
	invalidator := s.deps.AuthenticationInvalidator
	sink := s.sink
	s.mu.RUnlock()

	if invalidator != nil {
		invalidator.InvalidateAccessCredentials(ctx, revocation.AccessTokenHashes)
		invalidator.InvalidateSessionActivity(ctx, sessionIDsFrom(revocation.SessionIDs))
	}
	if !revocation.CloseConnections || sink == nil {
		return
	}
	if len(revocation.SessionIDs) == 0 {
		sink.CloseUser(revocation.UserID, ConnectionCloseSessionRevoked)
		return
	}
	for _, sessionID := range revocation.SessionIDs {
		sink.CloseSession(sessionID, ConnectionCloseSessionRevoked)
	}
}

func (s *Service) applyAuthorizationInvalidation(userID string) {
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

func (s *Service) broadcastSecurityInvalidation(
	ctx context.Context,
	operation string,
	event string,
	value any,
) {
	message := operation + " broadcast failed"
	payload, err := json.Marshal(value)
	if err != nil {
		s.reportSecurityFailure(ctx, message, "security propagation encoding failed")
		return
	}
	s.mu.RLock()
	fanout := s.fanout
	s.mu.RUnlock()
	if fanout == nil {
		s.reportSecurityFailure(ctx, message, "realtime cluster fan-out is not attached")
		return
	}
	if err := fanout.Broadcast(ctx, event, payload); err != nil {
		s.reportSecurityFailure(ctx, message, "realtime cluster fan-out failed")
	}
}

func (s *Service) reportSecurityFailure(ctx context.Context, message, category string) {
	s.mu.RLock()
	diagnostics := s.deps.Diagnostics
	s.mu.RUnlock()
	if diagnostics != nil {
		diagnostics.ErrorContext(ctx, message, errors.New(category))
	}
}

func (s *Service) handlePeerSessionRevocation(ctx context.Context, data []byte) error {
	var revocation sessionRevocationMessage
	if err := decodePayload(data, &revocation); err != nil {
		return err
	}
	if err := validateSessionRevocation(revocation); err != nil {
		return err
	}
	s.applySessionRevocation(ctx, revocation)
	return nil
}

func (s *Service) handlePeerAuthorizationInvalidation(_ context.Context, data []byte) error {
	var invalidation authorizationInvalidationMessage
	if err := decodePayload(data, &invalidation); err != nil {
		return err
	}
	if invalidation.UserID != "" && !model.UserID(invalidation.UserID).IsValid() {
		return errors.New("cluster authorization invalidation user ID is invalid")
	}
	s.applyAuthorizationInvalidation(invalidation.UserID)
	return nil
}

func validateSessionRevocation(message sessionRevocationMessage) error {
	if !model.UserID(message.UserID).IsValid() {
		return errors.New("session revocation user ID is invalid")
	}
	if len(message.SessionIDs) > 1024 || len(message.AccessTokenHashes) > 2048 {
		return errors.New("session revocation exceeds bounded entries")
	}
	for _, sessionID := range message.SessionIDs {
		if !model.SessionID(sessionID).IsValid() {
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

// sessionIDsFrom types the Session IDs of a validated peer message.
func sessionIDsFrom(spelled []string) []model.SessionID {
	if len(spelled) == 0 {
		return nil
	}
	ids := make([]model.SessionID, len(spelled))
	for index, id := range spelled {
		ids[index] = model.SessionID(id)
	}
	return ids
}

// sessionIDStrings spells Session IDs for the cluster peer message.
func sessionIDStrings(ids []model.SessionID) []string {
	if len(ids) == 0 {
		return nil
	}
	spelled := make([]string, len(ids))
	for index, id := range ids {
		spelled[index] = id.String()
	}
	return spelled
}
