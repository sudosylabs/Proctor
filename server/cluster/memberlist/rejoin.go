// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package memberlist

import (
	"context"

	hashimemberlist "github.com/hashicorp/memberlist"
)

// seedReconciler is private transport machinery. It serializes bounded join
// batches inside the discovery-maintenance goroutine and rotates through
// unavailable candidates so one bad static seed cannot starve later peers.
type seedReconciler struct {
	list *hashimemberlist.Memberlist
	next int
}

func newSeedReconciler(list *hashimemberlist.Memberlist) *seedReconciler {
	return &seedReconciler{list: list}
}

func (r *seedReconciler) rejoin(ctx context.Context, seeds []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	batch, next := nextJoinBatch(seeds, r.next)
	r.next = next
	if len(batch) == 0 {
		return nil
	}
	_, err := r.list.Join(batch)
	return err
}

func nextJoinBatch(seeds []string, next int) ([]string, int) {
	seeds = uniqueStrings(seeds)
	if len(seeds) == 0 {
		return nil, 0
	}
	if next < 0 || next >= len(seeds) {
		next = 0
	}
	count := min(maximumJoinAttemptsPerRun, len(seeds))
	batch := make([]string, 0, count)
	for offset := range count {
		batch = append(batch, seeds[(next+offset)%len(seeds)])
	}
	return batch, (next + count) % len(seeds)
}
