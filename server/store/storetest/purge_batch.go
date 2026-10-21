// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PurgeBatchObservation exposes only the persistence facts needed to prove
// fair retry and writer uncertainty. It contains no backend key or content.
type PurgeBatchObservation struct {
	Exists, ObservedAbsent, VerifiedAbsent, NextAttemptDeferred bool
}

// PurgeBatchProbe seeds eligible cleanup rows under an already established
// retired or expired fixture. Seed returns their distinct due-time order; the
// final key has an unfinished writer when unknownLast is true.
type PurgeBatchProbe[K comparable] struct {
	Begin, PeerBegin func(context.Context, int) ([]K, error)
	Complete         func(context.Context, K) error
	Seed             func(*testing.T, context.Context, int, bool) []K
	Observe          func(*testing.T, context.Context, K) PurgeBatchObservation
	RetryNow         func(*testing.T, context.Context, []K)
	FinishWriter     func(*testing.T, context.Context, K)
	MaximumBatch     int
}

// TestPurgeBatchProgress applies the same named batch contract to retained
// Workspace objects and expired Examination Export artifacts on every adapter.
func TestPurgeBatchProgress[K comparable](t *testing.T, probe PurgeBatchProbe[K]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const batch = 100
	keys := probe.Seed(t, ctx, 2*batch+1, true)
	require.Len(t, keys, 2*batch+1, "invalid purge fixture")
	for _, limit := range []int{0, -1, probe.MaximumBatch + 1} {
		_, err := probe.Begin(ctx, limit)
		require.Error(t, err, "invalid purge batch limit %d accepted", limit)
	}
	first, err := probe.Begin(ctx, batch)
	require.NoError(t, err)
	require.True(t, slices.Equal(first, keys[:batch]), "purge selection is not bounded oldest-due order")
	// Every first-batch VFS operation fails, or its selected result is lost.
	// Neither condition permits an absence completion.
	for _, key := range first {
		observed := probe.Observe(t, ctx, key)
		require.True(t, observed.Exists)
		require.False(t, observed.ObservedAbsent)
		require.False(t, observed.VerifiedAbsent)
		require.True(t, observed.NextAttemptDeferred)
	}
	second, err := probe.PeerBegin(ctx, batch)
	require.NoError(t, err)
	require.True(t, slices.Equal(second, keys[batch:2*batch]), "a full failed first batch starved later healthy keys")
	for _, key := range second {
		require.NoError(t, probe.Complete(ctx, key))
		observed := probe.Observe(t, ctx, key)
		if observed.Exists {
			require.True(t, observed.VerifiedAbsent)
			require.False(t, observed.NextAttemptDeferred)
		}
	}
	last, err := probe.Begin(ctx, batch)
	require.NoError(t, err)
	require.Len(t, last, 1, "cleanup did not progress beyond two full batches")
	require.Equal(t, keys[2*batch], last[0], "cleanup did not progress beyond two full batches")
	unknown := last[0]
	require.NoError(t, probe.Complete(ctx, unknown))
	observed := probe.Observe(t, ctx, unknown)
	require.True(t, observed.Exists)
	require.True(t, observed.ObservedAbsent)
	require.False(t, observed.VerifiedAbsent)
	require.True(t, observed.NextAttemptDeferred)
	none, err := probe.Begin(ctx, batch)
	require.NoError(t, err)
	require.Empty(t, none, "selected failures bypassed their retry interval")
	probe.RetryNow(t, ctx, first)
	retry, err := probe.PeerBegin(ctx, batch)
	require.NoError(t, err)
	assertPurgeKeySet(t, retry, first)
	for _, key := range retry {
		require.NoError(t, probe.Complete(ctx, key))
	}
	// Another absence observation cannot finish a still-unknown writer.
	probe.RetryNow(t, ctx, []K{unknown})
	again, err := probe.Begin(ctx, batch)
	require.NoError(t, err)
	require.Len(t, again, 1, "unknown writer was not eligible for exact-key re-observation")
	require.Equal(t, unknown, again[0], "unknown writer was not eligible for exact-key re-observation")
	require.NoError(t, probe.Complete(ctx, unknown))
	observed = probe.Observe(t, ctx, unknown)
	require.True(t, observed.Exists, "repeated absence falsely proved an unfinished writer complete")
	require.False(t, observed.VerifiedAbsent, "repeated absence falsely proved an unfinished writer complete")
	probe.FinishWriter(t, ctx, unknown)
	require.NoError(t, probe.Complete(ctx, unknown))
	observed = probe.Observe(t, ctx, unknown)
	if observed.Exists {
		require.True(t, observed.VerifiedAbsent, "finished writer remained pending after its independent absence observation")
		require.False(t, observed.NextAttemptDeferred, "finished writer remained pending after its independent absence observation")
	}

	concurrent := probe.Seed(t, ctx, 2*batch, false)
	type selected struct {
		keys []K
		err  error
	}
	start := make(chan struct{})
	results := make(chan selected, 2)
	for _, begin := range []func(context.Context, int) ([]K, error){probe.Begin, probe.PeerBegin} {
		go func() {
			<-start
			keys, err := begin(ctx, batch)
			results <- selected{keys: keys, err: err}
		}()
	}
	close(start)
	left, right := <-results, <-results
	require.NoError(t, left.err)
	require.NoError(t, right.err)
	require.Len(t, left.keys, batch, "concurrent purge batches exceeded or lost their bounds")
	require.Len(t, right.keys, batch, "concurrent purge batches exceeded or lost their bounds")
	assertPurgeKeySet(t, append(left.keys, right.keys...), concurrent)
	none, err = probe.Begin(ctx, batch)
	require.NoError(t, err)
	require.Empty(t, none, "concurrent selection did not durably defer both batches")
	for _, key := range concurrent {
		observed = probe.Observe(t, ctx, key)
		require.True(t, observed.Exists, "uncompleted concurrent claim lost its exact key or claimed absence")
		require.False(t, observed.ObservedAbsent, "uncompleted concurrent claim lost its exact key or claimed absence")
		require.False(t, observed.VerifiedAbsent, "uncompleted concurrent claim lost its exact key or claimed absence")
		require.True(t, observed.NextAttemptDeferred, "uncompleted concurrent claim lost its exact key or claimed absence")
	}
}

func assertPurgeKeySet[K comparable](t *testing.T, got, want []K) {
	t.Helper()
	remaining := make(map[K]bool, len(want))
	for _, key := range want {
		remaining[key] = true
	}
	require.Len(t, got, len(want), "purge batch count or fixture identity is invalid")
	require.Len(t, remaining, len(want), "purge batch count or fixture identity is invalid")
	for _, key := range got {
		require.True(t, remaining[key], "purge batches overlapped or returned an unrelated key")
		delete(remaining, key)
	}
}
