// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

func TestClusterDiscoveryStore(t *testing.T, ss store.Store) {
	t.Run("UpsertListAndExpire", func(t *testing.T) {
		testClusterDiscoveryUpsertListAndExpire(t, ss)
	})
	t.Run("DeleteRemovesNode", func(t *testing.T) {
		testClusterDiscoveryDeleteRemovesNode(t, ss)
	})
	t.Run("RejectsInvalidRecords", func(t *testing.T) {
		testClusterDiscoveryRejectsInvalidRecords(t, ss)
	})
}

func testClusterDiscoveryUpsertListAndExpire(t *testing.T, ss store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	ttl := int64(30_000)

	alpha := &store.ClusterDiscoveryNode{
		NodeID:           "node-alpha",
		AdvertiseAddress: "10.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now + ttl,
	}
	beta := &store.ClusterDiscoveryNode{
		NodeID:           "node-beta",
		AdvertiseAddress: "10.0.0.2:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      2,
		UpdatedAt:        now,
		ExpiresAt:        now + ttl,
	}
	stale := &store.ClusterDiscoveryNode{
		NodeID:           "node-stale",
		AdvertiseAddress: "10.0.0.3:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now - 60_000,
		ExpiresAt:        now - 1_000,
	}
	require.NoError(t, ss.ClusterDiscovery().Upsert(ctx, alpha))
	require.NoError(t, ss.ClusterDiscovery().Upsert(ctx, beta))
	require.NoError(t, ss.ClusterDiscovery().Upsert(ctx, stale))

	live, err := ss.ClusterDiscovery().ListLive(ctx, now)
	require.NoError(t, err)
	require.Len(t, live, 2, "ListLive()")
	require.Equal(t, "node-alpha", live[0].NodeID)
	require.Equal(t, "node-beta", live[1].NodeID)
	require.True(t, live[0].IsLive(now))
	require.Equal(t, alpha.AdvertiseAddress, live[0].AdvertiseAddress)

	// Heartbeat replaces the lease window for the same node.
	refreshed := *alpha
	refreshed.UpdatedAt = now + 5_000
	refreshed.ExpiresAt = refreshed.UpdatedAt + ttl
	refreshed.AdvertiseAddress = "10.0.0.1:17946"
	require.NoError(t, ss.ClusterDiscovery().Upsert(ctx, &refreshed))
	live, err = ss.ClusterDiscovery().ListLive(ctx, now+5_000)
	require.NoError(t, err)
	found := false
	for _, node := range live {
		if node.NodeID == "node-alpha" {
			found = true
			require.Equal(t, "10.0.0.1:17946", node.AdvertiseAddress)
		}
	}
	require.True(t, found, "refreshed node missing from live list")

	deleted, err := ss.ClusterDiscovery().DeleteExpired(ctx, now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, int64(1), "DeleteExpired()")
	live, err = ss.ClusterDiscovery().ListLive(ctx, now)
	require.NoError(t, err)
	for _, node := range live {
		require.NotEqual(t, "node-stale", node.NodeID, "stale node remained after DeleteExpired")
	}
}

func testClusterDiscoveryDeleteRemovesNode(t *testing.T, ss store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	node := &store.ClusterDiscoveryNode{
		NodeID:           "node-delete-me",
		AdvertiseAddress: "10.0.0.9:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now + 30_000,
	}
	require.NoError(t, ss.ClusterDiscovery().Upsert(ctx, node))
	require.NoError(t, ss.ClusterDiscovery().Delete(ctx, node.NodeID))
	live, err := ss.ClusterDiscovery().ListLive(ctx, now)
	require.NoError(t, err)
	for _, candidate := range live {
		require.NotEqual(t, node.NodeID, candidate.NodeID, "deleted node still listed as live")
	}
}

func testClusterDiscoveryRejectsInvalidRecords(t *testing.T, ss store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	err := ss.ClusterDiscovery().Upsert(ctx, &store.ClusterDiscoveryNode{
		NodeID:           "bad id",
		AdvertiseAddress: "10.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now + 1_000,
	})
	require.Error(t, err, "Upsert(invalid node_id) succeeded")
	_, err = ss.ClusterDiscovery().ListLive(ctx, 0)
	require.Error(t, err, "ListLive(0) succeeded")
	_, err = ss.ClusterDiscovery().DeleteExpired(ctx, 0)
	require.Error(t, err, "DeleteExpired(0) succeeded")
}
