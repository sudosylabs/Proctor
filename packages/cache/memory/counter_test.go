package memory_test

import (
	"testing"

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
