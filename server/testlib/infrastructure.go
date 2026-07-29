// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package testlib

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	memorymail "github.com/sudosylabs/proctor/packages/mail/memory"
	"github.com/sudosylabs/proctor/server/platform"
)

type Cache struct {
	store  *memorycache.Store[[]byte]
	closed atomic.Bool
}

func newCache() (*Cache, error) {
	store, err := memorycache.New(cachepkg.BytesCodec())
	if err != nil {
		return nil, err
	}
	return &Cache{store: store}, nil
}

func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.store.Get(ctx, key)
	if errors.Is(err, cachepkg.ErrNotFound) {
		return nil, platform.ErrCacheMiss
	}
	return value, err
}

func (c *Cache) Set(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
	condition platform.CacheCondition,
) error {
	var packageCondition cachepkg.Condition
	switch condition {
	case platform.CacheSetAlways:
		packageCondition = cachepkg.SetAlways
	case platform.CacheSetIfAbsent:
		packageCondition = cachepkg.SetIfAbsent
	case platform.CacheSetIfPresent:
		packageCondition = cachepkg.SetIfPresent
	default:
		return errors.New("unknown cache condition")
	}
	err := c.store.Set(ctx, key, value, cachepkg.SetOptions{
		TTL:       ttl,
		Condition: packageCondition,
	})
	if errors.Is(err, cachepkg.ErrNotStored) {
		return platform.ErrCacheNotStored
	}
	return err
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.store.Delete(ctx, key)
}

func (c *Cache) Add(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	return c.store.Add(ctx, key, delta, cachepkg.CounterOptions{TTL: ttl})
}

func (c *Cache) Ping(ctx context.Context) error {
	return ctx.Err()
}

func (c *Cache) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *Cache) Closed() bool {
	return c.closed.Load()
}

type Mailer struct {
	sender *memorymail.Sender
	from   mailpkg.Address
	closed atomic.Bool
}

func newMailer() (*Mailer, error) {
	sender, err := memorymail.New(mailpkg.ComposerConfig{
		MessageIDDomain: "test.proctor.invalid",
	})
	if err != nil {
		return nil, err
	}
	return &Mailer{
		sender: sender,
		from: mailpkg.Address{
			Name:    "Proctor Test",
			Address: "no-reply@test.proctor.invalid",
		},
	}, nil
}

func (m *Mailer) Enabled() bool {
	return true
}

func (m *Mailer) From() mailpkg.Address {
	return m.from
}

func (m *Mailer) Send(ctx context.Context, message mailpkg.Message) (mailpkg.Receipt, error) {
	if message.From.Address == "" {
		message.From = m.from
	}
	if message.EnvelopeFrom == "" {
		message.EnvelopeFrom = m.from.Address
	}
	return m.sender.Send(ctx, message)
}

func (m *Mailer) Test(ctx context.Context) error {
	return ctx.Err()
}

func (m *Mailer) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *Mailer) Deliveries() []mailpkg.Delivery {
	return m.sender.Deliveries()
}

func (m *Mailer) Closed() bool {
	return m.closed.Load()
}

var (
	_ platform.Cache  = (*Cache)(nil)
	_ platform.Mailer = (*Mailer)(nil)
)
