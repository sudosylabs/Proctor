//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/mlog"
	"github.com/sudosylabs/proctor/server/model"
)

func TestRedisClusterTwoNodeConformance(t *testing.T) {
	address := os.Getenv("PROCTOR_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Fatal("PROCTOR_TEST_REDIS_ADDRESS is not set")
	}
	logger, err := mlog.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Shutdown() })

	namespace := "proctor_test_" + model.NewId()
	settings := func(nodeID string) config.Cluster {
		return config.Cluster{
			Backend: "redis",
			NodeID:  nodeID,
			Redis: config.ClusterRedis{
				Addresses:       []string{address},
				ConnectTimeout:  config.Duration{Duration: 2 * time.Second},
				Namespace:       namespace,
				LeaseTTL:        config.Duration{Duration: 4 * time.Second},
				Heartbeat:       config.Duration{Duration: time.Second},
				ReliableMaximum: 128,
			},
		}
	}
	nodeA, err := NewRedisCluster(settings("node-a"), logger)
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := NewRedisCluster(settings("node-b"), logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := nodeA.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		_ = nodeB.Stop(stopCtx)
		_ = nodeA.Stop(stopCtx)
	})

	const (
		fanoutEvent     cluster.Event = "test.fanout"
		bestEffortEvent cluster.Event = "test.best_effort"
	)
	fanoutReceived := make(chan string, 1)
	if err := nodeA.RegisterHandler(
		fanoutEvent,
		func(context.Context, *cluster.Message) error {
			t.Error("peer-only broadcast was delivered back to its source")
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.RegisterHandler(
		fanoutEvent,
		func(_ context.Context, message *cluster.Message) error {
			fanoutReceived <- string(message.Data)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Broadcast(ctx, &cluster.Message{
		Event: fanoutEvent, Data: []byte("fanout"),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-fanoutReceived:
		if received != "fanout" {
			t.Fatalf("fanout payload = %q", received)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fanout peer broadcast was not delivered")
	}

	bestEffortReceived := make(chan struct{}, 1)
	if err := nodeB.RegisterHandler(
		bestEffortEvent,
		func(context.Context, *cluster.Message) error {
			select {
			case bestEffortReceived <- struct{}{}:
			default:
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	bestEffortDeadline := time.Now().Add(5 * time.Second)
	for {
		if err := nodeA.Broadcast(ctx, &cluster.Message{
			Event: bestEffortEvent,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-bestEffortReceived:
			goto bestEffortDone
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(bestEffortDeadline) {
				t.Fatal("best-effort peer broadcast was not observed")
			}
		}
	}
bestEffortDone:

	// Public cluster contract is best-effort only (ADR-0026). Handler retry
	// after failure is no longer a transport promise; correctness recovers
	// from PostgreSQL and client resynchronization.

	duplicate, err := NewRedisCluster(settings("node-a"), logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := duplicate.Start(ctx); !errors.Is(err, ErrClusterNodeIDInUse) {
		t.Fatalf("duplicate node start error = %v", err)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := duplicate.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}

	if err := nodeB.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Broadcast(ctx, &cluster.Message{
		Event: fanoutEvent,
	}); err != nil {
		t.Fatalf("broadcast to remaining live nodes: %v", err)
	}
}
