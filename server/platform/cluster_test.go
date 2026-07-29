// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

func TestLocalClusterLifecycleAndLoopSafeDelivery(t *testing.T) {
	t.Parallel()

	logger, logs := newClusterTestLogger(t)
	cluster, err := newLocalCluster("node-a", logger)
	if err != nil {
		t.Fatal(err)
	}
	message := &model.ClusterMessage{
		Event: "test.event", SendType: model.ClusterSendReliable, Data: []byte("original"),
	}
	var handled atomic.Int32
	if err := cluster.RegisterMessageHandler(message.Event, func(
		_ context.Context,
		received *model.ClusterMessage,
	) error {
		handled.Add(1)
		received.Data[0] = 'X'
		return errors.New("handler failure")
	}); err != nil {
		t.Fatal(err)
	}
	if err := cluster.RegisterMessageHandler(message.Event, func(context.Context, *model.ClusterMessage) error {
		return nil
	}); !errors.Is(err, ErrClusterHandlerExists) {
		t.Fatalf("duplicate registration error = %v", err)
	}
	if err := cluster.Broadcast(context.Background(), message); !errors.Is(err, ErrClusterNotStarted) {
		t.Fatalf("broadcast before start error = %v", err)
	}
	if err := cluster.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cluster.Broadcast(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 0 {
		t.Fatal("peer broadcast was delivered locally and can cause a rebroadcast loop")
	}
	if err := cluster.SendToNode(context.Background(), "missing", message); !errors.Is(err, ErrClusterNodeUnavailable) {
		t.Fatalf("unknown target error = %v", err)
	}
	if err := cluster.SendToNode(context.Background(), cluster.NodeID(), message); err == nil {
		t.Fatal("targeted delivery hid the handler failure")
	}
	if handled.Load() != 1 {
		t.Fatalf("local targeted delivery count = %d", handled.Load())
	}
	if string(message.Data) != "original" {
		t.Fatal("handler mutated the caller's message")
	}
	if !strings.Contains(logs.String(), "cluster message handler failed") ||
		strings.Contains(logs.String(), "original") {
		t.Fatalf("handler log did not preserve safe metadata: %s", logs.String())
	}
	if err := cluster.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := cluster.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if err := cluster.Ping(context.Background()); !errors.Is(err, ErrClusterStopped) {
		t.Fatalf("Ping() after stop error = %v", err)
	}
	if err := cluster.SendToNode(context.Background(), cluster.NodeID(), message); !errors.Is(err, ErrClusterStopped) {
		t.Fatalf("send after stop error = %v", err)
	}
	if err := cluster.Start(context.Background()); !errors.Is(err, ErrClusterStopped) {
		t.Fatalf("restart after terminal stop error = %v", err)
	}
}

func TestLocalClusterRecoversHandlerPanicAndHonorsContext(t *testing.T) {
	t.Parallel()

	logger, logs := newClusterTestLogger(t)
	cluster, err := newLocalCluster("node-a", logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := cluster.RegisterMessageHandler("panic", func(context.Context, *model.ClusterMessage) error {
		panic("sensitive payload")
	}); err != nil {
		t.Fatal(err)
	}
	if err := cluster.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	message := &model.ClusterMessage{Event: "panic", SendType: model.ClusterSendBestEffort}
	if err := cluster.SendToNode(context.Background(), cluster.NodeID(), message); err == nil {
		t.Fatal("targeted delivery hid the recovered handler panic")
	} else if strings.Contains(err.Error(), "sensitive payload") {
		t.Fatalf("panic value leaked into returned error: %v", err)
	}
	if !strings.Contains(logs.String(), "cluster message handler failed") {
		t.Fatalf("panic was not logged: %s", logs.String())
	}
	if strings.Contains(logs.String(), "sensitive payload") {
		t.Fatalf("panic value leaked into logs: %s", logs.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cluster.Broadcast(ctx, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled broadcast error = %v", err)
	}
}

func newClusterTestLogger(tb testing.TB) (*mlog.Logger, *mlog.Buffer) {
	tb.Helper()
	logs := &mlog.Buffer{}
	logger, err := mlog.New()
	if err != nil {
		tb.Fatal(err)
	}
	if err := logger.Configure(mlog.Config{
		MaxFieldBytes: 4096,
		Targets: []mlog.Target{{
			Name: "test", Type: "console", Level: "trace", Format: "json", Writer: logs,
		}},
	}); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := logger.Shutdown(); err != nil {
			tb.Error(err)
		}
	})
	return logger, logs
}
