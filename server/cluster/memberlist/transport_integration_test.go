// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

//go:build integration

package memberlist_test

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/memberlist"
)

type gatedDiscovery struct {
	store  *memberlist.MemoryDiscovery
	expose atomic.Bool
}

func (d *gatedDiscovery) Upsert(ctx context.Context, node cluster.DiscoveryNode) error {
	return d.store.Upsert(ctx, node)
}

func (d *gatedDiscovery) ListLive(ctx context.Context, now time.Time) ([]cluster.DiscoveryNode, error) {
	if !d.expose.Load() {
		return nil, nil
	}
	return d.store.ListLive(ctx, now)
}

func (d *gatedDiscovery) Delete(ctx context.Context, nodeID string) error {
	return d.store.Delete(ctx, nodeID)
}

func (d *gatedDiscovery) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	return d.store.DeleteExpired(ctx, now)
}

func TestMemberlistKeyringSupportsRollingPrimaryRotation(t *testing.T) {
	t.Parallel()

	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	discovery := memberlist.NewMemoryDiscovery()
	nodeA := mustTransportWithKeyring(
		t, "node-old-primary", freePort(t), discovery, oldKey, [][]byte{newKey},
	)
	nodeB := mustTransportWithKeyring(
		t, "node-new-primary", freePort(t), discovery, newKey, [][]byte{oldKey},
	)

	var receivedByA atomic.Int32
	var receivedByB atomic.Int32
	require.NoError(t, nodeA.RegisterHandler("test.rotation", func(context.Context, *cluster.Message) error {
		receivedByA.Add(1)
		return nil
	}))
	require.NoError(t, nodeB.RegisterHandler("test.rotation", func(context.Context, *cluster.Message) error {
		receivedByB.Add(1)
		return nil
	}))
	require.NoError(t, nodeA.Start(context.Background()))
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	require.NoError(t, nodeB.Start(context.Background()))
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	deadline := time.Now().Add(8 * time.Second)
	for (receivedByA.Load() == 0 || receivedByB.Load() == 0) && time.Now().Before(deadline) {
		require.NoError(t, nodeB.Broadcast(context.Background(), &cluster.Message{Event: "test.rotation"}))
		require.NoError(t, nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.rotation"}))
		time.Sleep(50 * time.Millisecond)
	}
	require.NotEqual(t, int32(0), receivedByA.Load(), "rotated keyring deliveries old->new")
	require.NotEqual(t, int32(0), receivedByB.Load(), "rotated keyring deliveries old->new")
}

func TestMemberlistPeriodicallyDiscoversAndJoinsPeersAfterStartup(t *testing.T) {
	t.Parallel()

	discovery := &gatedDiscovery{store: memberlist.NewMemoryDiscovery()}
	key := bytes.Repeat([]byte{4}, 32)
	nodeA := mustTransport(t, "node-late-a", freePort(t), discovery, key)
	nodeB := mustTransport(t, "node-late-b", freePort(t), discovery, key)

	var received atomic.Int32
	require.NoError(t, nodeB.RegisterHandler("test.rediscovery", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}))
	require.NoError(t, nodeA.Start(context.Background()))
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	require.NoError(t, nodeB.Start(context.Background()))
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	// Neither startup could see a peer. Exposing the already-written leases
	// proves convergence comes from periodic rediscovery rather than startup.
	discovery.expose.Store(true)
	deadline := time.Now().Add(8 * time.Second)
	for received.Load() == 0 && time.Now().Before(deadline) {
		require.NoError(t, nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.rediscovery"}))
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEqual(t, int32(0), received.Load(), "isolated startup nodes did not converge through periodic discovery")
}
