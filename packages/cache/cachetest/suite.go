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
		if !errors.Is(err, cache.ErrNotFound) {
			t.Fatalf("Get() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("set get overwrite delete", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()
		mustSet(t, store, "key", "one", cache.SetOptions{})
		assertGet(t, store, "key", "one")
		mustSet(t, store, "key", "two", cache.SetOptions{})
		assertGet(t, store, "key", "two")
		if err := store.Delete(ctx, "key"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if err := store.Delete(ctx, "key"); err != nil {
			t.Fatalf("second Delete() error = %v", err)
		}
		_, err := store.Get(ctx, "key")
		if !errors.Is(err, cache.ErrNotFound) {
			t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()
		if err := store.Set(ctx, "", "value", cache.SetOptions{}); !errors.Is(err, cache.ErrInvalidKey) {
			t.Fatalf("Set() invalid key error = %v", err)
		}
		if err := store.Set(ctx, "key", "value", cache.SetOptions{TTL: -1}); !errors.Is(err, cache.ErrInvalidTTL) {
			t.Fatalf("Set() negative TTL error = %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		store := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.Set(ctx, "key", "value", cache.SetOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Set() error = %v, want context.Canceled", err)
		}
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
		if !errors.Is(err, cache.ErrNotStored) {
			t.Fatalf("SetIfAbsent error = %v, want ErrNotStored", err)
		}
		assertGet(t, store, "key", "one")
		mustSet(t, store, "key", "two", cache.SetOptions{Condition: cache.SetIfPresent})
		assertGet(t, store, "key", "two")
		err = store.Set(context.Background(), "missing", "value", cache.SetOptions{Condition: cache.SetIfPresent})
		if !errors.Is(err, cache.ErrNotStored) {
			t.Fatalf("SetIfPresent error = %v, want ErrNotStored", err)
		}
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
		if !ok {
			t.Fatal("AtomicCounter advertised but Store does not implement cache.Counter")
		}
		value, err := counter.Add(context.Background(), "counter", 3, cache.CounterOptions{})
		if err != nil || value != 3 {
			t.Fatalf("first Add() = %d, %v; want 3, nil", value, err)
		}
		value, err = counter.Add(context.Background(), "counter", -1, cache.CounterOptions{})
		if err != nil || value != 2 {
			t.Fatalf("second Add() = %d, %v; want 2, nil", value, err)
		}

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
			if addErr != nil {
				t.Fatalf("parallel Add() error = %v", addErr)
			}
		}
		value, err = counter.Add(context.Background(), "parallel", 0, cache.CounterOptions{})
		if err != nil || value != workers {
			t.Fatalf("parallel counter = %d, %v; want %d, nil", value, err, workers)
		}
	})

	t.Run("counter ttl", func(t *testing.T) {
		store := factory(t)
		caps := store.Capabilities()
		if !caps.TTL || !caps.AtomicCounter {
			t.Skip("backend lacks TTL or atomic counters")
		}
		counter := store.(cache.Counter)
		value, err := counter.Add(context.Background(), "counter", 1, cache.CounterOptions{TTL: 20 * time.Millisecond})
		if err != nil || value != 1 {
			t.Fatalf("Add() = %d, %v; want 1, nil", value, err)
		}
		time.Sleep(10 * time.Millisecond)
		value, err = counter.Add(context.Background(), "counter", 1, cache.CounterOptions{TTL: 40 * time.Millisecond})
		if err != nil || value != 2 {
			t.Fatalf("refresh Add() = %d, %v; want 2, nil", value, err)
		}
		time.Sleep(20 * time.Millisecond)
		value, err = counter.Add(context.Background(), "counter", 0, cache.CounterOptions{TTL: 20 * time.Millisecond})
		if err != nil || value != 2 {
			t.Fatalf("counter expired before refreshed TTL: %d, %v", value, err)
		}
		time.Sleep(30 * time.Millisecond)
		value, err = counter.Add(context.Background(), "counter", 0, cache.CounterOptions{})
		if err != nil || value != 0 {
			t.Fatalf("counter after expiry = %d, %v; want 0, nil", value, err)
		}
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
				if err != nil || value != test.value {
					t.Fatalf("Add() = %d, %v; want %d, nil", value, err, test.value)
				}
				assertGet(t, store, "counter", strconv.FormatInt(test.value, 10))
				value, err = counter.Add(context.Background(), "counter", 0, cache.CounterOptions{})
				if err != nil || value != test.value {
					t.Fatalf("Add(0) = %d, %v; want %d, nil", value, err, test.value)
				}
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
				if !errors.Is(err, cache.ErrInvalidValue) {
					t.Fatalf("Add() error = %v, want ErrInvalidValue", err)
				}
				assertGet(t, store, "counter", test.initial)
				if test.initial == "-9223372036854775808" {
					value, err := counter.Add(context.Background(), "counter", math.MaxInt64, cache.CounterOptions{})
					if err != nil || value != -1 {
						t.Fatalf("Add() after rejected overflow = %d, %v; want -1, nil", value, err)
					}
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
		if !errors.Is(err, cache.ErrInvalidValue) {
			t.Fatalf("Add() error = %v, want ErrInvalidValue", err)
		}
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
		if !ok {
			t.Fatal("Purge advertised but Store does not implement cache.Purger")
		}
		for i := range 3 {
			mustSet(t, store, "key-"+strconv.Itoa(i), fmt.Sprint(i), cache.SetOptions{})
		}
		if err := purger.Purge(context.Background()); err != nil {
			t.Fatalf("Purge() error = %v", err)
		}
		for i := range 3 {
			_, err := store.Get(context.Background(), "key-"+strconv.Itoa(i))
			if !errors.Is(err, cache.ErrNotFound) {
				t.Fatalf("Get() after purge error = %v", err)
			}
		}
	})
}

func mustSet(t *testing.T, store cache.Store[string], key, value string, options cache.SetOptions) {
	t.Helper()
	if err := store.Set(context.Background(), key, value, options); err != nil {
		t.Fatalf("Set(%q) error = %v", key, err)
	}
}

func assertGet(t *testing.T, store cache.Store[string], key, want string) {
	t.Helper()
	got, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	if got != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
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
	t.Fatal("condition was not satisfied before timeout")
}
