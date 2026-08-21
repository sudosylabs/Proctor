// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/memberlist"
)

type testLogger struct{}

func (testLogger) ErrorContext(context.Context, string, error) {}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if err := nodeA.RegisterHandler("test.event", func(context.Context, *cluster.Message) error {
		localLoops.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.RegisterHandler("test.event", func(_ context.Context, message *cluster.Message) error {
		if string(message.Data) == "hello" {
			received.Add(1)
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
	t.Cleanup(func() { _ = nodeB.Stop(context.Background()) })

	deadline := time.Now().Add(8 * time.Second)
	for {
		if err := nodeA.Broadcast(ctx, &cluster.Message{Event: "test.event", Data: []byte("hello")}); err != nil {
			t.Fatal(err)
		}
		if received.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peer did not receive best-effort broadcast")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if localLoops.Load() != 0 {
		t.Fatalf("broadcast delivered locally %d times", localLoops.Load())
	}

	var selfHits atomic.Int32
	if err := nodeA.RegisterHandler("test.self", func(context.Context, *cluster.Message) error {
		selfHits.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.SendToNode(ctx, "node-a", &cluster.Message{Event: "test.self", Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if selfHits.Load() != 1 {
		t.Fatalf("self targeted deliveries = %d", selfHits.Load())
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
	if err == nil {
		t.Fatal("expected public bind rejection")
	}
}

func TestMemberlistConstructionIsInertUntilStart(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	transport := mustTransport(t, "node-inert", port, memberlist.NewMemoryDiscovery(), make([]byte, 32))
	if err := transport.Broadcast(context.Background(), &cluster.Message{Event: "test.event"}); !errors.Is(err, cluster.ErrNotStarted) {
		t.Fatalf("Broadcast before Start error = %v", err)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
