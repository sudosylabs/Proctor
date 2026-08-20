// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
)

type servingNodeLeasePersistence interface {
	Upsert(context.Context, *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error)
	Delete(context.Context, string, string) error
}

// servingNodeLeaseRuntime owns one process incarnation's installation-wide
// serving proof. A failed renewal is terminal: Server observes Failures and
// force-stops HTTP serving before the already-committed lease can expire.
type servingNodeLeaseRuntime struct {
	store           servingNodeLeasePersistence
	claim           store.ServingNodeLeaseClaim
	failure         chan error
	renewalInterval time.Duration
	operationLimit  time.Duration

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
	failed  bool
	closed  bool
}

func newServingNodeLeaseRuntime(persistence servingNodeLeasePersistence, nodeID string) (*servingNodeLeaseRuntime, error) {
	if persistence == nil || nodeID == "" {
		return nil, errors.New("serving node lease dependencies are invalid")
	}
	if !validServingNodeLeaseTiming(servingNodeLeaseLifetime, servingNodeLeaseRenewalInterval, servingNodeLeaseOperationLimit) {
		return nil, errors.New("serving node lease timing is invalid")
	}
	return &servingNodeLeaseRuntime{
		store:   persistence,
		claim:   store.ServingNodeLeaseClaim{NodeID: nodeID, LeaseID: model.NewId(), Lifetime: servingNodeLeaseLifetime},
		failure: make(chan error, 1), renewalInterval: servingNodeLeaseRenewalInterval, operationLimit: servingNodeLeaseOperationLimit,
	}, nil
}

func validServingNodeLeaseTiming(lifetime, renewalInterval, operationLimit time.Duration) bool {
	return lifetime >= store.ServingNodeLeaseMinimumLifetime && lifetime <= store.ServingNodeLeaseMaximumLifetime &&
		renewalInterval > 0 && operationLimit > 0 && renewalInterval+operationLimit < lifetime
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
	if err := r.renew(ctx); err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.closed || r.started {
		r.mu.Unlock()
		cancel()
		return errors.New("serving node lease runtime cannot start")
	}
	r.cancel, r.done, r.started = cancel, make(chan struct{}), true
	done := r.done
	r.mu.Unlock()
	go r.maintain(runCtx, done)
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
	if err := r.store.Delete(ctx, r.claim.NodeID, r.claim.LeaseID); err != nil {
		return fmt.Errorf("withdraw serving node lease: %w", err)
	}
	return nil
}

func (r *servingNodeLeaseRuntime) maintain(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(r.renewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, r.operationLimit)
			err := r.renew(renewCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				r.mu.Lock()
				r.failed = true
				r.mu.Unlock()
				select {
				case r.failure <- fmt.Errorf("renew serving node lease: %w", err):
				default:
				}
				return
			}
		}
	}
}

func (r *servingNodeLeaseRuntime) renew(ctx context.Context) error {
	lease, err := r.store.Upsert(ctx, &r.claim)
	if err != nil {
		return err
	}
	if lease == nil || lease.NodeID != r.claim.NodeID || lease.LeaseID != r.claim.LeaseID ||
		!lease.ExpiresAt.Equal(lease.UpdatedAt.Add(r.claim.Lifetime)) {
		return errors.New("serving node lease persistence returned invalid state")
	}
	return nil
}
