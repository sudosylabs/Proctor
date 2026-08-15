// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestRealtimePublishRequiresClusterFanout(t *testing.T) {
	t.Parallel()

	service := newTestRealtimeService(t, noopAuthenticationCache{})
	_ = service.SetSink(&recordingRealtimeSink{})
	err := service.Publish(context.Background(), apprealtime.RealtimeEvent{
		Name:   "user.notification",
		UserID: model.NewId(),
	})
	if err == nil || !Is(err, "websocket.internal") {
		t.Fatalf("Publish() error = %v, want websocket.internal", err)
	}
}

func TestRealtimePublishMapsInvalidPublication(t *testing.T) {
	t.Parallel()

	service := newTestRealtimeService(t, noopAuthenticationCache{})
	err := service.Publish(context.Background(), apprealtime.RealtimeEvent{Name: "missing.target"})
	if err == nil || !Is(err, "websocket.request.invalid") {
		t.Fatalf("Publish() error = %v, want websocket.request.invalid", err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRealtimeService(
		invalidator, &securityEffectsRealtimeDiagnosticsFake{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type recordingRealtimeSink struct {
	mu             sync.Mutex
	events         []apprealtime.RealtimeEvent
	sessionCloses  []closeRecord
	userCloses     []closeRecord
	allCloses      []apprealtime.ConnectionCloseReason
	attemptUnbinds []model.AttemptConnectionID
}

func (s *recordingRealtimeSink) UnbindExamAttemptConnection(connectionID model.AttemptConnectionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attemptUnbinds = append(s.attemptUnbinds, connectionID)
}

type closeRecord struct {
	id     string
	reason apprealtime.ConnectionCloseReason
}

func (s *recordingRealtimeSink) PublishLocal(_ context.Context, event apprealtime.RealtimeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event.Clone())
}

func (s *recordingRealtimeSink) CloseSession(sessionID string, reason apprealtime.ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionCloses = append(s.sessionCloses, closeRecord{id: sessionID, reason: reason})
}

func (s *recordingRealtimeSink) CloseUser(userID string, reason apprealtime.ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userCloses = append(s.userCloses, closeRecord{id: userID, reason: reason})
}

func (s *recordingRealtimeSink) CloseAll(reason apprealtime.ConnectionCloseReason) {
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
