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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	require.LessOrEqual(t, runtime.renewalInterval+runtime.operationLimit+runtime.stopMargin, runtime.claim.Lifetime, "test lease has no fail-closed expiry margin")
}

func (f *servingNodeLeasePersistenceFake) Delete(_ context.Context, input *store.ServingNodeLeaseDeletion) error {
	nodeID := input.NodeID
	leaseID := input.LeaseID
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
	require.NoError(t, err)
	require.Equal(t, 0, persistence.upserts, "construction upserts")
	require.NoError(t, runtime.Close())
	require.Equal(t, 0, persistence.deletes, "inert Close deletes")
}

func TestServingNodeLeaseRenewalSurvivesTransientFailureShorterThanSlack(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, recoverOnCall: 6, renewalErr: errors.New("database failover")}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	require.NoError(t, err)
	useFastServingNodeLeaseTiming(t, runtime)
	require.NoError(t, runtime.Start(context.Background()))
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-runtime.Failures():
			require.FailNow(t, fmt.Sprintf("transient renewal failure was terminal: %v", err))
		default:
		}
		if upserts, _ := persistence.calls(); upserts >= 10 {
			break
		}
		require.False(t, time.Now().After(deadline), "renewal did not resume after the transient failure")
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, runtime.Close())
	_, deletes := persistence.calls()
	require.Equal(t, 1, deletes, "recovered lease withdrawals")
}

func TestServingNodeLeaseRenewalFailsBeforeLastProvenLeaseExpires(t *testing.T) {
	t.Parallel()

	renewalErr := errors.New("database renewal failed")
	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, renewalErr: renewalErr}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	require.NoError(t, err)
	useFastServingNodeLeaseTiming(t, runtime)
	startedAt := time.Now()
	require.NoError(t, runtime.Start(context.Background()))
	select {
	case err := <-runtime.Failures():
		require.ErrorIs(t, err, renewalErr, "Failures()")
	case <-time.After(5 * time.Second):
		require.FailNow(t, "renewal failure was not surfaced")
	}
	elapsed := time.Since(startedAt)
	require.Less(t, elapsed, runtime.claim.Lifetime, "failure surfaced after")
	require.GreaterOrEqual(t, elapsed, runtime.renewalInterval+runtime.retryInterval, "failure surfaced after")
	require.NoError(t, runtime.Close())
	upserts, deletes := persistence.calls()
	require.GreaterOrEqual(t, upserts, 3, "lease persistence calls = upserts")
	require.Equal(t, 0, deletes, "lease persistence calls = upserts")
}

func TestServingNodeLeaseNormalCloseWithdrawsExactIncarnation(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	require.NoError(t, err)
	require.NoError(t, runtime.Start(context.Background()))
	require.NoError(t, runtime.Close())
	require.Equal(t, 1, persistence.upserts, "lease persistence calls = upserts")
	require.Equal(t, 1, persistence.deletes, "lease persistence calls = upserts")
	require.Equal(t, runtime.claim.NodeID, persistence.deletedNode, "lease persistence calls = upserts")
	require.Equal(t, runtime.claim.LeaseID, persistence.deletedID, "lease persistence calls = upserts")
}

func TestServingNodeLeaseExpiredIncarnationRejectionIsTerminal(t *testing.T) {
	t.Parallel()

	expiredErr := store.NewErrConflict("serving_node_lease", "lease_expired", nil)
	persistence := &servingNodeLeasePersistenceFake{failOnCall: 2, renewalErr: expiredErr}
	runtime, err := newServingNodeLeaseRuntime(persistence, "paused-node")
	require.NoError(t, err)
	useFastServingNodeLeaseTiming(t, runtime)
	require.NoError(t, runtime.Start(context.Background()))
	select {
	case err := <-runtime.Failures():
		upserts, _ := persistence.calls()
		require.Equal(t, 2, upserts, "expired incarnation was retried: upserts")
		require.ErrorIs(t, err, expiredErr, "Failures()")
	case <-time.After(time.Second):
		require.FailNow(t, "expired incarnation rejection was not terminal")
	}
	require.NoError(t, runtime.Close())
}

func TestServingNodeLeaseProductionTimingHasBoundedExpiryMargin(t *testing.T) {
	t.Parallel()
	require.True(t, validServingNodeLeaseTiming(servingNodeLeaseLifetime, servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseStopMargin), "serving lease timing = renewal %s + operation %s + stop margin %s, lifetime %s", servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseStopMargin, servingNodeLeaseLifetime)
	require.False(t, validServingNodeLeaseTiming(10*time.Second, 5*time.Second, 5*time.Second, time.Second), "timing without a fail-closed expiry margin was accepted")
}

func TestServingNodeLeaseKeepsRenewingAfterStartContextEndsUntilClose(t *testing.T) {
	t.Parallel()

	persistence := &servingNodeLeasePersistenceFake{}
	runtime, err := newServingNodeLeaseRuntime(persistence, "node-a")
	require.NoError(t, err)
	useFastServingNodeLeaseTiming(t, runtime)
	// The server cancels its run context when graceful shutdown begins, while
	// HTTP may still serve for the whole shutdown timeout.
	startCtx, cancelStart := context.WithCancel(context.Background())
	require.NoError(t, runtime.Start(startCtx))
	cancelStart()
	upsertsAtShutdown, _ := persistence.calls()
	require.Eventually(t, func() bool {
		upserts, _ := persistence.calls()
		return upserts >= upsertsAtShutdown+3
	}, 5*time.Second, 5*time.Millisecond, "serving lease stopped renewing when shutdown began")
	require.NoError(t, runtime.Close())
	_, deletes := persistence.calls()
	require.Equal(t, 1, deletes, "lease withdrawals")
}
