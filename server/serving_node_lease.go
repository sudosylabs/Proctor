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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	servingNodeLeaseLifetime        = 30 * time.Second
	servingNodeLeaseRenewalInterval = 5 * time.Second
	servingNodeLeaseOperationLimit  = 5 * time.Second
	servingNodeLeaseRetryInterval   = time.Second
	servingNodeLeaseStopMargin      = 5 * time.Second
)

// servingNodeLeaseRuntime owns one process incarnation's installation-wide
// serving proof. A transient renewal failure is retried while the last proven
// lease still leaves room for one bounded attempt plus the stop margin. A
// rejected incarnation, invalid persisted state, or exhausted slack is
// terminal: Server observes Failures and force-stops HTTP serving before the
// already-committed lease can expire.
type servingNodeLeaseRuntime struct {
	store           store.ServingNodeLeaseStore
	claim           store.ServingNodeLeaseClaim
	failure         chan error
	renewalInterval time.Duration
	operationLimit  time.Duration
	retryInterval   time.Duration
	stopMargin      time.Duration

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	failed  bool
	closed  bool
}

func newServingNodeLeaseRuntime(persistence store.ServingNodeLeaseStore, nodeID string) (*servingNodeLeaseRuntime, error) {
	if persistence == nil || nodeID == "" {
		return nil, errors.New("serving node lease dependencies are invalid")
	}
	if !validServingNodeLeaseTiming(servingNodeLeaseLifetime, servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit, servingNodeLeaseStopMargin) ||
		servingNodeLeaseRetryInterval <= 0 {
		return nil, errors.New("serving node lease timing is invalid")
	}
	return &servingNodeLeaseRuntime{
		store:   persistence,
		claim:   store.ServingNodeLeaseClaim{NodeID: nodeID, LeaseID: model.NewID(), Lifetime: servingNodeLeaseLifetime},
		failure: make(chan error, 1), renewalInterval: servingNodeLeaseRenewalInterval, operationLimit: servingNodeLeaseOperationLimit,
		retryInterval: servingNodeLeaseRetryInterval, stopMargin: servingNodeLeaseStopMargin,
	}, nil
}

// validServingNodeLeaseTiming requires that a regular renewal attempt can
// finish and still leave the stop margin before the previous lease expires.
func validServingNodeLeaseTiming(lifetime, renewalInterval, operationLimit, stopMargin time.Duration) bool {
	return lifetime >= store.ServingNodeLeaseMinimumLifetime && lifetime <= store.ServingNodeLeaseMaximumLifetime &&
		renewalInterval > 0 && operationLimit > 0 && stopMargin > 0 && renewalInterval+operationLimit+stopMargin <= lifetime
}

func (r *servingNodeLeaseRuntime) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("serving node lease runtime is nil")
	}
	r.mu.Lock()
	if r.closed || r.started {
		r.mu.Unlock()
		return errors.New("serving node lease runtime cannot start")
	}
	r.mu.Unlock()
	provenAt := time.Now()
	if err := r.renew(ctx); err != nil {
		return err
	}
	// Renewal outlives ctx: the server cancels its run context when graceful
	// shutdown begins, but HTTP keeps serving until it drains, so the lease
	// must keep proving this node until Close after serving has stopped.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.mu.Lock()
	if r.closed || r.started {
		r.mu.Unlock()
		cancel()
		return errors.New("serving node lease runtime cannot start")
	}
	r.cancel, r.done, r.started = cancel, make(chan struct{}), true
	done := r.done
	r.mu.Unlock()
	go r.maintain(runCtx, done, provenAt)
	return nil
}

func (r *servingNodeLeaseRuntime) Failures() <-chan error {
	if r == nil {
		return nil
	}
	return r.failure
}

func (r *servingNodeLeaseRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cancel, done, started := r.cancel, r.done, r.started
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	r.mu.Lock()
	failed := r.failed
	r.mu.Unlock()
	if !started || failed {
		return nil
	}
	ctx, cancelDelete := context.WithTimeout(context.Background(), r.operationLimit)
	defer cancelDelete()
	if err := r.store.Delete(ctx, &store.ServingNodeLeaseDeletion{NodeID: r.claim.NodeID, LeaseID: r.claim.LeaseID}); err != nil {
		return fmt.Errorf("withdraw serving node lease: %w", err)
	}
	return nil
}

func (r *servingNodeLeaseRuntime) maintain(ctx context.Context, done chan struct{}, provenAt time.Time) {
	defer close(done)
	timer := time.NewTimer(r.renewalInterval)
	defer timer.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		// provenAt is the local start of the last successful renewal. The
		// database stamps that lease no earlier, so it cannot expire before
		// provenAt+lifetime; serving stops at least stopMargin before then.
		stopBy := provenAt.Add(r.claim.Lifetime - r.stopMargin)
		attemptAt := time.Now()
		if attemptAt.Add(r.operationLimit).After(stopBy) {
			r.fail(errors.Join(errors.New("renew serving node lease: retry slack exhausted"), lastErr))
			return
		}
		renewCtx, cancel := context.WithTimeout(ctx, r.operationLimit)
		err := r.renew(renewCtx)
		cancel()
		if err == nil {
			provenAt, lastErr = attemptAt, nil
			timer.Reset(r.renewalInterval)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if terminalServingNodeLeaseError(err) {
			r.fail(fmt.Errorf("renew serving node lease: %w", err))
			return
		}
		lastErr = err
		timer.Reset(r.retryInterval)
	}
}

func (r *servingNodeLeaseRuntime) fail(err error) {
	r.mu.Lock()
	r.failed = true
	r.mu.Unlock()
	select {
	case r.failure <- err:
	default:
	}
}

var errServingNodeLeaseInvalidState = errors.New("serving node lease persistence returned invalid state")

// terminalServingNodeLeaseError reports failures that retrying cannot repair:
// a rejected or expired incarnation, invalid input, or invalid persisted state.
func terminalServingNodeLeaseError(err error) bool {
	var conflict *store.ErrConflict
	var invalid *store.ErrInvalidInput
	return errors.As(err, &conflict) || errors.As(err, &invalid) || errors.Is(err, errServingNodeLeaseInvalidState)
}

func (r *servingNodeLeaseRuntime) renew(ctx context.Context) error {
	lease, err := r.store.Upsert(ctx, &r.claim)
	if err != nil {
		return err
	}
	if lease == nil || lease.NodeID != r.claim.NodeID || lease.LeaseID != r.claim.LeaseID ||
		!lease.ExpiresAt.Equal(lease.UpdatedAt.Add(r.claim.Lifetime)) {
		return errServingNodeLeaseInvalidState
	}
	return nil
}
