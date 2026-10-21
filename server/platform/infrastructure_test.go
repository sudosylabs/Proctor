// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package platform

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	"github.com/sudosylabs/proctor/server/config"
)

type memoryByteCache struct {
	store *memorycache.Store[[]byte]
}

func (c memoryByteCache) Get(ctx context.Context, key string) ([]byte, error) {
	return c.store.Get(ctx, key)
}

func (c memoryByteCache) Set(ctx context.Context, key string, value []byte, options cachepkg.SetOptions) error {
	return c.store.Set(ctx, key, value, options)
}

func (c memoryByteCache) Delete(ctx context.Context, key string) error {
	return c.store.Delete(ctx, key)
}

func (c memoryByteCache) Add(ctx context.Context, key string, delta int64, options cachepkg.CounterOptions) (int64, error) {
	return c.store.Add(ctx, key, delta, options)
}

func TestMemoryCacheAdapterPortableSemantics(t *testing.T) {
	t.Parallel()

	store, err := memorycache.New(cachepkg.BytesCodec())
	require.NoError(t, err)
	cache := NewCacheAdapter(memoryByteCache{store: store})
	t.Cleanup(func() {
		assert.NoError(t, cache.Close())
	})

	ctx := context.Background()
	require.NoError(t, cache.Ping(ctx))
	_, err = cache.Get(ctx, "missing")
	require.ErrorIs(t, err, ErrCacheMiss)
	require.NoError(t, cache.Set(ctx, "value", []byte("first"), time.Minute, CacheSetIfAbsent))
	require.ErrorIs(t, cache.Set(ctx, "value", []byte("second"), time.Minute, CacheSetIfAbsent), ErrCacheNotStored)
	value, err := cache.Get(ctx, "value")
	require.NoError(t, err)
	require.True(t, bytes.Equal(value, []byte("first")), "value = %q", value)
	got, err := cache.Add(ctx, "counter", 2, time.Minute)
	require.NoError(t, err, "first Add()")
	require.Equal(t, int64(2), got, "first Add()")
	got, err = cache.Add(ctx, "counter", 3, time.Minute)
	require.NoError(t, err, "second Add()")
	require.Equal(t, int64(5), got, "second Add()")
	require.NoError(t, cache.Delete(ctx, "value"))
	_, err = cache.Get(ctx, "value")
	require.ErrorIs(t, err, ErrCacheMiss)
}

func TestDisabledMailerIsExplicitlyUnavailable(t *testing.T) {
	t.Parallel()

	settings := config.Default().Mail
	settings.Enabled = false
	mailer := NewDisabledMailer(mailpkg.Address{
		Name:    settings.FromName,
		Address: settings.FromAddress,
	})
	require.False(t, mailer.Enabled(), "disabled mailer reported itself enabled")
	require.Equal(t, settings.FromAddress, mailer.From().Address)
	_, err := mailer.Send(context.Background(), mailpkg.Message{})
	require.ErrorIs(t, err, ErrMailDisabled)
	require.NoError(t, mailer.Test(context.Background()))
}

func TestLocalVFSAdapterIsUsableAndHealthy(t *testing.T) {
	t.Parallel()

	settings := config.Default().VFS
	settings.Backend = "local"
	settings.Local.Root = t.TempDir()
	filesystem, err := localvfs.New(settings.Local.Root)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, checkVFS(ctx, filesystem))
	_, err = filesystem.Write(
		ctx,
		"health/probe.txt",
		bytes.NewBufferString("ok"),
		vfspkg.WriteOptions{},
	)
	require.NoError(t, err)
	info, err := filesystem.Stat(ctx, "health/probe.txt")
	require.NoError(t, err)
	require.Equal(t, int64(2), info.Size)
}
