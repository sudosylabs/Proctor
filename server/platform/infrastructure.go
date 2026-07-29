// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/redis/rueidis"

	cachepkg "github.com/sudosylabs/proctor/packages/cache"
	memorycache "github.com/sudosylabs/proctor/packages/cache/memory"
	rediscache "github.com/sudosylabs/proctor/packages/cache/redis"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	smtpmail "github.com/sudosylabs/proctor/packages/mail/smtp"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	localvfs "github.com/sudosylabs/proctor/packages/vfs/local"
	s3vfs "github.com/sudosylabs/proctor/packages/vfs/s3"
	"github.com/sudosylabs/proctor/server/config"
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

type cacheAdapter struct {
	store  cachepkg.Store[[]byte]
	client rueidis.Client
}

func newCache(settings config.Cache) (*cacheAdapter, error) {
	codec := cachepkg.BytesCodec()
	switch settings.Backend {
	case "memory":
		store, err := memorycache.New(codec)
		if err != nil {
			return nil, err
		}
		return &cacheAdapter{store: store}, nil
	case "redis":
		clientOption := rueidis.ClientOption{
			InitAddress: append([]string(nil), settings.Redis.Addresses...),
			Username:    settings.Redis.Username,
			Password:    settings.Redis.Password,
			SelectDB:    settings.Redis.Database,
			ClientName:  "proctor",
			Dialer: net.Dialer{
				Timeout:   settings.Redis.ConnectTimeout.Duration,
				KeepAlive: 30 * time.Second,
			},
		}
		if settings.Redis.TLS {
			clientOption.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		client, err := rueidis.NewClient(clientOption)
		if err != nil {
			return nil, fmt.Errorf("create Redis client: %w", err)
		}
		store, err := rediscache.New(client, codec, rediscache.Config{Namespace: settings.Namespace})
		if err != nil {
			client.Close()
			return nil, err
		}
		return &cacheAdapter{store: store, client: client}, nil
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", settings.Backend)
	}
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
	if c.client == nil {
		return ctx.Err()
	}
	return c.client.Do(ctx, c.client.B().Ping().Build()).Error()
}

func (c *cacheAdapter) Close() error {
	if c.client != nil {
		c.client.Close()
	}
	return nil
}

type mailAdapter struct {
	enabled bool
	from    mailpkg.Address
	sender  mailpkg.Sender
}

func newMailer(settings config.Mail) (*mailAdapter, error) {
	adapter := &mailAdapter{
		enabled: settings.Enabled,
		from: mailpkg.Address{
			Name:    settings.FromName,
			Address: settings.FromAddress,
		},
	}
	if !settings.Enabled {
		return adapter, nil
	}

	sender, err := smtpmail.New(smtpmail.Config{
		Address:         settings.SMTP.Address,
		ServerName:      settings.SMTP.ServerName,
		LocalName:       settings.SMTP.LocalName,
		Security:        smtpmail.Security(settings.SMTP.Security),
		Username:        settings.SMTP.Username,
		Password:        settings.SMTP.Password,
		Authentication:  smtpmail.Authentication(settings.SMTP.Authentication),
		Timeout:         settings.SMTP.Timeout.Duration,
		MessageIDDomain: settings.SMTP.MessageIDDomain,
		MaxMessageBytes: settings.SMTP.MaxMessageBytes,
		MaxRecipients:   settings.SMTP.MaxRecipients,
	})
	if err != nil {
		return nil, err
	}
	adapter.sender = sender
	return adapter, nil
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

func newVFS(settings config.VFS) (vfspkg.FileSystem, error) {
	switch settings.Backend {
	case "local":
		return localvfs.New(settings.Local.Root)
	case "s3":
		return s3vfs.New(s3vfs.Config{
			Endpoint:     settings.S3.Endpoint,
			AccessKey:    settings.S3.AccessKey,
			SecretKey:    settings.S3.SecretKey,
			SessionToken: settings.S3.SessionToken,
			Bucket:       settings.S3.Bucket,
			Prefix:       settings.S3.Prefix,
			Region:       settings.S3.Region,
			Secure:       settings.S3.Secure,
		})
	default:
		return nil, fmt.Errorf("unsupported VFS backend %q", settings.Backend)
	}
}

func checkVFS(ctx context.Context, filesystem vfspkg.FileSystem) error {
	_, err := filesystem.List(ctx, vfspkg.ListOptions{Limit: 1})
	return err
}

var (
	_ Cache  = (*cacheAdapter)(nil)
	_ Mailer = (*mailAdapter)(nil)
)
