// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

//go:build integration

package memberlist

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	ticker := &manualDiscoveryTicker{ticks: make(chan time.Time)}
	scheduled := make(chan time.Duration, 1)
	transport.discovery.cfg.newTicker = func(interval time.Duration) discoveryTicker {
		scheduled <- interval
		return ticker
	}

	err = transport.Start(context.Background())
	require.ErrorIs(t, err, errAdmissionMetadataInvalid, "first Start error")
	require.Equal(t, []string{"upsert", "list", "delete"}, store.operations)
	select {
	case interval := <-scheduled:
		require.FailNow(t, fmt.Sprintf("failed admission launched maintenance ticker at %v", interval))
	default:
	}
	require.NoError(t, transport.Ping(context.Background()))

	require.NoError(t, peer.Shutdown())
	peerStopped = true
	require.NoError(t, transport.Start(context.Background()))
	interval := <-scheduled
	require.Equal(t, time.Second, interval, "maintenance interval")
	require.True(t, logger.contains("memberlist join incomplete"), "retry did not diagnose nonfatal Memberlist join failure")
	require.NoError(t, transport.Stop(context.Background()))
	wantOperations := []string{"upsert", "list", "delete", "upsert", "list", "delete"}
	require.Equal(t, wantOperations, store.operations, "retry lifecycle operations")
	require.Equal(t, 1, ticker.stops, "retry maintenance ticker stops")
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
	require.NoError(t, err)
	require.NoError(t, transport.Start(context.Background()))
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
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Shutdown() })

	// Hashicorp may report the requester's push/pull as successful even when
	// the receiving node's Alive delegate refuses to admit that peer. The
	// authoritative assertion is the protected node's live membership.
	_, _ = peer.Join([]string{localAddress})
	deadline := time.Now().Add(500 * time.Millisecond)
	for transport.list.NumMembers() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, 1, transport.list.NumMembers(), "local membership after rejection")
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
		require.NoError(t, err)
		return transport
	}

	nodeA := newTransport("node-a", admissionFreePort(t))
	oldNodeB := newTransport("node-b", admissionFreePort(t))
	require.NoError(t, nodeA.Start(context.Background()))
	t.Cleanup(func() { _ = nodeA.Stop(context.Background()) })
	require.NoError(t, oldNodeB.Start(context.Background()))
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
	require.NoError(t, oldList.Shutdown())
	oldNodeCrashed = true

	replacement := newTransport("node-b", admissionFreePort(t))
	var received atomic.Int32
	require.NoError(t, replacement.RegisterHandler("test.crash-reclaim", func(context.Context, *cluster.Message) error {
		received.Add(1)
		return nil
	}))
	require.NoError(t, replacement.Start(context.Background()))
	t.Cleanup(func() { _ = replacement.Stop(context.Background()) })

	deadline := time.Now().Add(20 * time.Second)
	for received.Load() == 0 && time.Now().Before(deadline) {
		require.NoError(t, nodeA.Broadcast(context.Background(), &cluster.Message{Event: "test.crash-reclaim"}))
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEqual(t, int32(0), received.Load(), "replacement did not reclaim the stable node ID after abrupt shutdown")
}
