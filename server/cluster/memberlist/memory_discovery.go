// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/cluster"
)

// MemoryDiscovery is an in-process DiscoveryStore for tests.
type MemoryDiscovery struct {
	mu    sync.Mutex
	nodes map[string]cluster.DiscoveryNode
}

// NewMemoryDiscovery constructs an empty discovery store.
func NewMemoryDiscovery() *MemoryDiscovery {
	return &MemoryDiscovery{nodes: make(map[string]cluster.DiscoveryNode)}
}

// Upsert stores or replaces a discovery advertisement.
func (d *MemoryDiscovery) Upsert(_ context.Context, node cluster.DiscoveryNode) error {
	if err := node.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nodes[node.NodeID] = node
	return nil
}

// ListLive returns non-expired advertisements ordered by node ID.
func (d *MemoryDiscovery) ListLive(_ context.Context, now time.Time) ([]cluster.DiscoveryNode, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]cluster.DiscoveryNode, 0, len(d.nodes))
	for _, node := range d.nodes {
		if node.IsLive(now) {
			result = append(result, node)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].NodeID < result[j].NodeID
	})
	return result, nil
}

// Delete removes one advertisement.
func (d *MemoryDiscovery) Delete(_ context.Context, nodeID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.nodes, nodeID)
	return nil
}

// DeleteExpired removes stale advertisements.
func (d *MemoryDiscovery) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var removed int64
	for id, node := range d.nodes {
		if !node.IsLive(now) {
			delete(d.nodes, id)
			removed++
		}
	}
	return removed, nil
}

var _ cluster.DiscoveryStore = (*MemoryDiscovery)(nil)
