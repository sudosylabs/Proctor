// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRealtimePublishRequiresClusterFanout(t *testing.T) {
	t.Parallel()

	service := newTestRealtimeService(t, noopAuthenticationCache{})
	_ = service.AttachRealtimeSink(&recordingRealtimeSink{})
	err := service.PublishRealtimeEvent(context.Background(), realtime.RealtimeEvent{
		Name:   "user.notification",
		UserID: model.NewID(),
	})
	require.Error(t, err, "Publish() error")
	require.True(t, apperror.Is(err, "websocket.internal"), "Publish() error")
}

func TestRealtimePublishMapsInvalidPublication(t *testing.T) {
	t.Parallel()

	service := newTestRealtimeService(t, noopAuthenticationCache{})
	err := service.PublishRealtimeEvent(context.Background(), realtime.RealtimeEvent{Name: "missing.target"})
	require.Error(t, err, "Publish() error")
	require.True(t, apperror.Is(err, "websocket.request.invalid"), "Publish() error")
}

func TestSessionRevocationPublishesAttemptProjectionInvalidations(t *testing.T) {
	t.Parallel()
	candidateID := model.NewUserID()
	sessionID := model.NewSessionID()
	target := store.ExamAttemptInvalidationTarget{
		CandidateUserID: candidateID,
		ExamID:          model.NewExamID(),
		SittingID:       model.NewExamSittingID(),
	}
	attempts := &sessionRevocationAttemptProjectionFake{targets: []store.ExamAttemptInvalidationTarget{target}}
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	service.attempts = attempts
	sink := &recordingRealtimeSink{}
	require.NoError(t, service.AttachRealtimeSink(sink))
	require.NoError(t, service.AttachRealtimeClusterFanout(&recordingRealtimeCluster{}))

	service.SessionsRevoked(
		context.Background(), candidateID.String(), []string{sessionID.String()}, nil,
	)
	require.Equal(t, candidateID, attempts.candidateID, "attempt invalidation query = candidate")
	require.Len(t, attempts.sessionIDs, 1, "attempt invalidation query = candidate")
	require.Equal(t, sessionID, attempts.sessionIDs[0], "attempt invalidation query = candidate")
	require.Len(t, sink.events, 3)
	require.Equal(t, "candidate.exam_activity.changed", sink.events[0].Name)
	require.Equal(t, candidateID.String(), sink.events[0].UserID)
	require.Equal(t, "manager.sitting_board.changed", sink.events[1].Name)
	require.Equal(t, target.SittingID.String(), sink.events[1].Resource.ID)
	require.Equal(t, "current_user.context.changed", sink.events[2].Name)
	require.Equal(t, candidateID.String(), sink.events[2].UserID)
}

type sessionRevocationAttemptProjectionFake struct {
	candidateID model.UserID
	sessionIDs  []model.SessionID
	targets     []store.ExamAttemptInvalidationTarget
	err         error
}

func (f *sessionRevocationAttemptProjectionFake) ListSessionRevocationInvalidationTargets(
	_ context.Context,
	candidateID model.UserID,
	sessionIDs []model.SessionID,
) ([]store.ExamAttemptInvalidationTarget, error) {
	f.candidateID = candidateID
	f.sessionIDs = append([]model.SessionID(nil), sessionIDs...)
	return append([]store.ExamAttemptInvalidationTarget(nil), f.targets...), f.err
}

type noopAuthenticationCache struct{}

func (noopAuthenticationCache) Get(context.Context, string) ([]byte, error) {
	return nil, ErrAuthenticationCacheMiss
}
func (noopAuthenticationCache) SetAlways(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (noopAuthenticationCache) SetIfAbsent(context.Context, string, []byte, time.Duration) error {
	return nil
}
func (noopAuthenticationCache) Delete(context.Context, string) error { return nil }
func (noopAuthenticationCache) Add(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}

func newTestRealtimeService(t *testing.T, cache authenticationCache) *realtimeService {
	t.Helper()
	invalidator, err := newAuthenticationCacheInvalidator(cache, &securityEffectsDiagnosticsFake{})
	require.NoError(t, err)
	service, err := newRealtimeService(
		invalidator, &securityEffectsRealtimeDiagnosticsFake{},
	)
	require.NoError(t, err)
	return service
}

type recordingRealtimeSink struct {
	mu             sync.Mutex
	events         []realtime.RealtimeEvent
	sessionCloses  []closeRecord
	userCloses     []closeRecord
	allCloses      []realtime.ConnectionCloseReason
	attemptUnbinds []model.AttemptConnectionID
}

func (s *recordingRealtimeSink) UnbindExamAttemptConnection(connectionID model.AttemptConnectionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attemptUnbinds = append(s.attemptUnbinds, connectionID)
}

type closeRecord struct {
	id     string
	reason realtime.ConnectionCloseReason
}

func (s *recordingRealtimeSink) PublishLocal(_ context.Context, event realtime.RealtimeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event.Clone())
}

func (s *recordingRealtimeSink) CloseSession(sessionID string, reason realtime.ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionCloses = append(s.sessionCloses, closeRecord{id: sessionID, reason: reason})
}

func (s *recordingRealtimeSink) CloseUser(userID string, reason realtime.ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userCloses = append(s.userCloses, closeRecord{id: userID, reason: reason})
}

func (s *recordingRealtimeSink) CloseAll(reason realtime.ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allCloses = append(s.allCloses, reason)
}

type recordingRealtimeCluster struct {
	mu         sync.Mutex
	handlers   map[string]func(context.Context, []byte) error
	broadcasts []clusterBroadcast
}

type clusterBroadcast struct {
	event string
	data  []byte
}

func (c *recordingRealtimeCluster) RegisterHandler(
	event string,
	handler func(context.Context, []byte) error,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handlers == nil {
		c.handlers = map[string]func(context.Context, []byte) error{}
	}
	if _, exists := c.handlers[event]; exists {
		return errors.New("handler already registered")
	}
	c.handlers[event] = handler
	return nil
}

func (c *recordingRealtimeCluster) Broadcast(
	_ context.Context,
	event string,
	data []byte,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broadcasts = append(c.broadcasts, clusterBroadcast{
		event: event,
		data:  append([]byte(nil), data...),
	})
	return nil
}
