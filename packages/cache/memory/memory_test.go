package memory_test

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/cachetest"
	"github.com/sudosylabs/proctor/packages/cache/memory"
)

func TestConformance(t *testing.T) {
	cachetest.Run(t, func(t *testing.T) cache.Store[string] {
		t.Helper()
		store, err := memory.New(cache.StringCodec())
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return store
	})
}

func TestLRUEvictsTheLeastRecentlyUsedEntry(t *testing.T) {
	t.Parallel()
	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"first", "second"} {
		if err := store.Set(t.Context(), key, key, cache.SetOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Get(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "third", "third", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "second"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("least-recent entry Get() error = %v, want ErrNotFound", err)
	}
	if value, err := store.Get(t.Context(), "first"); err != nil || value != "first" {
		t.Fatalf("recent entry Get() = %q, %v", value, err)
	}
}

func TestByteBudgetEvictsAndRejectsAnOversizedEntry(t *testing.T) {
	t.Parallel()
	store, err := memory.New(cache.StringCodec(), memory.Config{MaxEntries: 10, MaxBytes: 11})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "a", "12345", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "b", "67890", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "a"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("byte-budget eviction error = %v, want ErrNotFound", err)
	}
	if err := store.Set(t.Context(), "oversized", "payload", cache.SetOptions{}); !errors.Is(err, cache.ErrInvalidValue) {
		t.Fatalf("oversized Set() error = %v, want ErrInvalidValue", err)
	}
}

func TestConfigurationMustBeBounded(t *testing.T) {
	t.Parallel()
	for _, configuration := range []memory.Config{{MaxBytes: 1}, {MaxEntries: 1}} {
		if _, err := memory.New(cache.StringCodec(), configuration); err == nil {
			t.Fatalf("New(%#v) error = nil", configuration)
		}
	}
	if _, err := memory.New(cache.StringCodec(), memory.DefaultConfig(), memory.DefaultConfig()); err == nil {
		t.Fatal("New() accepted multiple configurations")
	}
}

func TestEncodedIsolation(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.BytesCodec())
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("value")
	if err := store.Set(t.Context(), "key", source, cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	source[0] = 'X'
	first, err := store.Get(t.Context(), "key")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'Y'
	second, err := store.Get(t.Context(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != "value" {
		t.Fatalf("stored value was aliased: %q", second)
	}
}
