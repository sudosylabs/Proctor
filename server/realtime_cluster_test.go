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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/realtime"
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
	require.NoError(t, err)

	const event = "websocket.publish"
	wantPayload := []byte(`{"event":{"id":"yyyyyyyyyyyyyyyyyyyyyyyyyy"}}`)
	var received []byte
	require.NoError(t, adapter.RegisterHandler(event, func(_ context.Context, payload []byte) error {
		received = append([]byte(nil), payload...)
		return nil
	}))
	handler := transport.handlers[cluster.Event(event)]
	require.NotNil(t, handler, "handler %q was not registered", event)
	require.NoError(t, handler(context.Background(), &cluster.Message{
		Event: cluster.Event(event),
		Data:  wantPayload,
	}))
	require.True(t, reflect.DeepEqual(received, wantPayload), "received payload = %q, want %q", received, wantPayload)

	require.NoError(t, adapter.Broadcast(context.Background(), event, wantPayload))
	require.Len(t, transport.broadcasts, 1, "broadcasts")
	message := transport.broadcasts[0]
	require.Equal(t, cluster.Event(event), message.Event)
	require.True(t, reflect.DeepEqual(message.Data, wantPayload))
	require.Nil(t, message.Props)

	require.Error(t, adapter.RegisterHandler(event, nil), "nil child handler was accepted")
	require.Error(t, handler(context.Background(), nil), "nil cluster message was accepted")
}

func TestRealtimeClusterAdapterRegistersStableChildEventsAndPropagatesFailure(t *testing.T) {
	t.Parallel()

	registrationErr := errors.New("registration unavailable")
	transport := &recordingBorrowedCluster{registrationErr: map[cluster.Event]error{
		cluster.Event("authorization.invalidated"): registrationErr,
	}}
	adapter, err := newRealtimeClusterAdapter(transport)
	require.NoError(t, err)
	service, err := realtime.New(realtime.Dependencies{AuthenticationInvalidator: noopRealtimeInvalidator{}, Diagnostics: silentClusterLogger{}})
	require.NoError(t, err)
	err = service.SetClusterFanout(adapter)
	require.ErrorIs(t, err, registrationErr, "SetClusterFanout() error")
	want := []cluster.Event{
		cluster.Event("websocket.publish"),
		cluster.Event("exam_attempt.connection_unbound"),
		cluster.Event("authentication.session_revoked"),
		cluster.Event("authorization.invalidated"),
	}
	require.Equal(t, want, transport.registrations, "registrations")
	err = service.SetClusterFanout(adapter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already attached")
}

func TestRealtimeClusterAdapterPreservesSingleNodeLocalFirstPublication(t *testing.T) {
	t.Parallel()

	transport, err := local.New("node-a", silentClusterLogger{})
	require.NoError(t, err)
	adapter, err := newRealtimeClusterAdapter(transport)
	require.NoError(t, err)
	service, err := realtime.New(realtime.Dependencies{AuthenticationInvalidator: noopRealtimeInvalidator{}, Diagnostics: silentClusterLogger{}})
	require.NoError(t, err)
	sink := &countingRealtimeSink{}
	require.NoError(t, service.SetSink(sink))
	require.NoError(t, service.SetClusterFanout(adapter))
	require.NoError(t, transport.Start(context.Background()))
	t.Cleanup(func() {
		assert.NoError(t, transport.Stop(context.Background()))
	})

	require.NoError(t, service.Publish(context.Background(), realtime.RealtimeEvent{
		Name:   "user.notification",
		UserID: model.UserID(model.NewID()),
	}))
	require.Equal(t, 1, sink.publishCount(), "local publications")
}

type countingRealtimeSink struct {
	mu        sync.Mutex
	published int
}

func (s *countingRealtimeSink) PublishLocal(context.Context, realtime.RealtimeEvent) {
	s.mu.Lock()
	s.published++
	s.mu.Unlock()
}

func (*countingRealtimeSink) CloseSession(string, realtime.ConnectionCloseReason)   {}
func (*countingRealtimeSink) CloseUser(string, realtime.ConnectionCloseReason)      {}
func (*countingRealtimeSink) CloseAll(realtime.ConnectionCloseReason)               {}
func (*countingRealtimeSink) UnbindExamAttemptConnection(model.AttemptConnectionID) {}

func (s *countingRealtimeSink) publishCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.published
}

type silentClusterLogger struct{}

func (silentClusterLogger) ErrorContext(context.Context, string, error) {}

type noopRealtimeInvalidator struct{}

func (noopRealtimeInvalidator) InvalidateAccessCredentials(context.Context, []string)        {}
func (noopRealtimeInvalidator) InvalidateSessionActivity(context.Context, []model.SessionID) {}

var _ realtime.ClusterFanout = (*realtimeClusterAdapter)(nil)
var _ realtime.Sink = (*countingRealtimeSink)(nil)
