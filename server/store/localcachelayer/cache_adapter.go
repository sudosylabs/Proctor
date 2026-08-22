// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package localcachelayer

import (
	"context"
	"errors"
	"fmt"
	"time"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
)

// ByteCache is the reusable cache contract required by CacheAdapter. Concrete
// backend selection remains in the module-root composition package.
type ByteCache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, cachepkg.SetOptions) error
	Delete(context.Context, string) error
}

// NewCacheAdapter translates the reusable cache contract into the store
// layer's hit/miss contract without owning the backend lifecycle.
func NewCacheAdapter(store ByteCache) (Cache, error) {
	if store == nil {
		return nil, errors.New("local store byte cache is nil")
	}
	return &cacheAdapter{store: store}, nil
}

type cacheAdapter struct {
	store ByteCache
}

func (c *cacheAdapter) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := c.store.Get(ctx, key)
	if errors.Is(err, cachepkg.ErrNotFound) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func (c *cacheAdapter) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 || ttl > maxAllowedTTL {
		return fmt.Errorf("local store cache TTL must be between 1ns and %s", maxAllowedTTL)
	}
	return c.store.Set(ctx, key, value, cachepkg.SetOptions{TTL: ttl})
}

func (c *cacheAdapter) Delete(ctx context.Context, key string) error {
	return c.store.Delete(ctx, key)
}

var _ Cache = (*cacheAdapter)(nil)
