//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

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
		reliableEvent   model.ClusterEvent = "test.reliable"
		bestEffortEvent model.ClusterEvent = "test.best_effort"
		retryEvent      model.ClusterEvent = "test.retry"
	)
	reliableReceived := make(chan string, 1)
	if err := nodeA.RegisterMessageHandler(
		reliableEvent,
		func(context.Context, *model.ClusterMessage) error {
			t.Error("peer-only broadcast was delivered back to its source")
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := nodeB.RegisterMessageHandler(
		reliableEvent,
		func(_ context.Context, message *model.ClusterMessage) error {
			reliableReceived <- string(message.Data)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Broadcast(ctx, &model.ClusterMessage{
		Event: reliableEvent, SendType: model.ClusterSendReliable, Data: []byte("reliable"),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-reliableReceived:
		if received != "reliable" {
			t.Fatalf("reliable payload = %q", received)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reliable peer broadcast was not delivered")
	}

	bestEffortReceived := make(chan struct{}, 1)
	if err := nodeB.RegisterMessageHandler(
		bestEffortEvent,
		func(context.Context, *model.ClusterMessage) error {
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
		if err := nodeA.Broadcast(ctx, &model.ClusterMessage{
			Event: bestEffortEvent, SendType: model.ClusterSendBestEffort,
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

	var attempts atomic.Int32
	retrySucceeded := make(chan struct{}, 1)
	if err := nodeB.RegisterMessageHandler(
		retryEvent,
		func(context.Context, *model.ClusterMessage) error {
			if attempts.Add(1) == 1 {
				return errors.New("transient handler failure")
			}
			select {
			case retrySucceeded <- struct{}{}:
			default:
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.Broadcast(ctx, &model.ClusterMessage{
		Event: retryEvent, SendType: model.ClusterSendReliable,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-retrySucceeded:
		if attempts.Load() < 2 {
			t.Fatalf("reliable handler attempts = %d", attempts.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending reliable message was not retried")
	}

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
	if err := nodeA.Broadcast(ctx, &model.ClusterMessage{
		Event: reliableEvent, SendType: model.ClusterSendReliable,
	}); err != nil {
		t.Fatalf("broadcast to remaining live nodes: %v", err)
	}
}
