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
	if err := nodeA.RegisterHandler("test.rotation", func(context.Context, *cluster.Message) error {
		receivedByA.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.RegisterHandler("test.rotation", func(context.Context, *cluster.Message) error {
		receivedByB.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	if err := nodeB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	deadline := time.Now().Add(8 * time.Second)
	for (receivedByA.Load() == 0 || receivedByB.Load() == 0) && time.Now().Before(deadline) {
		if err := nodeB.Broadcast(context.Background(), &cluster.Message{Event: "test.rotation"}); err != nil {
			t.Fatal(err)
		}
		if err := nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.rotation"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if receivedByA.Load() == 0 || receivedByB.Load() == 0 {
		t.Fatalf(
			"rotated keyring deliveries old->new=%d new->old=%d",
			receivedByB.Load(),
			receivedByA.Load(),
		)
	}
}

func TestMemberlistPeriodicallyDiscoversAndJoinsPeersAfterStartup(t *testing.T) {
	t.Parallel()

	discovery := &gatedDiscovery{store: memberlist.NewMemoryDiscovery()}
	key := bytes.Repeat([]byte{4}, 32)
	nodeA := mustTransport(t, "node-late-a", freePort(t), discovery, key)
	nodeB := mustTransport(t, "node-late-b", freePort(t), discovery, key)

	var received atomic.Int32
	if err := nodeB.RegisterHandler("test.rediscovery", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	if err := nodeB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	// Neither startup could see a peer. Exposing the already-written leases
	// proves convergence comes from periodic rediscovery rather than startup.
	discovery.expose.Store(true)
	deadline := time.Now().Add(8 * time.Second)
	for received.Load() == 0 && time.Now().Before(deadline) {
		if err := nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.rediscovery"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if received.Load() == 0 {
		t.Fatal("isolated startup nodes did not converge through periodic discovery")
	}
}
