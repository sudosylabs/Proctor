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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/memory"
	"github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/local"
	"github.com/sudosylabs/proctor/server/config"
)

func TestMemoryCacheAdapterPortableSemantics(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.BytesCodec())
	require.NoError(t, err)
	cache := NewCacheAdapter(CacheBackend{Store: store})
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
	mailer := NewDisabledMailer(mail.Address{
		Name:    settings.FromName,
		Address: settings.FromAddress,
	})
	require.False(t, mailer.Enabled(), "disabled mailer reported itself enabled")
	require.Equal(t, settings.FromAddress, mailer.From().Address)
	_, err := mailer.Send(context.Background(), mail.Message{})
	require.ErrorIs(t, err, ErrMailDisabled)
	require.NoError(t, mailer.Test(context.Background()))
}

func TestLocalVFSAdapterIsUsableAndHealthy(t *testing.T) {
	t.Parallel()

	settings := config.Default().VFS
	settings.Backend = "local"
	settings.Local.Root = t.TempDir()
	filesystem, err := local.New(settings.Local.Root)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, checkVFS(ctx, filesystem))
	_, err = filesystem.Write(
		ctx,
		"health/probe.txt",
		bytes.NewBufferString("ok"),
		vfs.WriteOptions{},
	)
	require.NoError(t, err)
	info, err := filesystem.Stat(ctx, "health/probe.txt")
	require.NoError(t, err)
	require.Equal(t, int64(2), info.Size)
}

// Readiness probes VFS every second; a probe that lists can walk a large local
// tree past its deadline and empty the load-balancer pool.
func TestVFSHealthChecksTheRootWithoutListing(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "vfs")
	filesystem, err := local.New(root)
	require.NoError(t, err)
	probe := &listForbiddenVFS{FileSystem: filesystem}
	require.NoError(t, checkVFS(t.Context(), probe))

	require.NoError(t, os.Remove(root))
	require.ErrorIs(t, checkVFS(t.Context(), probe), vfs.ErrNotFound)
}

type listForbiddenVFS struct{ vfs.FileSystem }

func (*listForbiddenVFS) List(context.Context, vfs.ListOptions) (vfs.Page, error) {
	panic("the VFS health check listed files")
}
