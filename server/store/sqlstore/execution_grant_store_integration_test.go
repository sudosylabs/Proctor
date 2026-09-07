//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExecutionLifecycleLeaseLeavesPoolCapacityForOwner(t *testing.T) {
	for _, sameGrant := range []bool{true, false} {
		name := "different-grants"
		if sameGrant {
			name = "same-grant"
		}
		t.Run(name, func(t *testing.T) {
			persistence := openExecutionLeaseTestStore(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			grantID := model.NewExecutionGrantID()
			lease, err := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, grantID)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release(context.Background())
			waitFor := grantID
			if !sameGrant {
				waitFor = model.NewExecutionGrantID()
			}
			waiter, stopWaiter := context.WithTimeout(ctx, 150*time.Millisecond)
			defer stopWaiter()
			result := make(chan executionLeaseTestResult, 1)
			go func() {
				next, err := persistence.ExecutionGrant().AcquireLifecycleLease(waiter, waitFor)
				result <- executionLeaseTestResult{lease: next, err: err}
			}()
			queryCtx, stopQuery := context.WithTimeout(ctx, 100*time.Millisecond)
			var one int
			queryErr := persistence.GetMaster().Get(queryCtx, &one, `SELECT 1`)
			stopQuery()
			blocked := <-result
			if blocked.lease != nil {
				_ = blocked.lease.Release(context.Background())
			}
			if queryErr != nil || one != 1 {
				t.Fatalf("lease owner could not borrow an ordinary connection: one=%d err=%v", one, queryErr)
			}
			if !errors.Is(blocked.err, context.DeadlineExceeded) {
				t.Fatalf("competing lease = %v, want cancellation while pool capacity is reserved", blocked.err)
			}
			if err := lease.Release(ctx); err != nil {
				t.Fatal(err)
			}
			replacement, err := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, waitFor)
			if err != nil {
				t.Fatalf("canceled waiter did not leave its slot reusable: %v", err)
			}
			if err := replacement.Release(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecutionLifecycleLeaseContentionReturnsSlot(t *testing.T) {
	persistence := openExecutionLeaseTestStore(t)
	peer := openExecutionLeaseTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	grantID := model.NewExecutionGrantID()
	lease, err := peer.ExecutionGrant().AcquireLifecycleLease(ctx, grantID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(context.Background())
	waiter, stopWaiter := context.WithTimeout(ctx, 150*time.Millisecond)
	defer stopWaiter()
	result := make(chan executionLeaseTestResult, 1)
	go func() {
		next, err := persistence.ExecutionGrant().AcquireLifecycleLease(waiter, grantID)
		result <- executionLeaseTestResult{lease: next, err: err}
	}()
	other, otherErr := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, model.NewExecutionGrantID())
	if other != nil {
		defer other.Release(context.Background())
	}
	blocked := <-result
	if blocked.lease != nil {
		_ = blocked.lease.Release(context.Background())
	}
	if otherErr != nil {
		t.Fatalf("another grant could not acquire during advisory lock contention: %v", otherErr)
	}
	if !errors.Is(blocked.err, context.DeadlineExceeded) {
		t.Fatalf("peer-owned grant acquisition = %v, want deadline exceeded", blocked.err)
	}
}

func TestExecutionLifecycleLeaseDiscardsFailedUnlock(t *testing.T) {
	persistence := openExecutionLeaseTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	grantID := model.NewExecutionGrantID()
	lease, err := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, grantID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancelUnlock := context.WithCancel(ctx)
	cancelUnlock()
	if err := lease.Release(canceled); err == nil {
		t.Fatal("canceled unlock should report its failure")
	}
	replacement, err := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, grantID)
	if err != nil {
		t.Fatalf("failed unlock retained its connection or slot: %v", err)
	}
	defer replacement.Release(context.Background())
	if err := replacement.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(ctx); err == nil {
		t.Fatal("repeated release lost its original failure")
	}
	blockedCtx, stopBlocked := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stopBlocked()
	other, err := persistence.ExecutionGrant().AcquireLifecycleLease(blockedCtx, model.NewExecutionGrantID())
	if other != nil {
		_ = other.Release(context.Background())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("repeated release freed a successor's slot: %v", err)
	}
}

func TestExecutionLifecycleLeaseAcquisitionFailureReturnsSlot(t *testing.T) {
	persistence := openExecutionLeaseTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	canceled, cancelAcquire := context.WithCancel(ctx)
	cancelAcquire()
	if _, err := persistence.ExecutionGrant().AcquireLifecycleLease(canceled, model.NewExecutionGrantID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition = %v", err)
	}
	lease, err := persistence.ExecutionGrant().AcquireLifecycleLease(ctx, model.NewExecutionGrantID())
	if err != nil {
		t.Fatalf("canceled acquisition retained its slot: %v", err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		failed, stopFailed := context.WithTimeout(ctx, 100*time.Millisecond)
		_, err = persistence.ExecutionGrant().AcquireLifecycleLease(failed, model.NewExecutionGrantID())
		stopFailed()
		if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("closed pool acquisition did not return its slot: %v", err)
		}
	}
}

func TestExecutionLifecycleLeaseRejectsUndersizedPool(t *testing.T) {
	for _, maximum := range []int{0, 1} {
		settings := testSettings(t)
		settings.MaxOpenConnections, settings.MaxIdleConnections = maximum, 0
		persistence, err := New(t.Context(), settings)
		if persistence != nil {
			_ = persistence.Close()
		}
		if err == nil {
			t.Fatalf("New(maximum=%d) accepted a pool without ordinary query capacity", maximum)
		}
	}
}

type executionLeaseTestResult struct {
	lease store.ExecutionLifecycleLease
	err   error
}

// These tests use advisory locks only. Their own pool does not migrate or reset
// the integration database, and its configured budget stays fixed after New.
func openExecutionLeaseTestStore(t *testing.T) *SQLStore {
	t.Helper()
	settings := testSettings(t)
	settings.MaxOpenConnections, settings.MaxIdleConnections = 2, 2
	persistence, err := New(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := persistence.Close(); err != nil {
			t.Errorf("close SQL store: %v", err)
		}
	})
	return persistence
}
