// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package memberlist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"

	"github.com/sudosylabs/proctor/server/cluster"
)

type fixedMetaDelegate struct{ meta []byte }

func (d fixedMetaDelegate) NodeMeta(int) []byte           { return append([]byte(nil), d.meta...) }
func (fixedMetaDelegate) NotifyMsg([]byte)                {}
func (fixedMetaDelegate) GetBroadcasts(int, int) [][]byte { return nil }
func (fixedMetaDelegate) LocalState(bool) []byte          { return nil }
func (fixedMetaDelegate) MergeRemoteState([]byte, bool)   {}

type synchronizedAdmissionLogger struct {
	mu       sync.Mutex
	messages []string
	errors   []error
}

func (l *synchronizedAdmissionLogger) ErrorContext(_ context.Context, message string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
	l.errors = append(l.errors, err)
}

func (l *synchronizedAdmissionLogger) contains(message string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, observed := range l.messages {
		if observed == message {
			return true
		}
	}
	return false
}

func admissionFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestAdmissionFailureWithdrawsBeforeRetryAndJoinFailureRemainsNonfatal(t *testing.T) {
	key := make([]byte, 32)
	peerPort := admissionFreePort(t)
	peerAddress := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", peerPort))
	peerConfig := hashimemberlist.DefaultLANConfig()
	peerConfig.Name = "node-invalid"
	peerConfig.BindAddr = "127.0.0.1"
	peerConfig.BindPort = peerPort
	peerConfig.AdvertiseAddr = "127.0.0.1"
	peerConfig.AdvertisePort = peerPort
	peerConfig.SecretKey = append([]byte(nil), key...)
	peerConfig.DelegateProtocolVersion = uint8(wireProtocolVersion)
	peerConfig.DelegateProtocolMin = uint8(supportedProtocolMin)
	peerConfig.DelegateProtocolMax = uint8(supportedProtocolMax)
	peerConfig.Delegate = fixedMetaDelegate{meta: []byte("{malformed")}
	peerConfig.Logger = log.New(io.Discard, "", 0)
	peer, err := hashimemberlist.Create(peerConfig)
	if err != nil {
		t.Fatal(err)
	}
	peerStopped := false
	t.Cleanup(func() {
		if !peerStopped {
			_ = peer.Shutdown()
		}
	})

	store := &recordingDiscoveryStore{}
	logger := &synchronizedAdmissionLogger{}
	localPort := admissionFreePort(t)
	localAddress := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", localPort))
	transport, err := New(Config{
		NodeID:             "node-local",
		BindAddress:        localAddress,
		AdvertiseAddress:   localAddress,
		EncryptionKey:      key,
		SeedAddresses:      []string{peerAddress},
		Discovery:          store,
		DiscoveryTTL:       5 * time.Second,
		DiscoveryHeartbeat: time.Second,
		ServerVersion:      "test",
		Logger:             logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	transport.discovery.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}

	err = transport.Start(context.Background())
	if !errors.Is(err, errAdmissionMetadataInvalid) {
		t.Fatalf("first Start error = %v, want invalid peer metadata", err)
	}
	if !reflect.DeepEqual(store.operations, []string{"upsert", "list", "delete"}) {
		t.Fatalf("failed admission operations = %v", store.operations)
	}
	select {
	case interval := <-scheduled:
		t.Fatalf("failed admission launched maintenance ticker at %v", interval)
	default:
	}
	if err := transport.Ping(context.Background()); err != nil {
		t.Fatalf("failed admission made transport terminal: %v", err)
	}

	if err := peer.Shutdown(); err != nil {
		t.Fatal(err)
	}
	peerStopped = true
	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("retry Start with unavailable seed: %v", err)
	}
	if interval := <-scheduled; interval != time.Second {
		t.Fatalf("maintenance interval = %v, want 1s", interval)
	}
	if !logger.contains("memberlist join incomplete") {
		t.Fatal("retry did not diagnose nonfatal Memberlist join failure")
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantOperations := []string{"upsert", "list", "delete", "upsert", "list", "delete"}
	if !reflect.DeepEqual(store.operations, wantOperations) {
		t.Fatalf("retry lifecycle operations = %v, want %v", store.operations, wantOperations)
	}
	if ticker.stops != 1 {
		t.Fatalf("retry maintenance ticker stops = %d, want 1", ticker.stops)
	}
}

func TestLateIncompatiblePeerIsRejectedByContinuousAdmission(t *testing.T) {
	key := make([]byte, 32)
	localPort := admissionFreePort(t)
	localAddress := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", localPort))
	transport, err := New(Config{
		NodeID:             "node-local",
		BindAddress:        localAddress,
		AdvertiseAddress:   localAddress,
		EncryptionKey:      key,
		Discovery:          NewMemoryDiscovery(),
		DiscoveryTTL:       5 * time.Second,
		DiscoveryHeartbeat: time.Second,
		ServerVersion:      "test",
		Logger:             &synchronizedAdmissionLogger{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Stop(context.Background()) })

	peerPort := admissionFreePort(t)
	peerConfig := hashimemberlist.DefaultLANConfig()
	peerConfig.Name = "node-incompatible"
	peerConfig.BindAddr = "127.0.0.1"
	peerConfig.BindPort = peerPort
	peerConfig.AdvertiseAddr = "127.0.0.1"
	peerConfig.AdvertisePort = peerPort
	peerConfig.SecretKey = append([]byte(nil), key...)
	peerConfig.DelegateProtocolVersion = uint8(wireProtocolVersion)
	peerConfig.DelegateProtocolMin = uint8(supportedProtocolMin)
	peerConfig.DelegateProtocolMax = uint8(supportedProtocolMax)
	peerConfig.Delegate = fixedMetaDelegate{meta: encodedAdmissionMeta(t, nodeMeta{
		NodeID:        "node-incompatible",
		ServerVersion: "future",
		ProtocolMin:   2,
		ProtocolMax:   2,
	})}
	peerConfig.Logger = log.New(io.Discard, "", 0)
	peer, err := hashimemberlist.Create(peerConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Shutdown() })

	// Hashicorp may report the requester's push/pull as successful even when
	// the receiving node's Alive delegate refuses to admit that peer. The
	// authoritative assertion is the protected node's live membership.
	_, _ = peer.Join([]string{localAddress})
	deadline := time.Now().Add(500 * time.Millisecond)
	for transport.list.NumMembers() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := transport.list.NumMembers(); got != 1 {
		t.Fatalf("local membership after rejection = %d, want 1", got)
	}
}

func TestAbruptStableIDRestartReclaimsDeadAddress(t *testing.T) {
	key := make([]byte, 32)
	discovery := NewMemoryDiscovery()
	logger := &synchronizedAdmissionLogger{}
	newTransport := func(nodeID string, port int) *Transport {
		address := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
		transport, err := New(Config{
			NodeID:             nodeID,
			BindAddress:        address,
			AdvertiseAddress:   address,
			EncryptionKey:      key,
			Discovery:          discovery,
			DiscoveryTTL:       3 * time.Second,
			DiscoveryHeartbeat: 500 * time.Millisecond,
			ServerVersion:      "test",
			Logger:             logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		return transport
	}

	nodeA := newTransport("node-a", admissionFreePort(t))
	oldNodeB := newTransport("node-b", admissionFreePort(t))
	if err := nodeA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	if err := oldNodeB.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldNodeCrashed := false
	t.Cleanup(func() {
		if !oldNodeCrashed {
			_ = oldNodeB.Stop(context.Background())
		}
	})

	// Simulate process loss: stop owned maintenance and close Memberlist without
	// sending Leave or withdrawing the discovery lease.
	oldNodeB.mu.RLock()
	cancelOld := oldNodeB.cancel
	oldDone := oldNodeB.done
	oldList := oldNodeB.list
	oldNodeB.mu.RUnlock()
	cancelOld()
	<-oldDone
	if err := oldList.Shutdown(); err != nil {
		t.Fatal(err)
	}
	oldNodeCrashed = true

	replacement := newTransport("node-b", admissionFreePort(t))
	var received atomic.Int32
	if err := replacement.RegisterHandler("test.crash-reclaim", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Stop(context.Background()) })

	deadline := time.Now().Add(20 * time.Second)
	for received.Load() == 0 && time.Now().Before(deadline) {
		if err := nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.crash-reclaim"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if received.Load() == 0 {
		t.Fatal("replacement did not reclaim the stable node ID after abrupt shutdown")
	}
}
