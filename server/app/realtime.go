// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

// realtimeDiagnostics extends the child module's bounded diagnostics only for
// application-owned reporting of failed post-commit effects.
type realtimeDiagnostics interface {
	apprealtime.Diagnostics
	ErrorContextWithEvent(ctx context.Context, message, event string, err error)
}

// realtimeService translates the child module's publication failures into
// parent application errors and retains parent-owned effect facades.
type realtimeService struct {
	delivery    *apprealtime.Service
	diagnostics realtimeDiagnostics
}

func newRealtimeService(
	authenticationInvalidator authenticationInvalidator,
	diagnostics realtimeDiagnostics,
) (*realtimeService, error) {
	delivery, err := apprealtime.New(authenticationInvalidator, diagnostics)
	if err != nil {
		return nil, err
	}
	return newRealtimeServiceWithDelivery(delivery, diagnostics)
}

func newRealtimeServiceWithDelivery(
	delivery *apprealtime.Service,
	diagnostics realtimeDiagnostics,
) (*realtimeService, error) {
	if delivery == nil {
		return nil, errors.New("realtime delivery is required")
	}
	if diagnostics == nil {
		return nil, errors.New("realtime diagnostics are required")
	}
	return &realtimeService{
		delivery:    delivery,
		diagnostics: diagnostics,
	}, nil
}

// SetClusterFanout attaches the composition adapter and registers peer
// handlers. Peer handlers apply only local effects so Broadcast never loops.
func (s *realtimeService) SetClusterFanout(fanout apprealtime.ClusterFanout) error {
	return s.delivery.SetClusterFanout(fanout)
}

func (s *realtimeService) SetSink(sink apprealtime.Sink) error {
	return s.delivery.SetSink(sink)
}

// Publish delivers a transport-neutral event locally first, then fans it out
// to peers best-effort. Callers must invoke this only after durable commit.
func (s *realtimeService) Publish(ctx context.Context, event apprealtime.RealtimeEvent) error {
	err := s.delivery.Publish(ctx, event)
	if err == nil {
		return nil
	}
	var invalid *apprealtime.InvalidPublicationError
	if errors.As(err, &invalid) {
		return invalidRealtimeRequest(invalid.Error())
	}
	return internalRealtimeError(err)
}

func (s *realtimeService) reportTransientFailure(
	ctx context.Context,
	event string,
	err error,
) {
	if s.diagnostics == nil {
		return
	}
	s.diagnostics.ErrorContextWithEvent(ctx, "transient realtime publication failed", event, err)
}

func (s *realtimeService) SessionsRevoked(
	ctx context.Context,
	userID string,
	sessionIDs []string,
	accessTokenHashes []string,
) {
	s.delivery.SessionsRevoked(ctx, userID, sessionIDs, accessTokenHashes)
}

func (s *realtimeService) AuthenticationCacheInvalidated(
	ctx context.Context,
	userID string,
	accessTokenHashes []string,
) {
	s.delivery.AuthenticationCacheInvalidated(ctx, userID, accessTokenHashes)
}

func (s *realtimeService) InvalidateAuthorization(ctx context.Context, userID string) {
	s.delivery.InvalidateAuthorization(ctx, userID)
}

var _ authenticationSecurityEffects = (*realtimeService)(nil)

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

func (a *App) AttachRealtimeSink(sink apprealtime.Sink) error {
	return a.realtime.SetSink(sink)
}

// AttachRealtimeClusterFanout wires the composition-owned cluster adapter and
// registers peer handlers. It must be called once before the node becomes ready.
func (a *App) AttachRealtimeClusterFanout(fanout apprealtime.ClusterFanout) error {
	return a.realtime.SetClusterFanout(fanout)
}

// PublishRealtimeEvent publishes a transport-neutral application event after
// durable commit. Prefer this over any transport-shaped construction.
func (a *App) PublishRealtimeEvent(ctx context.Context, event apprealtime.RealtimeEvent) error {
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
	if action == model.ActionExamView && resource.Type == model.ResourceExam {
		examID, err := model.ParseExamID(resource.ID)
		if err != nil {
			return invalidRealtimeRequest("subscription")
		}
		if err := a.exams.AuthorizeView(ctx, examengine.NewCall(principal, metadata), examID); err != nil {
			return examError(err, true)
		}
		return nil
	}
	return a.Authorize(
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
	return a.authentication.ValidatePrincipal(ctx, principal)
}
