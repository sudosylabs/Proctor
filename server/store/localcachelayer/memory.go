// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package localcachelayer

import (
	"context"
	"errors"
	"sync"
	"time"
)

const maxAllowedEntries = 100_000

type memoryEntry struct {
	value     []byte
	expiresAt time.Time
}

// MemoryCache is a bounded, process-local Cache implementation.
type MemoryCache struct {
	mu         sync.Mutex
	entries    map[string]memoryEntry
	order      []string
	maxEntries int
	now        func() time.Time
}

// NewMemoryCache constructs a bounded process-local byte cache.
func NewMemoryCache(maxEntries int) (*MemoryCache, error) {
	if maxEntries < 1 || maxEntries > maxAllowedEntries {
		return nil, errors.New("local store cache entries must be between 1 and 100000")
	}
	return &MemoryCache{
		entries:    make(map[string]memoryEntry, maxEntries),
		order:      make([]string, 0, maxEntries),
		maxEntries: maxEntries,
		now:        time.Now,
	}, nil
}

func (c *MemoryCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !c.now().Before(entry.expiresAt) {
		c.deleteLocked(key)
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (c *MemoryCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 || ttl > maxAllowedTTL {
		return errors.New("local store cache TTL must be bounded")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		for len(c.entries) >= c.maxEntries {
			c.deleteLocked(c.order[0])
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = memoryEntry{
		value:     append([]byte(nil), value...),
		expiresAt: c.now().Add(ttl),
	}
	return nil
}

func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(key)
	return nil
}

func (c *MemoryCache) deleteLocked(key string) {
	if _, exists := c.entries[key]; !exists {
		return
	}
	delete(c.entries, key)
	for index, candidate := range c.order {
		if candidate == key {
			c.order = append(c.order[:index], c.order[index+1:]...)
			return
		}
	}
}

var _ Cache = (*MemoryCache)(nil)
