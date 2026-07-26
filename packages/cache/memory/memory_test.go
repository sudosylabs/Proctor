package memory_test

import (
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
