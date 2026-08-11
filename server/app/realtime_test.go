// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRealtimeEventValidateForPublish(t *testing.T) {
	t.Parallel()

	unitID := model.NewId()
	valid := RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	}
	if err := valid.ValidateForPublish(); err != nil {
		t.Fatalf("valid event: %v", err)
	}

	userTargeted := RealtimeEvent{
		Name:   "user.notification",
		UserID: model.NewId(),
	}
	if err := userTargeted.ValidateForPublish(); err != nil {
		t.Fatalf("user-targeted event: %v", err)
	}

	tests := []struct {
		name  string
		event RealtimeEvent
	}{
		{name: "empty name", event: RealtimeEvent{UserID: model.NewId()}},
		{name: "missing target", event: RealtimeEvent{Name: "orphan.event"}},
		{name: "invalid data", event: RealtimeEvent{
			Name: "x", UserID: model.NewId(), Data: json.RawMessage(`{`),
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.event.ValidateForPublish(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRealtimePublishIsLocalFirstAndLoopFree(t *testing.T) {
	t.Parallel()

	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	if err := service.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}

	unitID := model.NewId()
	event := RealtimeEvent{
		Name:   "academic_unit_created",
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   unitID,
		},
	}
	if err := service.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("local publishes = %d, want 1", len(sink.events))
	}
	if sink.events[0].Name != "academic_unit_created" || sink.events[0].ID == "" {
		t.Fatalf("local event = %#v", sink.events[0])
	}
	if len(cluster.broadcasts) != 1 {
		t.Fatalf("cluster broadcasts = %d, want 1", len(cluster.broadcasts))
	}
	if cluster.broadcasts[0].event != realtimeClusterEventPublication {
		t.Fatalf("broadcast = %#v", cluster.broadcasts[0])
	}
	var wire struct {
		Event struct {
			Resource map[string]json.RawMessage `json:"resource"`
		} `json:"event"`
	}
	if err := json.Unmarshal(cluster.broadcasts[0].data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Event.Resource) != 2 || wire.Event.Resource["type"] == nil ||
		wire.Event.Resource["id"] == nil {
		t.Fatalf("cluster resource wire shape = %s", cluster.broadcasts[0].data)
	}

	// Peer handler must apply only locally and must not rebroadcast.
	if err := service.handlePeerPublication(context.Background(), cluster.broadcasts[0].data); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("after peer local publishes = %d, want 2", len(sink.events))
	}
	if len(cluster.broadcasts) != 1 {
		t.Fatalf("peer path rebroadcast: broadcasts = %d", len(cluster.broadcasts))
	}
}

func TestRealtimePublishRequiresClusterFanout(t *testing.T) {
	t.Parallel()

	service := newTestRealtimeService(t, noopAuthenticationCache{})
	_ = service.SetSink(&recordingRealtimeSink{})
	err := service.Publish(context.Background(), RealtimeEvent{
		Name:   "user.notification",
		UserID: model.NewId(),
	})
	if err == nil || !Is(err, "websocket.internal") {
		t.Fatalf("Publish() error = %v, want websocket.internal", err)
	}
}

func TestRealtimeSessionRevocationClosesLocalSessions(t *testing.T) {
	t.Parallel()

	sink := &recordingRealtimeSink{}
	cluster := &recordingRealtimeCluster{}
	service := newTestRealtimeService(t, noopAuthenticationCache{})
	if err := service.SetClusterFanout(cluster); err != nil {
		t.Fatal(err)
	}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}

	userID := model.NewId()
	sessionID := model.NewId()
	service.SessionsRevoked(context.Background(), userID, []string{sessionID}, nil)
	if len(sink.sessionCloses) != 1 || sink.sessionCloses[0].id != sessionID {
		t.Fatalf("session closes = %#v", sink.sessionCloses)
	}
	if sink.sessionCloses[0].reason != ConnectionCloseSessionRevoked {
		t.Fatalf("close reason = %q", sink.sessionCloses[0].reason)
	}
	if len(cluster.broadcasts) != 1 ||
		cluster.broadcasts[0].event != realtimeClusterEventSessionRevoked {
		t.Fatalf("revocation broadcast = %#v", cluster.broadcasts)
	}
}

type noopAuthenticationCache struct{}

func (noopAuthenticationCache) Get(context.Context, string) ([]byte, error) {
	return nil, errAuthenticationCacheMiss
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

func newTestRealtimeService(t *testing.T, cache authenticationCache) *RealtimeService {
	t.Helper()
	invalidator, err := newAuthenticationCacheInvalidator(cache, &securityEffectsDiagnosticsFake{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := newRealtimeService(invalidator, &securityEffectsRealtimeDiagnosticsFake{})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type recordingRealtimeSink struct {
	mu            sync.Mutex
	events        []RealtimeEvent
	sessionCloses []closeRecord
	userCloses    []closeRecord
	allCloses     []ConnectionCloseReason
}

type closeRecord struct {
	id     string
	reason ConnectionCloseReason
}

func (s *recordingRealtimeSink) PublishLocal(_ context.Context, event RealtimeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event.Clone())
}

func (s *recordingRealtimeSink) CloseSession(sessionID string, reason ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionCloses = append(s.sessionCloses, closeRecord{id: sessionID, reason: reason})
}

func (s *recordingRealtimeSink) CloseUser(userID string, reason ConnectionCloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userCloses = append(s.userCloses, closeRecord{id: userID, reason: reason})
}

func (s *recordingRealtimeSink) CloseAll(reason ConnectionCloseReason) {
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
