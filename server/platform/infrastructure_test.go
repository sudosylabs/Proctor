// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCacheAdapter(memoryByteCache{store: store})
	t.Cleanup(func() {
		if err := cache.Close(); err != nil {
			t.Error(err)
		}
	})

	ctx := context.Background()
	if err := cache.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "missing"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("missing key error = %v", err)
	}
	if err := cache.Set(ctx, "value", []byte("first"), time.Minute, CacheSetIfAbsent); err != nil {
		t.Fatal(err)
	}
	if err := cache.Set(ctx, "value", []byte("second"), time.Minute, CacheSetIfAbsent); !errors.Is(err, ErrCacheNotStored) {
		t.Fatalf("second set-if-absent error = %v", err)
	}
	value, err := cache.Get(ctx, "value")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(value, []byte("first")) {
		t.Fatalf("value = %q", value)
	}
	if got, err := cache.Add(ctx, "counter", 2, time.Minute); err != nil || got != 2 {
		t.Fatalf("first Add() = %d, %v", got, err)
	}
	if got, err := cache.Add(ctx, "counter", 3, time.Minute); err != nil || got != 5 {
		t.Fatalf("second Add() = %d, %v", got, err)
	}
	if err := cache.Delete(ctx, "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "value"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("deleted key error = %v", err)
	}
}

func TestDisabledMailerIsExplicitlyUnavailable(t *testing.T) {
	t.Parallel()

	settings := config.Default().Mail
	settings.Enabled = false
	mailer := NewDisabledMailer(mailpkg.Address{
		Name:    settings.FromName,
		Address: settings.FromAddress,
	})
	if mailer.Enabled() {
		t.Fatal("disabled mailer reported itself enabled")
	}
	if mailer.From().Address != settings.FromAddress {
		t.Fatalf("From() = %#v", mailer.From())
	}
	if _, err := mailer.Send(context.Background(), mailpkg.Message{}); !errors.Is(err, ErrMailDisabled) {
		t.Fatalf("Send() error = %v", err)
	}
	if err := mailer.Test(context.Background()); err != nil {
		t.Fatalf("disabled mailer health check = %v", err)
	}
}

func TestLocalVFSAdapterIsUsableAndHealthy(t *testing.T) {
	t.Parallel()

	settings := config.Default().VFS
	settings.Backend = "local"
	settings.Local.Root = t.TempDir()
	filesystem, err := localvfs.New(settings.Local.Root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := checkVFS(ctx, filesystem); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Write(
		ctx,
		"health/probe.txt",
		bytes.NewBufferString("ok"),
		vfspkg.WriteOptions{},
	); err != nil {
		t.Fatal(err)
	}
	info, err := filesystem.Stat(ctx, "health/probe.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 2 {
		t.Fatalf("stored object = %#v", info)
	}
}
