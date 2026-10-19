// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	mu            sync.Mutex
	upserts       int
	deletes       int
	failOnCall    int
	recoverOnCall int
	renewalErr    error
	deletedNode   string
	deletedID     string
}

func (f *servingNodeLeasePersistenceFake) Upsert(_ context.Context, claim *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	if f.failOnCall > 0 && f.upserts >= f.failOnCall && (f.recoverOnCall == 0 || f.upserts < f.recoverOnCall) {
		return nil, f.renewalErr
	}
	at := time.Date(2026, 8, 20, 12, 0, 0, f.upserts, time.UTC)
	return &store.ServingNodeLease{NodeID: claim.NodeID, LeaseID: claim.LeaseID, UpdatedAt: at, ExpiresAt: at.Add(claim.Lifetime)}, nil
}

func (f *servingNodeLeasePersistenceFake) calls() (upserts, deletes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upserts, f.deletes
}

// useFastServingNodeLeaseTiming keeps the production proportions (renewal and
// one bounded attempt plus the stop margin fit inside the lifetime) at test
// scale.
func useFastServingNodeLeaseTiming(t *testing.T, runtime *servingNodeLeaseRuntime) {
	t.Helper()
	runtime.claim.Lifetime = time.Second
	runtime.renewalInterval = 20 * time.Millisecond
	runtime.operationLimit = 20 * time.Millisecond
	runtime.retryInterval = 10 * time.Millisecond
	runtime.stopMargin = 250 * time.Millisecond
	if runtime.renewalInterval+runtime.operationLimit+runtime.stopMargin > runtime.claim.Lifetime {
		t.Fatal("test lease has no fail-closed expiry margin")
	}
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

func TestServingNodeLeaseRenewalSurvivesTransientFailureShorterThanSlack(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, recoverOnCall: 6, renewalErr: errors.New("database failover")}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	useFastServingNodeLeaseTiming(t, runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-runtime.Failures():
			t.Fatalf("transient renewal failure was terminal: %v", err)
		default:
		}
		if upserts, _ := persistence.calls(); upserts >= 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewal did not resume after the transient failure")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if _, deletes := persistence.calls(); deletes != 1 {
		t.Fatalf("recovered lease withdrawals = %d, want 1", deletes)
	}
}

func TestServingNodeLeaseRenewalFailsBeforeLastProvenLeaseExpires(t *testing.T) {
	t.Parallel()

	renewalErr := errors.New("database renewal failed")
	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, renewalErr: renewalErr}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	useFastServingNodeLeaseTiming(t, runtime)
	startedAt := time.Now()
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runtime.Failures():
		if !errors.Is(err, renewalErr) {
			t.Fatalf("Failures() = %v, want %v", err, renewalErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("renewal failure was not surfaced")
	}
	elapsed := time.Since(startedAt)
	if elapsed >= runtime.claim.Lifetime {
		t.Fatalf("failure surfaced after %s, at or after the %s lease lifetime", elapsed, runtime.claim.Lifetime)
	}
	if elapsed < runtime.renewalInterval+runtime.retryInterval {
		t.Fatalf("failure surfaced after %s without retrying within the slack", elapsed)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	upserts, deletes := persistence.calls()
	if upserts < 3 || deletes != 0 {
		t.Fatalf("lease persistence calls = upserts:%d deletes:%d, want retries and no withdrawal", upserts, deletes)
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
	useFastServingNodeLeaseTiming(t, runtime)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runtime.Failures():
		if upserts, _ := persistence.calls(); upserts != 2 {
			t.Fatalf("expired incarnation was retried: upserts = %d, want 2", upserts)
		}
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
	if !validServingNodeLeaseTiming(servingNodeLeaseLifetime, servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseStopMargin) {
		t.Fatalf("serving lease timing = renewal %s + operation %s + stop margin %s, lifetime %s", servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseStopMargin, servingNodeLeaseLifetime)
	}
	if validServingNodeLeaseTiming(10*time.Second, 5*time.Second, 5*time.Second, time.Second) {
		t.Fatal("timing without a fail-closed expiry margin was accepted")
	}
}
