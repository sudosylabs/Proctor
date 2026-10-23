package memory_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/memory"
)

func TestCounterRejectsEncodedValue(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.JSONCodec[string]())
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), "key", "not-an-integer", cache.SetOptions{}))
	_, err = store.Add(t.Context(), "key", 1, cache.CounterOptions{})
	require.ErrorIs(t, err, cache.ErrInvalidValue, "Add() error")
}

func TestValueFloodNeverEvictsALiveCounter(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 2, MaxBytes: 1024, MaxCounters: 4})
	require.NoError(t, err)
	_, err = store.Add(t.Context(), "attempts/victim", 3, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err)
	for index := range 100 {
		require.NoError(t, store.Set(t.Context(), "flood/"+strconv.Itoa(index), "value", cache.SetOptions{}))
	}
	value, err := store.Add(t.Context(), "attempts/victim", 0, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err)
	require.Equal(t, int64(3), value, "the counter survived the value flood")
}

func TestCounterFloodFailsClosedInsteadOfEvicting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 100, MaxBytes: 1 << 20, MaxCounters: 3})
	require.NoError(t, err)
	memory.SetClock(store, func() time.Time { return now })
	_, err = store.Add(t.Context(), "attempts/victim", 5, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err)
	for _, key := range []string{"attempts/random-a", "attempts/random-b"} {
		_, err = store.Add(t.Context(), key, 1, cache.CounterOptions{TTL: 2 * time.Minute})
		require.NoError(t, err)
	}
	_, err = store.Add(t.Context(), "attempts/random-c", 1, cache.CounterOptions{TTL: time.Minute})
	require.ErrorIs(t, err, cache.ErrCapacity, "a new counter beyond the bound is refused")
	value, err := store.Add(t.Context(), "attempts/victim", 1, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err, "an existing counter keeps counting while the space is full")
	require.Equal(t, int64(6), value)

	now = now.Add(time.Minute)
	_, err = store.Add(t.Context(), "attempts/random-c", 1, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err, "an expired window frees its slot")
	_, err = store.Get(t.Context(), "attempts/victim")
	require.ErrorIs(t, err, cache.ErrNotFound, "the expired counter is gone")

	require.NoError(t, store.Delete(t.Context(), "attempts/random-a"))
	_, err = store.Add(t.Context(), "attempts/random-d", 1, cache.CounterOptions{TTL: time.Minute})
	require.NoError(t, err, "a deleted counter frees its slot")
}

func TestCountersShareTheValueKeySpace(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 1, MaxBytes: 1024, MaxCounters: 1})
	require.NoError(t, err)
	_, err = store.Add(t.Context(), "counter", 7, cache.CounterOptions{})
	require.NoError(t, err)
	value, err := store.Get(t.Context(), "counter")
	require.NoError(t, err)
	require.Equal(t, "7", value)
	require.NoError(t, store.Set(t.Context(), "counter", "8", cache.SetOptions{}))
	added, err := store.Add(t.Context(), "counter", 1, cache.CounterOptions{})
	require.NoError(t, err, "an integer value turns back into a counter")
	require.Equal(t, int64(9), added)
	require.NoError(t, store.Set(t.Context(), "value", "kept", cache.SetOptions{}))
	value, err = store.Get(t.Context(), "value")
	require.NoError(t, err, "the counter does not occupy the value budget")
	require.Equal(t, "kept", value)
}

func TestZeroMaxCountersDisablesCounters(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.False(t, store.Capabilities().AtomicCounter)
	_, err = store.Add(t.Context(), "counter", 1, cache.CounterOptions{})
	require.ErrorIs(t, err, cache.ErrUnsupported)
}
