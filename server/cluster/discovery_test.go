// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package cluster

import (
	"strings"
	"testing"
	"time"
)

func TestDiscoveryNodeValidateAndLiveness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	node := DiscoveryNode{
		NodeID:           "node-a",
		AdvertiseAddress: "10.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(DefaultDiscoveryTTL),
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !node.IsLive(now) {
		t.Fatal("live node reported expired at lease start")
	}
	if node.IsLive(node.ExpiresAt) {
		t.Fatal("exclusive expiry treated as still live")
	}

	invalid := []DiscoveryNode{
		{},
		{
			NodeID: "bad id", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 2, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now,
		},
	}
	for index, candidate := range invalid {
		if err := candidate.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error", index)
		}
	}
}

func TestDiscoveryLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	updatedAt, expiresAt, err := DiscoveryLease(now, DefaultDiscoveryTTL)
	if err != nil {
		t.Fatal(err)
	}
	if !updatedAt.Equal(now.UTC()) {
		t.Fatalf("UpdatedAt = %v, want %v", updatedAt, now.UTC())
	}
	if !expiresAt.Equal(now.UTC().Add(DefaultDiscoveryTTL)) {
		t.Fatalf("ExpiresAt = %v", expiresAt)
	}
	if _, _, err := DiscoveryLease(time.Time{}, time.Second); err == nil {
		t.Fatal("expected empty now error")
	}
	if _, _, err := DiscoveryLease(now, 0); err == nil {
		t.Fatal("expected non-positive ttl error")
	}
	if DefaultDiscoveryHeartbeat >= DefaultDiscoveryTTL {
		t.Fatalf("heartbeat %s must be less than ttl %s", DefaultDiscoveryHeartbeat, DefaultDiscoveryTTL)
	}
	if strings.TrimSpace(DefaultDiscoveryTTL.String()) == "" {
		t.Fatal("default ttl is empty")
	}
}
