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
	"testing"
	"time"

	hashimemberlist "github.com/hashicorp/memberlist"
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
		ProtocolMin:        2,
		ProtocolMax:        4,
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
