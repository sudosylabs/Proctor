// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package memberlist_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/memberlist"
)

type testLogger struct{}

func (testLogger) ErrorContext(context.Context, string, error) {}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func mustTransport(
	t *testing.T,
	nodeID string,
	port int,
	discovery cluster.DiscoveryStore,
	key []byte,
) *memberlist.Transport {
	t.Helper()
	return mustTransportWithKeyring(t, nodeID, port, discovery, key, nil)
}

func mustTransportWithKeyring(
	t *testing.T,
	nodeID string,
	port int,
	discovery cluster.DiscoveryStore,
	key []byte,
	decryptionKeys [][]byte,
) *memberlist.Transport {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	transport, err := memberlist.New(memberlist.Config{
		NodeID:             nodeID,
		BindAddress:        address,
		AdvertiseAddress:   address,
		EncryptionKey:      append([]byte(nil), key...),
		DecryptionKeys:     decryptionKeys,
		Discovery:          discovery,
		DiscoveryTTL:       5 * time.Second,
		DiscoveryHeartbeat: time.Second,
		ServerVersion:      "test",
		Logger:             testLogger{},
	})
	require.NoError(t, err)
	return transport
}

func TestMemberlistTwoNodeBestEffortFanout(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	discovery := memberlist.NewMemoryDiscovery()
	portA := freePort(t)
	portB := freePort(t)
	nodeA := mustTransport(t, "node-a", portA, discovery, key)
	nodeB := mustTransport(t, "node-b", portB, discovery, key)

	var localLoops atomic.Int32
	var received atomic.Int32
	require.NoError(t, nodeA.RegisterHandler("test.event", func(context.Context, *cluster.Message) error {
		localLoops.Add(1)
		return nil
	}))
	require.NoError(t, nodeB.RegisterHandler("test.event", func(_ context.Context, message *cluster.Message) error {
		if string(message.Data) == "hello" {
			received.Add(1)
		}
		return nil
	}))

	ctx := context.Background()
	require.NoError(t, nodeA.Start(ctx))
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	require.NoError(t, nodeB.Start(ctx))
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	deadline := time.Now().Add(8 * time.Second)
	for {
		require.NoError(t, nodeA.Broadcast(ctx, &cluster.Message{Event: "test.event", Data: []byte("hello")}))
		if received.Load() > 0 {
			break
		}
		require.False(t, time.Now().After(deadline), "peer did not receive best-effort broadcast")
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, int32(0), localLoops.Load(), "broadcast delivered locally")

	var selfHits atomic.Int32
	require.NoError(t, nodeA.RegisterHandler("test.self", func(context.Context, *cluster.Message) error {
		selfHits.Add(1)
		return nil
	}))
	require.NoError(t, nodeA.SendToNode(ctx, "node-a", &cluster.Message{Event: "test.self", Data: []byte("x")}))
	require.Equal(t, int32(1), selfHits.Load(), "self targeted deliveries")
}

func TestMemberlistDeliversRealtimeSizedMessagesBeyondOneUDPPacket(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	discovery := memberlist.NewMemoryDiscovery()
	nodeA := mustTransport(t, "node-a", freePort(t), discovery, key)
	nodeB := mustTransport(t, "node-b", freePort(t), discovery, key)
	// Random bytes defeat Memberlist's payload compression.
	data := make([]byte, 256<<10)
	_, err := rand.Read(data)
	require.NoError(t, err)
	var broadcasts, targeted atomic.Int32
	require.NoError(t, nodeB.RegisterHandler("test.large", func(_ context.Context, message *cluster.Message) error {
		if bytes.Equal(message.Data, data) {
			if message.Props["route"] == "targeted" {
				targeted.Add(1)
			} else {
				broadcasts.Add(1)
			}
		}
		return nil
	}))
	ctx := context.Background()
	require.NoError(t, nodeA.Start(ctx))
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	require.NoError(t, nodeB.Start(ctx))
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	deadline := time.Now().Add(8 * time.Second)
	for broadcasts.Load() == 0 || targeted.Load() == 0 {
		require.False(t, time.Now().After(deadline), "large deliveries broadcast")
		require.NoError(t, nodeA.Broadcast(ctx, &cluster.Message{Event: "test.large", Data: data}))
		err := nodeA.SendToNode(ctx, "node-b", &cluster.Message{Event: "test.large", Data: data, Props: map[string]string{"route": "targeted"}})
		if err != nil {
			require.ErrorIs(t, err, cluster.ErrNodeUnavailable)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMemberlistRejectsPublicBindByDefault(t *testing.T) {
	t.Parallel()

	_, err := memberlist.New(memberlist.Config{
		NodeID:             "node-a",
		BindAddress:        "0.0.0.0:7946",
		AdvertiseAddress:   "10.0.0.1:7946",
		EncryptionKey:      make([]byte, 32),
		Discovery:          memberlist.NewMemoryDiscovery(),
		DiscoveryTTL:       5 * time.Second,
		DiscoveryHeartbeat: time.Second,
		ServerVersion:      "test",
		Logger:             testLogger{},
	})
	require.Error(t, err, "expected public bind rejection")
}

func TestMemberlistConstructionIsInertUntilStart(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	transport := mustTransport(t, "node-inert", port, memberlist.NewMemoryDiscovery(), make([]byte, 32))
	require.ErrorIs(t, transport.Broadcast(context.Background(), &cluster.Message{Event: "test.event"}), cluster.ErrNotStarted)
	require.NoError(t, transport.Stop(context.Background()))
}
