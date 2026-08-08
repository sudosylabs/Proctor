// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

func TestClusterDiscoveryNodeValidateAndLiveness(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().UnixMilli()
	node := &store.ClusterDiscoveryNode{
		NodeID:           "node-a",
		AdvertiseAddress: "127.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now + 30_000,
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !node.IsLive(now) {
		t.Fatal("expected live node")
	}
	if node.IsLive(node.ExpiresAt) {
		t.Fatal("exclusive expiry treated as live")
	}
	if err := (&store.ClusterDiscoveryNode{}).Validate(); err == nil {
		t.Fatal("expected empty node validation error")
	}
}
