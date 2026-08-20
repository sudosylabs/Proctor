// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

type servingNodeLeasePersistenceFake struct {
	mu          sync.Mutex
	upserts     int
	deletes     int
	failOnCall  int
	renewalErr  error
	deletedNode string
	deletedID   string
}

func (f *servingNodeLeasePersistenceFake) Upsert(_ context.Context, claim *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	if f.failOnCall > 0 && f.upserts >= f.failOnCall {
		return nil, f.renewalErr
	}
	at := time.Date(2026, 8, 20, 12, 0, 0, f.upserts, time.UTC)
	return &store.ServingNodeLease{NodeID: claim.NodeID, LeaseID: claim.LeaseID, UpdatedAt: at, ExpiresAt: at.Add(claim.Lifetime)}, nil
}

func (f *servingNodeLeasePersistenceFake) Delete(_ context.Context, nodeID, leaseID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	f.deletedNode, f.deletedID = nodeID, leaseID
	return nil
}

func TestServingNodeLeaseConstructionIsInert(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if persistence.upserts != 0 {
		t.Fatalf("construction upserts = %d, want 0", persistence.upserts)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if persistence.deletes != 0 {
		t.Fatalf("inert Close deletes = %d, want 0", persistence.deletes)
	}
}

func TestServingNodeLeaseRenewalFailureIsTerminalBeforeExpiryMargin(t *testing.T) {
	t.Parallel()

	renewalErr := errors.New("database renewal failed")
	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, renewalErr: renewalErr}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	runtime.claim.Lifetime = 200 * time.Millisecond
	runtime.renewalInterval = 10 * time.Millisecond
	runtime.operationLimit = 20 * time.Millisecond
	if runtime.renewalInterval+runtime.operationLimit >= runtime.claim.Lifetime {
		t.Fatal("test lease has no fail-closed expiry margin")
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runtime.Failures():
		if !errors.Is(err, renewalErr) {
			t.Fatalf("Failures() = %v, want %v", err, renewalErr)
		}
	case <-time.After(time.Second):
		t.Fatal("renewal failure was not surfaced")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if persistence.upserts != 2 || persistence.deletes != 0 {
		t.Fatalf("lease persistence calls = upserts:%d deletes:%d deleted:%s/%s", persistence.upserts, persistence.deletes, persistence.deletedNode, persistence.deletedID)
	}
}

func TestServingNodeLeaseNormalCloseWithdrawsExactIncarnation(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if persistence.upserts != 1 || persistence.deletes != 1 ||
		persistence.deletedNode != runtime.claim.NodeID || persistence.deletedID != runtime.claim.LeaseID {
		t.Fatalf("lease persistence calls = upserts:%d deletes:%d deleted:%s/%s", persistence.upserts, persistence.deletes, persistence.deletedNode, persistence.deletedID)
	}
}

func TestServingNodeLeaseExpiredIncarnationRejectionIsTerminal(t *testing.T) {
	t.Parallel()

	expiredErr := store.NewErrConflict("serving_node_lease", "lease_expired", nil)
	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, renewalErr: expiredErr}
	runtime, err := newServingNodeLeaseRuntime(persistence, "paused-node")
	if err != nil {
		t.Fatal(err)
	}
	runtime.claim.Lifetime = 200 * time.Millisecond
	runtime.renewalInterval = 10 * time.Millisecond
	runtime.operationLimit = 20 * time.Millisecond
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runtime.Failures():
		if !errors.Is(err, expiredErr) {
			t.Fatalf("Failures() = %v, want expired incarnation conflict", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expired incarnation rejection was not terminal")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServingNodeLeaseProductionTimingHasBoundedExpiryMargin(t *testing.T) {
	t.Parallel()
	if !validServingNodeLeaseTiming(servingNodeLeaseLifetime, servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit) {
		t.Fatalf("serving lease timing = renewal %s + operation %s, lifetime %s", servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseLifetime)
	}
	if validServingNodeLeaseTiming(10*time.Second, 5*time.Second, 5*time.Second) {
		t.Fatal("timing without a fail-closed expiry margin was accepted")
	}
}
