// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

// CacheBackend is a reusable cache store together with the lifecycle of the
// client it runs on. Ping and Close are optional: a nil Ping reports only
// context cancellation and a nil Close releases nothing. The store answers
// Add when it implements the package Counter capability.
type CacheBackend struct {
	Store ByteCache
	Ping  func(context.Context) error
	Close func() error
}

// NewCacheAdapter translates a reusable cache backend into the platform Cache
// port. Concrete backend construction belongs to the module-root composition
// package.
func NewCacheAdapter(backend CacheBackend) Cache {
	return &cacheAdapter{backend: backend}
}

type cacheAdapter struct {
	backend CacheBackend
}

func (c *cacheAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.backend.Store.Get(ctx, key)
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
	err := c.backend.Store.Set(ctx, key, value, cachepkg.SetOptions{
		TTL:       ttl,
		Condition: packageCondition,
	})
	if errors.Is(err, cachepkg.ErrNotStored) {
		return fmt.Errorf("%w: %v", ErrCacheNotStored, err)
	}
	return err
}

func (c *cacheAdapter) Delete(ctx context.Context, key string) error {
	return c.backend.Store.Delete(ctx, key)
}

func (c *cacheAdapter) Add(
	ctx context.Context,
	key string,
	delta int64,
	ttl time.Duration,
) (int64, error) {
	counter, ok := c.backend.Store.(cachepkg.Counter)
	if !ok {
		return 0, cachepkg.ErrUnsupported
	}
	return counter.Add(ctx, key, delta, cachepkg.CounterOptions{TTL: ttl})
}

func (c *cacheAdapter) Ping(ctx context.Context) error {
	if c.backend.Ping != nil {
		return c.backend.Ping(ctx)
	}
	return ctx.Err()
}

func (c *cacheAdapter) Close() error {
	if c.backend.Close != nil {
		return c.backend.Close()
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

// checkVFS runs on every readiness probe, so it checks only that the backend
// root is reachable; its cost must not grow with the number of stored files.
func checkVFS(ctx context.Context, filesystem vfspkg.FileSystem) error {
	return filesystem.CheckRoot(ctx)
}

var (
	_ Cache  = (*cacheAdapter)(nil)
	_ Mailer = (*mailAdapter)(nil)
)
