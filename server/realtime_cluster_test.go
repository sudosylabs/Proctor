// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
	"github.com/sudosylabs/proctor/server/model"
)

type recordingBorrowedCluster struct {
	mu              sync.Mutex
	registrations   []cluster.Event
	handlers        map[cluster.Event]cluster.Handler
	broadcasts      []*cluster.Message
	registrationErr map[cluster.Event]error
}

func (c *recordingBorrowedCluster) NodeID() string { return "node-a" }

func (c *recordingBorrowedCluster) RegisterHandler(event cluster.Event, handler cluster.Handler) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registrations = append(c.registrations, event)
	if err := c.registrationErr[event]; err != nil {
		return err
	}
	if c.handlers == nil {
		c.handlers = make(map[cluster.Event]cluster.Handler)
	}
	c.handlers[event] = handler
	return nil
}

func (c *recordingBorrowedCluster) Broadcast(_ context.Context, message *cluster.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broadcasts = append(c.broadcasts, message.Clone())
	return nil
}

func TestRealtimeClusterAdapterCarriesChildContractWithoutOwningPayloads(t *testing.T) {
	t.Parallel()

	transport := &recordingBorrowedCluster{}
	adapter, err := newRealtimeClusterAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}

	const event = "websocket.publish"
	wantPayload := []byte(`{"event":{"id":"yyyyyyyyyyyyyyyyyyyyyyyyyy"}}`)
	var received []byte
	if err := adapter.RegisterHandler(event, func(_ context.Context, payload []byte) error {
		received = append([]byte(nil), payload...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := transport.handlers[cluster.Event(event)]
	if handler == nil {
		t.Fatalf("handler %q was not registered", event)
	}
	if err := handler(context.Background(), &cluster.Message{
		Event: cluster.Event(event),
		Data:  wantPayload,
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, wantPayload) {
		t.Fatalf("received payload = %q, want %q", received, wantPayload)
	}

	if err := adapter.Broadcast(context.Background(), event, wantPayload); err != nil {
		t.Fatal(err)
	}
	if len(transport.broadcasts) != 1 {
		t.Fatalf("broadcasts = %d, want 1", len(transport.broadcasts))
	}
	message := transport.broadcasts[0]
	if message.Event != cluster.Event(event) || !reflect.DeepEqual(message.Data, wantPayload) || message.Props != nil {
		t.Fatalf("cluster envelope = %#v", message)
	}

	if err := adapter.RegisterHandler(event, nil); err == nil {
		t.Fatal("nil child handler was accepted")
	}
	if err := handler(context.Background(), nil); err == nil {
		t.Fatal("nil cluster message was accepted")
	}
}

func TestRealtimeClusterAdapterRegistersStableChildEventsAndPropagatesFailure(t *testing.T) {
	t.Parallel()

	registrationErr := errors.New("registration unavailable")
	transport := &recordingBorrowedCluster{registrationErr: map[cluster.Event]error{
		cluster.Event("authorization.invalidated"): registrationErr,
	}}
	adapter, err := newRealtimeClusterAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}
	service, err := apprealtime.New(noopRealtimeInvalidator{}, silentClusterLogger{})
	if err != nil {
		t.Fatal(err)
	}
	err = service.SetClusterFanout(adapter)
	if !errors.Is(err, registrationErr) {
		t.Fatalf("SetClusterFanout() error = %v, want %v", err, registrationErr)
	}
	want := []cluster.Event{
		cluster.Event("websocket.publish"),
		cluster.Event("exam_attempt.connection_unbound"),
		cluster.Event("authentication.session_revoked"),
		cluster.Event("authorization.invalidated"),
	}
	if !reflect.DeepEqual(transport.registrations, want) {
		t.Fatalf("registrations = %#v, want %#v", transport.registrations, want)
	}
	if err := service.SetClusterFanout(adapter); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("same-instance retry error = %v", err)
	}
}

func TestRealtimeClusterAdapterPreservesSingleNodeLocalFirstPublication(t *testing.T) {
	t.Parallel()

	transport, err := local.New("node-a", silentClusterLogger{})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := newRealtimeClusterAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}
	service, err := apprealtime.New(noopRealtimeInvalidator{}, silentClusterLogger{})
	if err != nil {
		t.Fatal(err)
	}
	sink := &countingRealtimeSink{}
	if err := service.SetSink(sink); err != nil {
		t.Fatal(err)
	}
	if err := service.SetClusterFanout(adapter); err != nil {
		t.Fatal(err)
	}
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Stop(context.Background()); err != nil {
			t.Errorf("stop local cluster: %v", err)
		}
	})

	if err := service.Publish(context.Background(), apprealtime.RealtimeEvent{
		Name:   "user.notification",
		UserID: model.NewId(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := sink.publishCount(); got != 1 {
		t.Fatalf("local publications = %d, want 1", got)
	}
}

type countingRealtimeSink struct {
	mu        sync.Mutex
	published int
}

func (s *countingRealtimeSink) PublishLocal(context.Context, apprealtime.RealtimeEvent) {
	s.mu.Lock()
	s.published++
	s.mu.Unlock()
}

func (*countingRealtimeSink) CloseSession(string, apprealtime.ConnectionCloseReason) {}
func (*countingRealtimeSink) CloseUser(string, apprealtime.ConnectionCloseReason)    {}
func (*countingRealtimeSink) CloseAll(apprealtime.ConnectionCloseReason)             {}
func (*countingRealtimeSink) UnbindExamAttemptConnection(model.AttemptConnectionID)  {}

func (s *countingRealtimeSink) publishCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.published
}

type silentClusterLogger struct{}

func (silentClusterLogger) ErrorContext(context.Context, string, error) {}

type noopRealtimeInvalidator struct{}

func (noopRealtimeInvalidator) InvalidateAccessCredentials(context.Context, []string) {}
func (noopRealtimeInvalidator) InvalidateSessionActivity(context.Context, []string)   {}

var _ apprealtime.ClusterFanout = (*realtimeClusterAdapter)(nil)
var _ apprealtime.Sink = (*countingRealtimeSink)(nil)
