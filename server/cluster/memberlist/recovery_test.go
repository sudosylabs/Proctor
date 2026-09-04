// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/memberlist"
)

// TestMemberlistRejoinAfterChurn proves nodes recover messaging after stop and
// restart without operator repair of discovery state.
func TestMemberlistRejoinAfterChurn(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	discovery := memberlist.NewMemoryDiscovery()
	portA := freePort(t)
	portB := freePort(t)
	nodeA := mustTransport(t, "node-a", portA, discovery, key)
	nodeB := mustTransport(t, "node-b", portB, discovery, key)

	var received atomic.Int32
	if err := nodeB.RegisterHandler("test.rejoin", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := nodeA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	if err := nodeB.Start(ctx); err != nil {
		t.Fatal(err)
	}

	waitForDelivery(t, func() error {
		return nodeA.Broadcast(ctx, &cluster.Message{Event: "test.rejoin", Data: []byte("1")})
	}, &received)

	if err := nodeB.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// Restart B on a new port with the same identity and shared discovery.
	portB2 := freePort(t)
	nodeB = mustTransport(t, "node-b", portB2, discovery, key)
	if err := nodeB.RegisterHandler("test.rejoin", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	before := received.Load()
	waitForDelivery(t, func() error {
		return nodeA.Broadcast(ctx, &cluster.Message{Event: "test.rejoin", Data: []byte("2")})
	}, &received)
	if received.Load() <= before {
		t.Fatal("no deliveries observed after rejoin")
	}
}

// TestMemberlistDuplicateSendToNodeInvokesHandlerTwice proves the transport
// may deliver the same logical payload more than once. Application handlers
// (see app cluster recovery tests) must treat that as an idempotent apply.
func TestMemberlistDuplicateSendToNodeInvokesHandlerTwice(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	discovery := memberlist.NewMemoryDiscovery()
	port := freePort(t)
	node := mustTransport(t, "node-dup", port, discovery, key)

	// Model an idempotent invalidation apply: set membership of keys, not a count.
	applied := make(map[string]struct{})
	var mu sync.Mutex
	var hits atomic.Int32
	if err := node.RegisterHandler("test.duplicate", func(_ context.Context, message *cluster.Message) error {
		hits.Add(1)
		if len(message.Data) == 0 {
			return errors.New("empty payload")
		}
		key := string(message.Data)
		mu.Lock()
		applied[key] = struct{}{}
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	payload := &cluster.Message{Event: "test.duplicate", Data: []byte("session-1")}
	if err := node.SendToNode(ctx, "node-dup", payload); err != nil {
		t.Fatal(err)
	}
	if err := node.SendToNode(ctx, "node-dup", payload); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("duplicate deliveries = %d, want 2 handler invocations", hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(applied) != 1 {
		t.Fatalf("idempotent apply set = %#v, want single key", applied)
	}
}

// TestMemberlistLostBroadcastDoesNotBreakLaterDelivery stops a peer during a
// window of loss, then proves subsequent messages flow after restart. Lost
// messages are not recovered by the transport (best-effort non-guarantee).
func TestMemberlistLostBroadcastDoesNotBreakLaterDelivery(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	discovery := memberlist.NewMemoryDiscovery()
	nodeA := mustTransport(t, "node-a", freePort(t), discovery, key)
	nodeB := mustTransport(t, "node-b", freePort(t), discovery, key)

	var anyDelivery atomic.Int32
	var afterDelivery atomic.Int32
	var lostSeen atomic.Int32
	if err := nodeB.RegisterHandler("test.loss", func(_ context.Context, message *cluster.Message) error {
		anyDelivery.Add(1)
		switch string(message.Data) {
		case "after":
			afterDelivery.Add(1)
		case "lost":
			lostSeen.Add(1)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := nodeA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	if err := nodeB.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Establish mesh with a warmup payload, then stop B so broadcasts during
	// the outage are lost.
	waitForDelivery(t, func() error {
		return nodeA.Broadcast(ctx, &cluster.Message{Event: "test.loss", Data: []byte("warmup")})
	}, &anyDelivery)
	_ = nodeB.Stop(ctx)
	_ = nodeA.Broadcast(ctx, &cluster.Message{Event: "test.loss", Data: []byte("lost")})

	// Brief delay while B is down; messages sent in this window are not durable.
	time.Sleep(100 * time.Millisecond)

	nodeB = mustTransport(t, "node-b", freePort(t), discovery, key)
	if err := nodeB.RegisterHandler("test.loss", func(_ context.Context, message *cluster.Message) error {
		if string(message.Data) == "after" {
			afterDelivery.Add(1)
		}
		if string(message.Data) == "lost" {
			lostSeen.Add(1)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	// The mid-outage "lost" payload is not recovered by the transport.
	if lostSeen.Load() != 0 {
		t.Fatalf("lost payload was delivered after rejoin (%d); transport must not replay", lostSeen.Load())
	}

	// Later messages after rejoin must still work.
	waitForDelivery(t, func() error {
		return nodeA.Broadcast(ctx, &cluster.Message{Event: "test.loss", Data: []byte("after")})
	}, &afterDelivery)
}

func waitForDelivery(t *testing.T, send func() error, counter *atomic.Int32) {
	t.Helper()
	before := counter.Load()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if err := send(); err != nil {
			t.Fatal(err)
		}
		if counter.Load() > before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for cluster delivery")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
