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
	requireNoError(t, ss.ClusterDiscovery().Upsert(ctx, alpha))
	requireNoError(t, ss.ClusterDiscovery().Upsert(ctx, beta))
	requireNoError(t, ss.ClusterDiscovery().Upsert(ctx, stale))

	live, err := ss.ClusterDiscovery().ListLive(ctx, now)
	requireNoError(t, err)
	if len(live) != 2 {
		t.Fatalf("ListLive() = %#v, want two live nodes", live)
	}
	if live[0].NodeID != "node-alpha" || live[1].NodeID != "node-beta" {
		t.Fatalf("ListLive() order = %#v", live)
	}
	if !live[0].IsLive(now) || live[0].AdvertiseAddress != alpha.AdvertiseAddress {
		t.Fatalf("alpha record = %#v", live[0])
	}

	// Heartbeat replaces the lease window for the same node.
	refreshed := *alpha
	refreshed.UpdatedAt = now + 5_000
	refreshed.ExpiresAt = refreshed.UpdatedAt + ttl
	refreshed.AdvertiseAddress = "10.0.0.1:17946"
	requireNoError(t, ss.ClusterDiscovery().Upsert(ctx, &refreshed))
	live, err = ss.ClusterDiscovery().ListLive(ctx, now+5_000)
	requireNoError(t, err)
	found := false
	for _, node := range live {
		if node.NodeID == "node-alpha" {
			found = true
			if node.AdvertiseAddress != "10.0.0.1:17946" {
				t.Fatalf("refreshed advertise address = %#v", node)
			}
		}
	}
	if !found {
		t.Fatal("refreshed node missing from live list")
	}

	deleted, err := ss.ClusterDiscovery().DeleteExpired(ctx, now)
	requireNoError(t, err)
	if deleted < 1 {
		t.Fatalf("DeleteExpired() = %d, want at least the stale node", deleted)
	}
	live, err = ss.ClusterDiscovery().ListLive(ctx, now)
	requireNoError(t, err)
	for _, node := range live {
		if node.NodeID == "node-stale" {
			t.Fatal("stale node remained after DeleteExpired")
		}
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
	requireNoError(t, ss.ClusterDiscovery().Upsert(ctx, node))
	requireNoError(t, ss.ClusterDiscovery().Delete(ctx, node.NodeID))
	live, err := ss.ClusterDiscovery().ListLive(ctx, now)
	requireNoError(t, err)
	for _, candidate := range live {
		if candidate.NodeID == node.NodeID {
			t.Fatal("deleted node still listed as live")
		}
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
	if err == nil {
		t.Fatal("Upsert(invalid node_id) succeeded")
	}
	if _, err := ss.ClusterDiscovery().ListLive(ctx, 0); err == nil {
		t.Fatal("ListLive(0) succeeded")
	}
	if _, err := ss.ClusterDiscovery().DeleteExpired(ctx, 0); err == nil {
		t.Fatal("DeleteExpired(0) succeeded")
	}
}
