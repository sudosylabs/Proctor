// Package redis provides a Redis cache backend using rueidis.
package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/rueidis"
	"github.com/sudosylabs/proctor/packages/cache"
)

const counterScript = `
local value = redis.call("INCRBY", KEYS[1], ARGV[1])
if ARGV[2] == "0" then
	redis.call("PERSIST", KEYS[1])
else
	redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return value
`

var addCounter = rueidis.NewLuaScript(counterScript)

// Config controls Redis key ownership.
type Config struct {
	// Namespace is prepended to every key. It is required so independent
	// applications and cache instances do not collide.
	Namespace string
}

// Store persists encoded cache values in Redis. The supplied client remains
// owned by the caller and is never closed by Store.
type Store[V any] struct {
	client rueidis.Client
	codec  cache.Codec[V]
	prefix string
}

// New constructs a Redis store over an existing rueidis client.
func New[V any](client rueidis.Client, codec cache.Codec[V], config Config) (*Store[V], error) {
	if err := cache.ValidateNamespace(config.Namespace); err != nil {
		return nil, fmt.Errorf("redis cache: %w", err)
	}
	if codec == nil {
		return nil, fmt.Errorf("redis cache: codec must not be nil")
	}
	if client == nil {
		return nil, fmt.Errorf("redis cache: client must not be nil")
	}
	return &Store[V]{
		client: client,
		codec:  codec,
		prefix: config.Namespace + ":",
	}, nil
}

func (s *Store[V]) Capabilities() cache.Capabilities {
	return cache.Capabilities{
		TTL:            true,
		ConditionalSet: true,
		AtomicCounter:  true,
		// Purging a namespace portably requires topology-aware scanning. It is
		// intentionally not part of this adapter's first contract.
		Purge: false,
	}
}

func (s *Store[V]) Get(ctx context.Context, key string) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, cache.Error("get", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return zero, cache.Error("get", key, err)
	}

	data, err := s.client.Do(ctx, s.client.B().Get().Key(s.key(key)).Build()).AsBytes()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return zero, cache.Error("get", key, cache.ErrNotFound)
		}
		return zero, cache.Error("get", key, err)
	}
	value, err := s.codec.Decode(data)
	if err != nil {
		return zero, cache.Error("get", key, err)
	}
	return value, nil
}

func (s *Store[V]) Set(ctx context.Context, key string, value V, options cache.SetOptions) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("set", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return cache.Error("set", key, err)
	}
	if err := options.Validate(); err != nil {
		return cache.Error("set", key, err)
	}
	ttl, err := cache.NormalizedTTL(options.TTL)
	if err != nil {
		return cache.Error("set", key, err)
	}
	data, err := s.codec.Encode(value)
	if err != nil {
		return cache.Error("set", key, err)
	}

	command := s.client.B().Arbitrary("SET").
		Keys(s.key(key)).
		Args(rueidis.BinaryString(data))
	if ttl > 0 {
		command = command.Args("PX", strconv.FormatInt(ttl.Milliseconds(), 10))
	}
	switch options.Condition {
	case cache.SetIfAbsent:
		command = command.Args("NX")
	case cache.SetIfPresent:
		command = command.Args("XX")
	}

	err = s.client.Do(ctx, command.Build()).Error()
	if rueidis.IsRedisNil(err) {
		return cache.Error("set", key, cache.ErrNotStored)
	}
	return cache.Error("set", key, err)
}

func (s *Store[V]) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("delete", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return cache.Error("delete", key, err)
	}
	err := s.client.Do(ctx, s.client.B().Del().Key(s.key(key)).Build()).Error()
	return cache.Error("delete", key, err)
}

// Add atomically increments a Redis integer and refreshes its expiration in
// the same server-side script.
func (s *Store[V]) Add(ctx context.Context, key string, delta int64, options cache.CounterOptions) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, cache.Error("add", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return 0, cache.Error("add", key, err)
	}
	if err := options.Validate(); err != nil {
		return 0, cache.Error("add", key, err)
	}
	ttl, err := cache.NormalizedTTL(options.TTL)
	if err != nil {
		return 0, cache.Error("add", key, err)
	}

	result, err := addCounter.Exec(
		ctx,
		s.client,
		[]string{s.key(key)},
		[]string{strconv.FormatInt(delta, 10), strconv.FormatInt(ttl.Milliseconds(), 10)},
	).AsInt64()
	if err != nil {
		if isInvalidCounterError(err) {
			err = fmt.Errorf("%w: %v", cache.ErrInvalidValue, err)
		}
		return 0, cache.Error("add", key, err)
	}
	return result, nil
}

func (s *Store[V]) key(key string) string {
	return s.prefix + key
}

func isInvalidCounterError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "integer") ||
		strings.Contains(message, "wrongtype") ||
		strings.Contains(message, "overflow")
}

var (
	_ cache.Store[any] = (*Store[any])(nil)
	_ cache.Counter    = (*Store[any])(nil)
)
