// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package testlib

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
	"github.com/sudosylabs/proctor/packages/mail"
	memorymail "github.com/sudosylabs/proctor/packages/mail/memory"
	"github.com/sudosylabs/proctor/server/platform"
)

type Cache struct {
	store  *memorycache.Store[[]byte]
	closed atomic.Bool
}

func newCache() (*Cache, error) {
	store, err := memorycache.New(cache.BytesCodec())
	if err != nil {
		return nil, err
	}
	return &Cache{store: store}, nil
}

func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.store.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
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
	var packageCondition cache.Condition
	switch condition {
	case platform.CacheSetAlways:
		packageCondition = cache.SetAlways
	case platform.CacheSetIfAbsent:
		packageCondition = cache.SetIfAbsent
	case platform.CacheSetIfPresent:
		packageCondition = cache.SetIfPresent
	default:
		return errors.New("unknown cache condition")
	}
	err := c.store.Set(ctx, key, value, cache.SetOptions{
		TTL:       ttl,
		Condition: packageCondition,
	})
	if errors.Is(err, cache.ErrNotStored) {
		return platform.ErrCacheNotStored
	}
	return err
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.store.Delete(ctx, key)
}

func (c *Cache) Add(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	return c.store.Add(ctx, key, delta, cache.CounterOptions{TTL: ttl})
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
	from   mail.Address
	closed atomic.Bool
}

func newMailer() (*Mailer, error) {
	sender, err := memorymail.New(mail.ComposerConfig{
		MessageIDDomain: "test.proctor.invalid",
	})
	if err != nil {
		return nil, err
	}
	return &Mailer{
		sender: sender,
		from: mail.Address{
			Name:    "Proctor Test",
			Address: "no-reply@test.proctor.invalid",
		},
	}, nil
}

func (m *Mailer) Enabled() bool {
	return true
}

func (m *Mailer) From() mail.Address {
	return m.from
}

func (m *Mailer) Send(ctx context.Context, message mail.Message) (mail.Receipt, error) {
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

func (m *Mailer) Deliveries() []mail.Delivery {
	return m.sender.Deliveries()
}

func (m *Mailer) Closed() bool {
	return m.closed.Load()
}

var (
	_ platform.Cache  = (*Cache)(nil)
	_ platform.Mailer = (*Mailer)(nil)
)
