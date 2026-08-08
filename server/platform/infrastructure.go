// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
)

var (
	ErrCacheMiss      = errors.New("platform cache: key not found")
	ErrCacheNotStored = errors.New("platform cache: conditional write not applied")
	ErrMailDisabled   = errors.New("platform mail: delivery is disabled")
)

type CacheCondition uint8

const (
	CacheSetAlways CacheCondition = iota
	CacheSetIfAbsent
	CacheSetIfPresent
)

// Cache is the server-owned disposable byte-cache port. PostgreSQL remains
// authoritative for every value placed behind this interface.
type Cache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration, CacheCondition) error
	Delete(context.Context, string) error
	Add(context.Context, string, int64, time.Duration) (int64, error)
	Ping(context.Context) error
	Close() error
}

// Mailer owns the configured sender identity in addition to the transport.
type Mailer interface {
	Enabled() bool
	From() mailpkg.Address
	Send(context.Context, mailpkg.Message) (mailpkg.Receipt, error)
	Test(context.Context) error
	Close() error
}

// ByteCache is the package-cache contract required by NewCacheAdapter.
type ByteCache interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, cachepkg.SetOptions) error
	Delete(context.Context, string) error
}

// CachePinger optionally reports cache backend health.
type CachePinger interface {
	Ping(context.Context) error
}

// CacheCloser optionally releases cache backend resources.
type CacheCloser interface {
	Close() error
}

// NewCacheAdapter wraps a reusable cache store as the platform Cache port.
// Concrete backend construction belongs to the module-root composition package.
func NewCacheAdapter(store ByteCache) Cache {
	return &cacheAdapter{store: store}
}

type cacheAdapter struct {
	store ByteCache
}

func (c *cacheAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.store.Get(ctx, key)
	if errors.Is(err, cachepkg.ErrNotFound) {
		return nil, fmt.Errorf("%w: %v", ErrCacheMiss, err)
	}
	return value, err
}

func (c *cacheAdapter) Set(
	ctx context.Context,
	key string,
	value []byte,
	ttl time.Duration,
	condition CacheCondition,
) error {
	var packageCondition cachepkg.Condition
	switch condition {
	case CacheSetAlways:
		packageCondition = cachepkg.SetAlways
	case CacheSetIfAbsent:
		packageCondition = cachepkg.SetIfAbsent
	case CacheSetIfPresent:
		packageCondition = cachepkg.SetIfPresent
	default:
		return fmt.Errorf("platform cache: unknown set condition %d", condition)
	}
	err := c.store.Set(ctx, key, value, cachepkg.SetOptions{
		TTL:       ttl,
		Condition: packageCondition,
	})
	if errors.Is(err, cachepkg.ErrNotStored) {
		return fmt.Errorf("%w: %v", ErrCacheNotStored, err)
	}
	return err
}

func (c *cacheAdapter) Delete(ctx context.Context, key string) error {
	return c.store.Delete(ctx, key)
}

func (c *cacheAdapter) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	counter, ok := c.store.(cachepkg.Counter)
	if !ok {
		return 0, cachepkg.ErrUnsupported
	}
	return counter.Add(ctx, key, delta, cachepkg.CounterOptions{TTL: ttl})
}

func (c *cacheAdapter) Ping(ctx context.Context) error {
	if pinger, ok := c.store.(CachePinger); ok {
		return pinger.Ping(ctx)
	}
	return ctx.Err()
}

func (c *cacheAdapter) Close() error {
	if closer, ok := c.store.(CacheCloser); ok {
		return closer.Close()
	}
	return nil
}

// NewMailAdapter wraps a reusable mail sender as the platform Mailer port.
// Concrete backend construction belongs to the module-root composition package.
func NewMailAdapter(enabled bool, from mailpkg.Address, sender mailpkg.Sender) Mailer {
	return &mailAdapter{enabled: enabled, from: from, sender: sender}
}

// NewDisabledMailer constructs an explicitly disabled mail capability.
func NewDisabledMailer(from mailpkg.Address) Mailer {
	return &mailAdapter{from: from}
}

type mailAdapter struct {
	enabled bool
	from    mailpkg.Address
	sender  mailpkg.Sender
}

func (m *mailAdapter) Enabled() bool {
	return m.enabled
}

func (m *mailAdapter) From() mailpkg.Address {
	return m.from
}

func (m *mailAdapter) Send(ctx context.Context, message mailpkg.Message) (mailpkg.Receipt, error) {
	if !m.enabled || m.sender == nil {
		return mailpkg.Receipt{}, ErrMailDisabled
	}
	if message.From.Address == "" {
		message.From = m.from
	}
	if message.EnvelopeFrom == "" {
		message.EnvelopeFrom = m.from.Address
	}
	return m.sender.Send(ctx, message)
}

func (m *mailAdapter) Test(ctx context.Context) error {
	if !m.enabled || m.sender == nil {
		return ctx.Err()
	}
	tester, ok := m.sender.(mailpkg.Tester)
	if !ok {
		return nil
	}
	return tester.Test(ctx)
}

func (m *mailAdapter) Close() error {
	return nil
}

func checkVFS(ctx context.Context, filesystem vfspkg.FileSystem) error {
	_, err := filesystem.List(ctx, vfspkg.ListOptions{Limit: 1})
	return err
}

var (
	_ Cache  = (*cacheAdapter)(nil)
	_ Mailer = (*mailAdapter)(nil)
)
