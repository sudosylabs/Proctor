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
	if len(keys) != 2*batch+1 {
		t.Fatal("invalid purge fixture")
	}
	for _, limit := range []int{0, -1, probe.MaximumBatch + 1} {
		if _, err := probe.Begin(ctx, limit); err == nil {
			t.Fatalf("invalid purge batch limit %d accepted", limit)
		}
	}
	first, err := probe.Begin(ctx, batch)
	requireNoError(t, err)
	if !slices.Equal(first, keys[:batch]) {
		t.Fatal("purge selection is not bounded oldest-due order")
	}
	// Every first-batch VFS operation fails, or its selected result is lost.
	// Neither condition permits an absence completion.
	for _, key := range first {
		observed := probe.Observe(t, ctx, key)
		if !observed.Exists || observed.ObservedAbsent || observed.VerifiedAbsent || !observed.NextAttemptDeferred {
			t.Fatalf("selection changed absence or lost its retry delay: %#v", observed)
		}
	}
	second, err := probe.PeerBegin(ctx, batch)
	requireNoError(t, err)
	if !slices.Equal(second, keys[batch:2*batch]) {
		t.Fatal("a full failed first batch starved later healthy keys")
	}
	for _, key := range second {
		requireNoError(t, probe.Complete(ctx, key))
		observed := probe.Observe(t, ctx, key)
		if observed.Exists && (!observed.VerifiedAbsent || observed.NextAttemptDeferred) {
			t.Fatalf("finished verified key remains pending behind selection backoff: %#v", observed)
		}
	}
	last, err := probe.Begin(ctx, batch)
	requireNoError(t, err)
	if len(last) != 1 || last[0] != keys[2*batch] {
		t.Fatal("cleanup did not progress beyond two full batches")
	}
	unknown := last[0]
	requireNoError(t, probe.Complete(ctx, unknown))
	observed := probe.Observe(t, ctx, unknown)
	if !observed.Exists || !observed.ObservedAbsent || observed.VerifiedAbsent || !observed.NextAttemptDeferred {
		t.Fatalf("unknown writer lost its exact-key reconciliation reference: %#v", observed)
	}
	none, err := probe.Begin(ctx, batch)
	requireNoError(t, err)
	if len(none) != 0 {
		t.Fatal("selected failures bypassed their retry interval")
	}
	probe.RetryNow(t, ctx, first)
	retry, err := probe.PeerBegin(ctx, batch)
	requireNoError(t, err)
	assertPurgeKeySet(t, retry, first)
	for _, key := range retry {
		requireNoError(t, probe.Complete(ctx, key))
	}
	// Another absence observation cannot finish a still-unknown writer.
	probe.RetryNow(t, ctx, []K{unknown})
	again, err := probe.Begin(ctx, batch)
	requireNoError(t, err)
	if len(again) != 1 || again[0] != unknown {
		t.Fatal("unknown writer was not eligible for exact-key re-observation")
	}
	requireNoError(t, probe.Complete(ctx, unknown))
	observed = probe.Observe(t, ctx, unknown)
	if !observed.Exists || observed.VerifiedAbsent {
		t.Fatal("repeated absence falsely proved an unfinished writer complete")
	}
	probe.FinishWriter(t, ctx, unknown)
	requireNoError(t, probe.Complete(ctx, unknown))
	observed = probe.Observe(t, ctx, unknown)
	if observed.Exists && (!observed.VerifiedAbsent || observed.NextAttemptDeferred) {
		t.Fatal("finished writer remained pending after its independent absence observation")
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
	requireNoError(t, left.err)
	requireNoError(t, right.err)
	if len(left.keys) != batch || len(right.keys) != batch {
		t.Fatal("concurrent purge batches exceeded or lost their bounds")
	}
	assertPurgeKeySet(t, append(left.keys, right.keys...), concurrent)
	none, err = probe.Begin(ctx, batch)
	requireNoError(t, err)
	if len(none) != 0 {
		t.Fatal("concurrent selection did not durably defer both batches")
	}
	for _, key := range concurrent {
		observed = probe.Observe(t, ctx, key)
		if !observed.Exists || observed.ObservedAbsent || observed.VerifiedAbsent || !observed.NextAttemptDeferred {
			t.Fatal("uncompleted concurrent claim lost its exact key or claimed absence")
		}
	}
}

func assertPurgeKeySet[K comparable](t *testing.T, got, want []K) {
	t.Helper()
	remaining := make(map[K]bool, len(want))
	for _, key := range want {
		remaining[key] = true
	}
	if len(got) != len(want) || len(remaining) != len(want) {
		t.Fatal("purge batch count or fixture identity is invalid")
	}
	for _, key := range got {
		if !remaining[key] {
			t.Fatal("purge batches overlapped or returned an unrelated key")
		}
		delete(remaining, key)
	}
}
