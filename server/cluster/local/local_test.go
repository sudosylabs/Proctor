// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package local_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
)

type testLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *testLogger) ErrorContext(_ context.Context, message string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := message
	if err != nil {
		entry += ": " + err.Error()
	}
	l.entries = append(l.entries, entry)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func TestLocalTransportLifecycleAndLoopSafeDelivery(t *testing.T) {
	t.Parallel()

	logger := &testLogger{}
	transport, err := local.New("node-a", logger)
	if err != nil {
		t.Fatal(err)
	}
	message := &cluster.Message{
		Event: "test.event", Data: []byte("original"),
	}
	var handled atomic.Int32
	if err := transport.RegisterHandler(message.Event, func(
		_ context.Context,
		received *cluster.Message,
	) error {
		handled.Add(1)
		received.Data[0] = 'X'
		return errors.New("handler failure")
	}); err != nil {
		t.Fatal(err)
	}
	if err := transport.RegisterHandler(message.Event, func(context.Context, *cluster.Message) error {
		return nil
	}); !errors.Is(err, cluster.ErrHandlerExists) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	if err := transport.Broadcast(context.Background(), message); !errors.Is(err, cluster.ErrNotStarted) {
		t.Fatalf("broadcast before start error = %v", err)
	}
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := transport.Broadcast(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 0 {
		t.Fatal("peer broadcast was delivered locally and can cause a rebroadcast loop")
	}
	if err := transport.SendToNode(context.Background(), "missing", message); !errors.Is(err, cluster.ErrNodeUnavailable) {
		t.Fatalf("unknown target error = %v", err)
	}
	if err := transport.SendToNode(context.Background(), transport.NodeID(), message); err == nil {
		t.Fatal("targeted delivery hid the handler failure")
	}
	if handled.Load() != 1 {
		t.Fatalf("local targeted delivery count = %d", handled.Load())
	}
	if string(message.Data) != "original" {
		t.Fatal("handler mutated the caller's message")
	}
	if !strings.Contains(logger.String(), "cluster message handler failed") ||
		strings.Contains(logger.String(), "original") {
		t.Fatalf("handler log did not preserve safe metadata: %s", logger.String())
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := transport.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if err := transport.Ping(context.Background()); !errors.Is(err, cluster.ErrStopped) {
		t.Fatalf("Ping() after stop error = %v", err)
	}
	if err := transport.SendToNode(context.Background(), transport.NodeID(), message); !errors.Is(err, cluster.ErrStopped) {
		t.Fatalf("send after stop error = %v", err)
	}
	if err := transport.Start(context.Background()); !errors.Is(err, cluster.ErrStopped) {
		t.Fatalf("restart after terminal stop error = %v", err)
	}
}

func TestLocalTransportRecoversHandlerPanicAndHonorsContext(t *testing.T) {
	t.Parallel()

	logger := &testLogger{}
	transport, err := local.New("node-a", logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.RegisterHandler("panic", func(context.Context, *cluster.Message) error {
		panic("sensitive payload")
	}); err != nil {
		t.Fatal(err)
	}
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	message := &cluster.Message{Event: "panic"}
	if err := transport.SendToNode(context.Background(), transport.NodeID(), message); err == nil {
		t.Fatal("targeted delivery hid the recovered handler panic")
	} else if strings.Contains(err.Error(), "sensitive payload") {
		t.Fatalf("panic value leaked into returned error: %v", err)
	}
	if !strings.Contains(logger.String(), "cluster message handler failed") {
		t.Fatalf("panic was not logged: %s", logger.String())
	}
	if strings.Contains(logger.String(), "sensitive payload") {
		t.Fatalf("panic value leaked into logs: %s", logger.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transport.Broadcast(ctx, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("Broadcast(canceled) error = %v", err)
	}
}
