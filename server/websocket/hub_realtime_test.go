// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

type websocketMetricsFake struct {
	broadcastEvent      string
	broadcastOutcome    string
	broadcastRecipients int
	replays             []string
	subscriptions       int
}

func (*websocketMetricsFake) ConnectionOpened(string)                             {}
func (*websocketMetricsFake) ConnectionClosed(string)                             {}
func (*websocketMetricsFake) Backpressure()                                       {}
func (*websocketMetricsFake) ObserveWebSocketMessage(string, string, string, int) {}
func (r *websocketMetricsFake) ObserveWebSocketBroadcast(event, outcome string, recipients int) {
	r.broadcastEvent, r.broadcastOutcome, r.broadcastRecipients = event, outcome, recipients
}
func (r *websocketMetricsFake) ObserveWebSocketReplay(outcome string, _ int) {
	r.replays = append(r.replays, outcome)
}
func (r *websocketMetricsFake) AddWebSocketSubscriptions(delta int) { r.subscriptions += delta }

func TestEventFromRealtimePreservesWireFieldsAndClonesData(t *testing.T) {
	t.Parallel()

	data := json.RawMessage(`{"status":"ready"}`)
	source := realtime.RealtimeEvent{
		ID:     model.NewID(),
		Name:   "academic_unit.updated",
		UserID: model.NewID(),
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewID(),
		},
		Data: data,
	}

	event := eventFromRealtime(source)
	require.Equal(t, source.ID, event.ID, "wire event fields = %#v, source = %#v", event, source)
	require.Equal(t, source.Name, event.Event, "wire event fields = %#v, source = %#v", event, source)
	require.Equal(t, source.UserID, event.UserID, "wire event fields = %#v, source = %#v", event, source)
	require.Equal(t, source.Action, event.Action, "wire event fields = %#v, source = %#v", event, source)
	require.Equal(t, resourceFromModel(source.Resource), event.Resource, "wire resource")
	require.True(t, bytes.Equal(event.Data, source.Data), "wire data = %s, want %s", event.Data, source.Data)

	event.Data[0] = '['
	require.False(t, bytes.Equal(event.Data, source.Data), "wire event data aliases the realtime event data")
}

func TestHubMetricsObserveBroadcastFanoutAndResumeOutcome(t *testing.T) {
	t.Parallel()

	hub := newInternalTestHub(t)
	recorder := &websocketMetricsFake{}
	require.NoError(t, hub.AttachRecorder(recorder))
	require.NoError(t, hub.Start(context.Background()))
	defer func() { _ = hub.Close() }()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	connection, _ := hub.register(newRuntimeTestSocket(), principal, model.RequestMetadata{}, "missing-resume", 0, "")
	require.NotNil(t, connection, "register returned nil")
	event := realtime.RealtimeEvent{ID: model.NewID(), Name: "academic_unit.updated", UserID: principal.UserID.String(), Data: json.RawMessage(`{"updated":true}`)}
	hub.PublishLocal(context.Background(), event)
	require.Equal(t, "event", recorder.broadcastEvent, "broadcast metrics")
	require.Equal(t, "published", recorder.broadcastOutcome, "broadcast metrics")
	require.Equal(t, 1, recorder.broadcastRecipients, "broadcast metrics")
	require.Len(t, recorder.replays, 1)
	require.Equal(t, "resync_required", recorder.replays[0])
}

func TestHubUnbindExamAttemptConnectionClearsOnlyExactBindingAndKeepsSocketOpen(t *testing.T) {
	t.Parallel()

	hub := newInternalTestHub(t)
	require.NoError(t, hub.Start(context.Background()))
	defer func() { _ = hub.Close() }()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	exactSocket, otherSocket := newRuntimeTestSocket(), newRuntimeTestSocket()
	exact, _ := hub.register(exactSocket, principal, model.RequestMetadata{}, "", 0, "")
	other, _ := hub.register(otherSocket, principal, model.RequestMetadata{}, "", 0, "")
	require.NotNil(t, exact, "Hub did not register test connections")
	require.NotNil(t, other, "Hub did not register test connections")
	exactID, otherID := model.NewAttemptConnectionID(), model.NewAttemptConnectionID()
	exactSubscription := bindRuntimeForUnbindTest(exact, exactID)
	bindRuntimeForUnbindTest(other, otherID)

	hub.UnbindExamAttemptConnection(exactID)

	exact.mu.Lock()
	exactBinding := exact.attempt
	_, exactSubscribed := exact.subscriptions[exactSubscription.Key()]
	exact.mu.Unlock()
	other.mu.Lock()
	otherBinding := other.attempt
	otherSubscribed := len(other.subscriptions) == 1
	other.mu.Unlock()
	require.Nil(t, exactBinding, "exact binding")
	require.False(t, exactSubscribed, "exact binding")
	require.NotNil(t, otherBinding, "exact binding")
	require.Equal(t, otherID, otherBinding.connectionID, "exact binding")
	require.True(t, otherSubscribed, "exact binding")
	select {
	case <-exactSocket.closed:
		require.FailNow(t, "exact generic WebSocket was closed")
	default:
	}
	select {
	case <-otherSocket.closed:
		require.FailNow(t, "unrelated generic WebSocket was closed")
	default:
	}
}

func bindRuntimeForUnbindTest(runtime *connectionRuntime, connectionID model.AttemptConnectionID) Subscription {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	sittingID := model.NewExamSittingID()
	runtime.attempt = &examAttemptBinding{attemptID: model.NewExamAttemptID(), sittingID: sittingID,
		connectionID: connectionID, participationID: model.NewAttemptParticipationID(), generation: 1}
	subscription := Subscription{Action: model.ActionExamSittingParticipate,
		Resource: Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}}
	runtime.subscriptions[subscription.Key()] = subscription
	return subscription
}

func TestCloseCodeForRealtimeReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		reason     realtime.ConnectionCloseReason
		wantCode   int
		wantReason string
	}{
		{
			name:       "session revoked",
			reason:     realtime.ConnectionCloseSessionRevoked,
			wantCode:   CloseSessionRevoked,
			wantReason: "session no longer valid",
		},
		{
			name:       "authorization changed",
			reason:     realtime.ConnectionCloseAuthorizationChanged,
			wantCode:   CloseAuthorizationChanged,
			wantReason: "authorization changed",
		},
		{
			name:       "unspecified",
			wantCode:   CloseServer,
			wantReason: "connection closed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, reason := closeCodeForReason(test.reason)
			require.Equal(t, test.wantCode, code, "close = (%d, %q), want (%d, %q)", code, reason, test.wantCode, test.wantReason)
			require.Equal(t, test.wantReason, reason.fallback, "close = (%d, %q), want (%d, %q)", code, reason, test.wantCode, test.wantReason)
		})
	}
}
