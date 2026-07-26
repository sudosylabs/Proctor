package memory_test

import (
	"errors"
	"math"
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

func TestCounterDetectsOverflow(t *testing.T) {
	t.Parallel()

	store, err := memory.New(cache.StringCodec())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "key", "9223372036854775807", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(t.Context(), "key", 1, cache.CounterOptions{}); !errors.Is(err, cache.ErrInvalidValue) {
		t.Fatalf("positive overflow error = %v, want ErrInvalidValue", err)
	}
	if err := store.Set(t.Context(), "key", "-9223372036854775808", cache.SetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(t.Context(), "key", -1, cache.CounterOptions{}); !errors.Is(err, cache.ErrInvalidValue) {
		t.Fatalf("negative overflow error = %v, want ErrInvalidValue", err)
	}
	if value, err := store.Add(t.Context(), "key", math.MaxInt64, cache.CounterOptions{}); err != nil || value != -1 {
		t.Fatalf("non-overflowing Add() = %d, %v; want -1, nil", value, err)
	}
}
