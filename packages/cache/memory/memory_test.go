package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/cachetest"
	"github.com/sudosylabs/proctor/packages/cache/memory"
)

func TestConformance(t *testing.T) {
	cachetest.Run(t, func(t *testing.T) cache.Store[string] {
		t.Helper()
		store, err := memory.New(cache.StringCodec())
		require.NoError(t, err)
		return store
	})
}

func TestLRUEvictsTheLeastRecentlyUsedEntry(t *testing.T) {
	t.Parallel()
	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 2, MaxBytes: 1024})
	require.NoError(t, err)
	for _, key := range []string{"first", "second"} {
		require.NoError(t, store.Set(t.Context(), key, key, cache.SetOptions{}))
	}
	_, err = store.Get(t.Context(), "first")
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), "third", "third", cache.SetOptions{}))
	_, err = store.Get(t.Context(), "second")
	require.ErrorIs(t, err, cache.ErrNotFound, "least-recent entry Get() error")
	value, err := store.Get(t.Context(), "first")
	require.NoError(t, err, "recent entry Get()")
	require.Equal(t, "first", value, "recent entry Get()")
}

func TestByteBudgetEvictsAndRejectsAnOversizedEntry(t *testing.T) {
	t.Parallel()
	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 10, MaxBytes: 11})
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), "a", "12345", cache.SetOptions{}))
	require.NoError(t, store.Set(t.Context(), "b", "67890", cache.SetOptions{}))
	_, err = store.Get(t.Context(), "a")
	require.ErrorIs(t, err, cache.ErrNotFound, "byte-budget eviction error")
	require.ErrorIs(t, store.Set(t.Context(), "oversized", "payload", cache.SetOptions{}), cache.ErrInvalidValue, "oversized Set() error")
}

func TestConfigurationMustBeBounded(t *testing.T) {
	t.Parallel()
	for _, configuration := range []memory.Config{{MaxBytes: 1}, {MaxEntries: 1}} {
		_, err := memory.New(cache.StringCodec(), configuration)
		require.Error(t, err, "New(%#v) error = nil", configuration)
	}
	_, err := memory.New(cache.StringCodec(), memory.DefaultConfig(), memory.DefaultConfig())
	require.Error(t, err, "New() accepted multiple configurations")
}

func TestEncodedIsolation(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.BytesCodec())
	require.NoError(t, err)
	source := []byte("value")
	require.NoError(t, store.Set(t.Context(), "key", source, cache.SetOptions{}))
	source[0] = 'X'
	first, err := store.Get(t.Context(), "key")
	require.NoError(t, err)
	first[0] = 'Y'
	second, err := store.Get(t.Context(), "key")
	require.NoError(t, err)
	require.Equal(t, "value", string(second), "stored value was aliased")
}
