// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestConnectionRuntimeFinalSnapshotIsDeeplyImmutable(t *testing.T) {
	t.Parallel()

	subscription := validInboundSubscription()
	runtime := &connectionRuntime{
		id: model.NewId(),
		principal: model.Principal{
			UserID:           model.NewUserID(),
			SessionID:        model.NewSessionID(),
			CredentialID:     model.PrincipalCredentialID(model.NewId()),
			CredentialScopes: []string{"class.view"},
		},
		nextSequence: 12,
		history: []*Event{{
			Id:       model.NewId(),
			Event:    "class.updated",
			Sequence: 12,
			UserID:   model.NewId(),
			Data:     json.RawMessage(`{"revision":2}`),
		}},
		subscriptions: map[string]Subscription{subscription.Key(): subscription},
		replayable:    true,
	}

	snapshot := runtime.finalSnapshot()
	runtime.principal.CredentialScopes[0] = "changed"
	runtime.history[0].Data[0] = '['
	delete(runtime.subscriptions, subscription.Key())

	if snapshot.id != runtime.id ||
		snapshot.principal.UserID != runtime.principal.UserID ||
		snapshot.principal.SessionID != runtime.principal.SessionID ||
		snapshot.nextSequence != 12 || !snapshot.replayable {
		t.Fatalf("final snapshot identity/state = %#v", snapshot)
	}
	if got := snapshot.principal.CredentialScopes[0]; got != "class.view" {
		t.Fatalf("snapshot credential scope = %q, want %q", got, "class.view")
	}
	if got := string(snapshot.history[0].Data); got != `{"revision":2}` {
		t.Fatalf("snapshot event data = %q, want immutable JSON", got)
	}
	if _, exists := snapshot.subscriptions[subscription.Key()]; !exists {
		t.Fatal("runtime mutation removed the final snapshot subscription")
	}

	snapshot.principal.CredentialScopes[0] = "snapshot-change"
	snapshot.history[0].Data[0] = '{'
	delete(snapshot.subscriptions, subscription.Key())
	if runtime.principal.CredentialScopes[0] != "changed" ||
		runtime.history[0].Data[0] != '[' {
		t.Fatal("snapshot mutation changed stopped runtime state")
	}
}

func TestHubRetainsOnlyTheRuntimeFinalSnapshot(t *testing.T) {
	t.Parallel()

	hub := newInternalTestHub(t)
	if err := hub.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	principal := model.Principal{
		UserID:    model.NewUserID(),
		SessionID: model.NewSessionID(),
	}
	runtime := &connectionRuntime{
		principal: principal,
		id:        model.NewId(),
	}
	shard := hub.shardForUser(principal.UserID.String())
	shard.mu.Lock()
	shard.conns[runtime.id] = runtime
	shard.mu.Unlock()

	subscription := validInboundSubscription()
	snapshot := connectionSnapshot{
		id:            runtime.id,
		principal:     clonePrincipal(principal),
		nextSequence:  7,
		history:       []*Event{{Id: model.NewId(), Event: "class.updated", Sequence: 7, Data: json.RawMessage(`{"ok":true}`)}},
		subscriptions: map[string]Subscription{subscription.Key(): subscription},
		replayable:    true,
	}
	hub.unregister(runtime, snapshot)

	// Mutating both inputs after unregister must not alter retained replay.
	runtime.id = model.NewId()
	snapshot.history[0].Data[0] = '['
	delete(snapshot.subscriptions, subscription.Key())
	shard.mu.RLock()
	retained := shard.replay[snapshot.id]
	shard.mu.RUnlock()
	if retained == nil {
		t.Fatal("Hub did not retain the stopped runtime snapshot")
	}
	if retained.nextSequence != 7 || string(retained.history[0].Data) != `{"ok":true}` {
		t.Fatalf("retained replay = %#v", retained)
	}
	if _, exists := retained.subscriptions[subscription.Key()]; !exists {
		t.Fatal("snapshot mutation changed retained subscriptions")
	}
	if retained.expiresAt.Before(time.Now()) {
		t.Fatalf("retained replay already expired at %s", retained.expiresAt)
	}
}

func newInternalTestHub(t *testing.T) *Hub {
	t.Helper()
	hub, err := NewHub(
		&inboundTestApplication{},
		replayTestLogger{},
		"https://proctor.example",
		"node-a",
	)
	if err != nil {
		t.Fatalf("NewHub() error = %v", err)
	}
	return hub
}

type replayTestLogger struct{}

func (replayTestLogger) WarnContext(context.Context, string, error) {}
