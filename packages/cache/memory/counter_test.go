package memory_test

import (
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/packages/cache"
	"github.com/sudosylabs/proctor/packages/cache/memory"
)

func TestCounterRejectsEncodedValue(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.JSONCodec[string]())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "key", "not-an-integer", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(t.Context(), "key", 1, cache.CounterOptions{}); !errors.Is(err, cache.ErrInvalidValue) {
		t.Fatalf("Add() error = %v, want ErrInvalidValue", err)
	}
}
