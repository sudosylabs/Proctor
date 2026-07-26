// Package memory provides a concurrency-safe in-process cache backend.
package memory

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/packages/cache"
)

type entry struct {
	data      []byte
	expiresAt time.Time
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// Store is an encoded in-process cache. Encoding values before storing them
// keeps its copy/isolation behavior aligned with remote backends.
type Store[V any] struct {
	mu     sync.Mutex
	values map[string]entry
	codec  cache.Codec[V]
	now    func() time.Time
}

// New constructs an empty in-process store.
func New[V any](codec cache.Codec[V]) (*Store[V], error) {
	if codec == nil {
		return nil, fmt.Errorf("memory cache: codec must not be nil")
	}
	return &Store[V]{
		values: make(map[string]entry),
		codec:  codec,
		now:    time.Now,
	}, nil
}

func (s *Store[V]) Capabilities() cache.Capabilities {
	return cache.Capabilities{
		TTL:            true,
		ConditionalSet: true,
		AtomicCounter:  true,
		Purge:          true,
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

	s.mu.Lock()
	item, found := s.values[key]
	if found && item.expired(s.now()) {
		delete(s.values, key)
		found = false
	}
	data := append([]byte(nil), item.data...)
	s.mu.Unlock()

	if !found {
		return zero, cache.Error("get", key, cache.ErrNotFound)
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
	data = append([]byte(nil), data...)

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	current, found := s.values[key]
	if found && current.expired(now) {
		delete(s.values, key)
		found = false
	}
	if options.Condition == cache.SetIfAbsent && found {
		return cache.Error("set", key, cache.ErrNotStored)
	}
	if options.Condition == cache.SetIfPresent && !found {
		return cache.Error("set", key, cache.ErrNotStored)
	}

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}
	s.values[key] = entry{data: data, expiresAt: expiresAt}
	return nil
}

func (s *Store[V]) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("delete", key, err)
	}
	if err := cache.ValidateKey(key); err != nil {
		return cache.Error("delete", key, err)
	}

	s.mu.Lock()
	delete(s.values, key)
	s.mu.Unlock()
	return nil
}

// Add atomically changes an integer stored as base-10 text. Each successful
// call replaces the key's expiration according to options.
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

	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()

	item, found := s.values[key]
	if found && item.expired(now) {
		delete(s.values, key)
		found = false
	}

	var current int64
	if found {
		current, err = strconv.ParseInt(string(item.data), 10, 64)
		if err != nil {
			return 0, cache.Error("add", key, fmt.Errorf("%w: counter is not an integer", cache.ErrInvalidValue))
		}
	}
	if (delta > 0 && current > math.MaxInt64-delta) ||
		(delta < 0 && current < math.MinInt64-delta) {
		return 0, cache.Error("add", key, fmt.Errorf("%w: integer overflow", cache.ErrInvalidValue))
	}
	current += delta

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = now.Add(ttl)
	}
	s.values[key] = entry{
		data:      []byte(strconv.FormatInt(current, 10)),
		expiresAt: expiresAt,
	}
	return current, nil
}

// Purge atomically removes every item from this store instance.
func (s *Store[V]) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return cache.Error("purge", "", err)
	}
	s.mu.Lock()
	s.values = make(map[string]entry)
	s.mu.Unlock()
	return nil
}

var (
	_ cache.Store[any] = (*Store[any])(nil)
	_ cache.Counter    = (*Store[any])(nil)
	_ cache.Purger     = (*Store[any])(nil)
)
