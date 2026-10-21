// Package cachetest contains a reusable backend conformance suite.
package cachetest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
)

// Factory returns an isolated string store for each test.
type Factory func(t *testing.T) cache.Store[string]

// Run exercises the portable Store contract and advertised optional features.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("missing", func(t *testing.T) {
		store := factory(t)
		_, err := store.Get(context.Background(), "missing")
		require.ErrorIs(t, err, cache.ErrNotFound, "Get() error")
	})

	t.Run("set get overwrite delete", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()
		mustSet(t, store, "key", "one", cache.SetOptions{})
		assertGet(t, store, "key", "one")
		mustSet(t, store, "key", "two", cache.SetOptions{})
		assertGet(t, store, "key", "two")
		require.NoError(t, store.Delete(ctx, "key"))
		require.NoError(t, store.Delete(ctx, "key"))
		_, err := store.Get(ctx, "key")
		require.ErrorIs(t, err, cache.ErrNotFound, "Get() after delete error")
	})

	t.Run("invalid input", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()
		require.ErrorIs(t, store.Set(ctx, "", "value", cache.SetOptions{}), cache.ErrInvalidKey)
		require.ErrorIs(t, store.Set(ctx, "key", "value", cache.SetOptions{TTL: -1}), cache.ErrInvalidTTL)
	})

	t.Run("cancelled context", func(t *testing.T) {
		store := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, store.Set(ctx, "key", "value", cache.SetOptions{}), context.Canceled, "Set() error")
	})

	t.Run("ttl", func(t *testing.T) {
		store := factory(t)
		if !store.Capabilities().TTL {
			t.Skip("backend does not advertise TTL")
		}
		mustSet(t, store, "short", "value", cache.SetOptions{TTL: 20 * time.Millisecond})
		assertGet(t, store, "short", "value")
		eventually(t, time.Second, func() bool {
			_, err := store.Get(context.Background(), "short")
			return errors.Is(err, cache.ErrNotFound)
		})
	})

	t.Run("conditional set", func(t *testing.T) {
		store := factory(t)
		if !store.Capabilities().ConditionalSet {
			t.Skip("backend does not advertise conditional writes")
		}
		mustSet(t, store, "key", "one", cache.SetOptions{Condition: cache.SetIfAbsent})
		err := store.Set(context.Background(), "key", "two", cache.SetOptions{Condition: cache.SetIfAbsent})
		require.ErrorIs(t, err, cache.ErrNotStored, "SetIfAbsent error")
		assertGet(t, store, "key", "one")
		mustSet(t, store, "key", "two", cache.SetOptions{Condition: cache.SetIfPresent})
		assertGet(t, store, "key", "two")
		err = store.Set(context.Background(), "missing", "value", cache.SetOptions{Condition: cache.SetIfPresent})
		require.ErrorIs(t, err, cache.ErrNotStored, "SetIfPresent error")
	})

	t.Run("conditional set after expiry", func(t *testing.T) {
		store := factory(t)
		caps := store.Capabilities()
		if !caps.TTL || !caps.ConditionalSet {
			t.Skip("backend lacks TTL or conditional writes")
		}
		mustSet(t, store, "key", "old", cache.SetOptions{TTL: 20 * time.Millisecond})
		eventually(t, time.Second, func() bool {
			err := store.Set(context.Background(), "key", "new", cache.SetOptions{Condition: cache.SetIfAbsent})
			return err == nil
		})
		assertGet(t, store, "key", "new")
	})

	t.Run("atomic counter", func(t *testing.T) {
		store := factory(t)
		if !store.Capabilities().AtomicCounter {
			t.Skip("backend does not advertise atomic counters")
		}
		counter, ok := store.(cache.Counter)
		require.True(t, ok, "AtomicCounter advertised but Store does not implement cache.Counter")
		value, err := counter.Add(context.Background(), "counter", 3, cache.CounterOptions{})
		require.NoError(t, err, "first Add()")
		require.Equal(t, int64(3), value, "first Add()")
		value, err = counter.Add(context.Background(), "counter", -1, cache.CounterOptions{})
		require.NoError(t, err, "second Add()")
		require.Equal(t, int64(2), value, "second Add()")

		const workers = 16
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, addErr := counter.Add(context.Background(), "parallel", 1, cache.CounterOptions{})
				errs <- addErr
			}()
		}
		wg.Wait()
		close(errs)
		for addErr := range errs {
			require.NoError(t, addErr)
		}
		value, err = counter.Add(context.Background(), "parallel", 0, cache.CounterOptions{})
		require.NoError(t, err, "parallel counter")
		require.Equal(t, int64(workers), value, "parallel counter")
	})

	t.Run("counter fixed window", func(t *testing.T) {
		store := factory(t)
		caps := store.Capabilities()
		if !caps.TTL || !caps.AtomicCounter {
			t.Skip("backend lacks TTL or atomic counters")
		}
		counter := store.(cache.Counter)
		window := cache.CounterOptions{TTL: 100 * time.Millisecond}
		value, err := counter.Add(context.Background(), "counter", 1, window)
		require.NoError(t, err, "Add()")
		require.Equal(t, int64(1), value, "Add()")
		time.Sleep(50 * time.Millisecond)
		value, err = counter.Add(context.Background(), "counter", 1, cache.CounterOptions{TTL: time.Minute})
		require.NoError(t, err, "second Add()")
		require.Equal(t, int64(2), value, "second Add()")
		value, err = counter.Add(context.Background(), "counter", 0, cache.CounterOptions{})
		require.NoError(t, err, "zero-TTL Add()")
		require.Equal(t, int64(2), value, "zero-TTL Add()")
		// 120ms after creation: past the original deadline, which neither a
		// longer TTL nor a zero TTL on later increments may extend or remove.
		time.Sleep(70 * time.Millisecond)
		value, err = counter.Add(context.Background(), "counter", 1, window)
		require.NoError(t, err, "Add() after the window")
		require.Equal(t, int64(1), value, "Add() after the window")

		// A persistent counter gains the first positive TTL applied to it.
		_, err = counter.Add(context.Background(), "persistent", 5, cache.CounterOptions{})
		require.NoError(t, err)
		value, err = counter.Add(context.Background(), "persistent", 1, cache.CounterOptions{TTL: 40 * time.Millisecond})
		require.NoError(t, err, "Add() on persistent counter")
		require.Equal(t, int64(6), value, "Add() on persistent counter")
		eventually(t, time.Second, func() bool {
			value, addErr := counter.Add(context.Background(), "persistent", 0, cache.CounterOptions{TTL: time.Second})
			return addErr == nil && value == 0
		})
	})

	t.Run("counter integer precision", func(t *testing.T) {
		tests := []struct {
			name  string
			value int64
		}{
			{name: "positive above float precision", value: 1<<53 + 1},
			{name: "negative below float precision", value: -(1<<53 + 1)},
			{name: "maximum int64", value: math.MaxInt64},
			{name: "minimum int64", value: math.MinInt64},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				store := factory(t)
				if !store.Capabilities().AtomicCounter {
					t.Skip("backend does not advertise atomic counters")
				}
				counter := store.(cache.Counter)
				value, err := counter.Add(context.Background(), "counter", test.value, cache.CounterOptions{})
				require.NoError(t, err, "Add()")
				require.Equal(t, test.value, value, "Add()")
				assertGet(t, store, "counter", strconv.FormatInt(test.value, 10))
				value, err = counter.Add(context.Background(), "counter", 0, cache.CounterOptions{})
				require.NoError(t, err, "Add(0)")
				require.Equal(t, test.value, value, "Add(0)")
			})
		}
	})

	t.Run("counter rejected update preserves value", func(t *testing.T) {
		tests := []struct {
			name    string
			initial string
			delta   int64
		}{
			{name: "positive overflow", initial: "9223372036854775807", delta: 1},
			{name: "negative overflow", initial: "-9223372036854775808", delta: -1},
			{name: "noninteger", initial: "not-an-integer", delta: 1},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				store := factory(t)
				if !store.Capabilities().AtomicCounter {
					t.Skip("backend does not advertise atomic counters")
				}
				mustSet(t, store, "counter", test.initial, cache.SetOptions{})
				counter := store.(cache.Counter)
				_, err := counter.Add(context.Background(), "counter", test.delta, cache.CounterOptions{})
				require.ErrorIs(t, err, cache.ErrInvalidValue, "Add() error")
				assertGet(t, store, "counter", test.initial)
				if test.initial == "-9223372036854775808" {
					value, err := counter.Add(context.Background(), "counter", math.MaxInt64, cache.CounterOptions{})
					require.NoError(t, err, "Add() after rejected overflow")
					require.Equal(t, int64(-1), value, "Add() after rejected overflow")
					assertGet(t, store, "counter", "-1")
				}
			})
		}
	})

	t.Run("counter rejected update preserves expiration", func(t *testing.T) {
		store := factory(t)
		caps := store.Capabilities()
		if !caps.AtomicCounter || !caps.TTL {
			t.Skip("backend lacks TTL or atomic counters")
		}
		const initial = "9223372036854775807"
		mustSet(t, store, "counter", initial, cache.SetOptions{TTL: 250 * time.Millisecond})
		counter := store.(cache.Counter)
		_, err := counter.Add(context.Background(), "counter", 1, cache.CounterOptions{})
		require.ErrorIs(t, err, cache.ErrInvalidValue, "Add() error")
		assertGet(t, store, "counter", initial)
		eventually(t, 2*time.Second, func() bool {
			_, err := store.Get(context.Background(), "counter")
			return errors.Is(err, cache.ErrNotFound)
		})
	})

	t.Run("purge", func(t *testing.T) {
		store := factory(t)
		if !store.Capabilities().Purge {
			t.Skip("backend does not advertise purge")
		}
		purger, ok := store.(cache.Purger)
		require.True(t, ok, "Purge advertised but Store does not implement cache.Purger")
		for i := range 3 {
			mustSet(t, store, "key-"+strconv.Itoa(i), fmt.Sprint(i), cache.SetOptions{})
		}
		require.NoError(t, purger.Purge(context.Background()))
		for i := range 3 {
			_, err := store.Get(context.Background(), "key-"+strconv.Itoa(i))
			require.ErrorIs(t, err, cache.ErrNotFound)
		}
	})
}

func mustSet(t *testing.T, store cache.Store[string], key, value string, options cache.SetOptions) {
	t.Helper()
	require.NoError(t, store.Set(context.Background(), key, value, options), "Set(%q) error", key)
}

func assertGet(t *testing.T, store cache.Store[string], key, want string) {
	t.Helper()
	got, err := store.Get(context.Background(), key)
	require.NoError(t, err, "Get(%q) error", key)
	require.Equal(t, want, got, "Get(%q)", key)
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.FailNow(t, "condition was not satisfied before timeout")
}
