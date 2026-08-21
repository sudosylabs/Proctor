// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/cluster/local"
	"github.com/sudosylabs/proctor/server/model"
)

type cacheClusterTestLogger struct{}

func (cacheClusterTestLogger) ErrorContext(context.Context, string, error) {}

func TestLocalCacheClusterAdapterRegistersValidatedLoopSafeInvalidation(t *testing.T) {
	t.Parallel()

	transport, err := local.New("node-a", cacheClusterTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := newLocalCacheClusterAdapter(transport)
	if err != nil {
		t.Fatal(err)
	}
	id := model.NewAcademicPeriodID().String()
	var handled atomic.Int64
	if err := adapter.RegisterAcademicPeriod(func(_ context.Context, got string) error {
		if got != id {
			t.Fatalf("invalidation ID = %q, want %q", got, id)
		}
		handled.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := transport.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := adapter.BroadcastAcademicPeriod(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 0 {
		t.Fatal("peer-only broadcast re-entered the local invalidation handler")
	}
	if err := transport.SendToNode(context.Background(), transport.NodeID(), &cluster.Message{
		Event: academicPeriodInvalidatedEvent,
		Data:  []byte(`{"id":"` + id + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != 1 {
		t.Fatalf("handled invalidations = %d, want 1", handled.Load())
	}
	if err := transport.SendToNode(context.Background(), transport.NodeID(), &cluster.Message{
		Event: academicPeriodInvalidatedEvent,
		Data:  []byte(`{"id":"invalid"}`),
	}); err == nil {
		t.Fatal("invalid peer invalidation was accepted")
	}
}
