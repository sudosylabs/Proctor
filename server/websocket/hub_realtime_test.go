// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

func TestEventFromRealtimePreservesWireFieldsAndClonesData(t *testing.T) {
	t.Parallel()

	data := json.RawMessage(`{"status":"ready"}`)
	source := realtime.RealtimeEvent{
		ID:     model.NewId(),
		Name:   "academic_unit.updated",
		UserID: model.NewId(),
		Action: model.ActionAcademicUnitView,
		Resource: model.Resource{
			Type: model.ResourceAcademicUnit,
			ID:   model.NewId(),
		},
		Data: data,
	}

	event := eventFromRealtime(source)
	if event.Id != source.ID || event.Event != source.Name ||
		event.UserID != source.UserID || event.Action != source.Action {
		t.Fatalf("wire event fields = %#v, source = %#v", event, source)
	}
	if event.Resource != resourceFromModel(source.Resource) {
		t.Fatalf("wire resource = %#v, want %#v", event.Resource, source.Resource)
	}
	if !bytes.Equal(event.Data, source.Data) {
		t.Fatalf("wire data = %s, want %s", event.Data, source.Data)
	}

	event.Data[0] = '['
	if bytes.Equal(event.Data, source.Data) {
		t.Fatal("wire event data aliases the realtime event data")
	}
}

func TestHubUnbindExamAttemptConnectionClearsOnlyExactBindingAndKeepsSocketOpen(t *testing.T) {
	t.Parallel()

	hub := newInternalTestHub(t)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hub.Close() }()
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID()}
	exactSocket, otherSocket := newRuntimeTestSocket(), newRuntimeTestSocket()
	exact, _ := hub.register(exactSocket, principal, model.RequestMetadata{}, "", 0, "")
	other, _ := hub.register(otherSocket, principal, model.RequestMetadata{}, "", 0, "")
	if exact == nil || other == nil {
		t.Fatal("Hub did not register test connections")
	}
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
	if exactBinding != nil || exactSubscribed || otherBinding == nil || otherBinding.connectionID != otherID || !otherSubscribed {
		t.Fatalf("exact binding=%#v subscribed=%v other=%#v subscribed=%v", exactBinding, exactSubscribed, otherBinding, otherSubscribed)
	}
	select {
	case <-exactSocket.closed:
		t.Fatal("exact generic WebSocket was closed")
	default:
	}
	select {
	case <-otherSocket.closed:
		t.Fatal("unrelated generic WebSocket was closed")
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
			if code != test.wantCode || reason.fallback != test.wantReason {
				t.Fatalf("close = (%d, %q), want (%d, %q)", code, reason, test.wantCode, test.wantReason)
			}
		})
	}
}
