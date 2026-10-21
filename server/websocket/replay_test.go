// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"encoding/json"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestConnectionRuntimeFinalSnapshotIsDeeplyImmutable(t *testing.T) {
	t.Parallel()

	subscription := validInboundSubscription()
	runtime := &connectionRuntime{
		id: model.NewID(),
		principal: model.Principal{
			UserID:           model.NewUserID(),
			SessionID:        model.NewSessionID(),
			CredentialID:     model.PrincipalCredentialID(model.NewID()),
			CredentialScopes: []string{"class.view"},
		},
		nextSequence: 12,
		history: []*Event{{
			ID:       model.NewID(),
			Event:    "class.updated",
			Sequence: 12,
			UserID:   model.NewID(),
			Data:     json.RawMessage(`{"revision":2}`),
		}},
		subscriptions: map[string]Subscription{subscription.Key(): subscription},
		replayable:    true,
	}

	snapshot := runtime.finalSnapshot()
	runtime.principal.CredentialScopes[0] = "changed"
	runtime.history[0].Data[0] = '['
	delete(runtime.subscriptions, subscription.Key())

	require.Equal(t, runtime.id, snapshot.id)
	require.Equal(t, runtime.principal.UserID, snapshot.principal.UserID)
	require.Equal(t, runtime.principal.SessionID, snapshot.principal.SessionID)
	require.Equal(t, int64(12), snapshot.nextSequence)
	require.True(t, snapshot.replayable)
	require.Equal(t, "class.view", snapshot.principal.CredentialScopes[0], "snapshot credential scope")
	require.Equal(t, `{"revision":2}`, string(snapshot.history[0].Data), "snapshot event data")
	_, exists := snapshot.subscriptions[subscription.Key()]
	require.True(t, exists, "runtime mutation removed the final snapshot subscription")

	snapshot.principal.CredentialScopes[0] = "snapshot-change"
	snapshot.history[0].Data[0] = '{'
	delete(snapshot.subscriptions, subscription.Key())
	require.Equal(t, "changed", runtime.principal.CredentialScopes[0], "snapshot mutation changed stopped runtime state")
	require.Equal(t, byte('['), runtime.history[0].Data[0], "snapshot mutation changed stopped runtime state")
}

func TestConnectionRuntimeClonesTheAcceptedPrincipal(t *testing.T) {
	t.Parallel()

	principal := model.Principal{
		UserID:           model.NewUserID(),
		SessionID:        model.NewSessionID(),
		CredentialScopes: []string{"class.view"},
	}
	runtime := newConnectionRuntime(
		&inboundTestApplication{},
		replayTestLogger{},
		nil,
		"",
		"node-a",
		newInboundTestSocket(),
		principal,
		model.RequestMetadata{},
		model.NewID(),
		0,
		nil,
		map[string]Subscription{},
		nil,
	)

	principal.CredentialScopes[0] = "changed-by-caller"
	require.Equal(t, "class.view", runtime.principal.CredentialScopes[0], "connection principal scope")
}

func TestConnectionRuntimeFinalSnapshotWaitsForSelectedPublication(t *testing.T) {
	t.Parallel()

	runtime := newOutboundTestRuntime(newOutboundTestSocket(), 1)
	require.True(t, runtime.acquire(), "active runtime rejected Hub publication")
	snapshotReady := make(chan connectionSnapshot, 1)
	go func() {
		snapshotReady <- runtime.finalSnapshot()
	}()
	finalizationDeadline := time.After(time.Second)
	for {
		runtime.activityMu.Lock()
		finalized := runtime.finalized
		runtime.activityMu.Unlock()
		if finalized {
			break
		}
		select {
		case <-finalizationDeadline:
			require.FailNow(t, "final snapshot did not begin finalization")
		default:
			goruntime.Gosched()
		}
	}

	event := &Event{ID: model.NewID(), Event: "class.updated", UserID: model.NewID()}
	runtime.enqueueEvent(event)
	select {
	case <-snapshotReady:
		require.FailNow(t, "final snapshot completed before selected publication")
	default:
	}
	runtime.release()

	var snapshot connectionSnapshot
	select {
	case snapshot = <-snapshotReady:
	case <-time.After(time.Second):
		require.FailNow(t, "final snapshot did not complete after selected publication")
	}
	require.Len(t, snapshot.history, 1, "final snapshot history")
	require.Equal(t, event.ID, snapshot.history[0].ID, "final snapshot history")
	require.Equal(t, int64(1), snapshot.history[0].Sequence, "final snapshot history")
	if runtime.acquire() {
		runtime.release()
		require.FailNow(t, "finalized runtime accepted a later Hub publication")
	}
}

func TestHubRetainsOnlyTheRuntimeFinalSnapshot(t *testing.T) {
	t.Parallel()

	hub := newInternalTestHub(t)
	require.NoError(t, hub.Start(t.Context()))
	t.Cleanup(func() { _ = hub.Close() })
	principal := model.Principal{
		UserID:    model.NewUserID(),
		SessionID: model.NewSessionID(),
	}
	subscription := validInboundSubscription()
	connectionID := model.NewID()
	runtime := &connectionRuntime{
		principal:     principal,
		id:            connectionID,
		nextSequence:  7,
		history:       []*Event{{ID: model.NewID(), Event: "class.updated", Sequence: 7, Data: json.RawMessage(`{"ok":true}`)}},
		subscriptions: map[string]Subscription{subscription.Key(): subscription},
		replayable:    true,
	}
	shard := hub.shardForUser(principal.UserID.String())
	shard.mu.Lock()
	shard.conns[runtime.id] = runtime
	shard.mu.Unlock()

	hub.unregister(runtime)

	// Mutating the stopped runtime after unregister must not alter retained replay.
	runtime.id = model.NewID()
	runtime.history[0].Data[0] = '['
	delete(runtime.subscriptions, subscription.Key())
	shard.mu.RLock()
	retained := shard.replay[connectionID]
	shard.mu.RUnlock()
	require.NotNil(t, retained, "Hub did not retain the stopped runtime snapshot")
	require.Equal(t, int64(7), retained.nextSequence)
	require.Equal(t, `{"ok":true}`, string(retained.history[0].Data))
	_, exists := retained.subscriptions[subscription.Key()]
	require.True(t, exists, "snapshot mutation changed retained subscriptions")
	require.False(t, retained.expiresAt.Before(time.Now()))
}

func newInternalTestHub(t *testing.T) *Hub {
	t.Helper()
	hub, err := NewHub(
		&inboundTestApplication{},
		replayTestLogger{},
		"https://proctor.example",
		"node-a",
		nil,
		15*time.Second,
	)
	require.NoError(t, err)
	return hub
}

type replayTestLogger struct{}

func (replayTestLogger) WarnContext(context.Context, string, error) {}
