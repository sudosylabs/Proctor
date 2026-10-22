// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultReadinessProbeTimeout bounds one mandatory-dependency probe.
	DefaultReadinessProbeTimeout = 2 * time.Second
)

// DependencyProbe reports whether mandatory runtime dependencies are usable.
type DependencyProbe func(context.Context) error

// Health combines the lifecycle readiness flag with a bounded, rate-limited
// probe of mandatory dependencies. A node is ready only while the lifecycle
// owner has published readiness and the most recent probe succeeded.
type Health struct {
	ready atomic.Bool

	probe    DependencyProbe
	timeout  time.Duration
	interval time.Duration
	now      func() time.Time

	mu        sync.Mutex
	probing   bool
	checkedAt time.Time
	healthy   bool
}

// NewHealth constructs readiness that probes dependencies at most once per
// interval, each probe bounded by timeout. A nil probe reports only the flag.
func NewHealth(probe DependencyProbe, timeout, interval time.Duration) (*Health, error) {
	if probe != nil && (timeout <= 0 || interval <= 0) {
		return nil, errors.New("readiness probe timing must be positive")
	}
	return &Health{probe: probe, timeout: timeout, interval: interval, now: time.Now}, nil
}

func (h *Health) Live() bool {
	return true
}

// Ready reports lifecycle readiness and current dependency health. While one
// caller refreshes an expired probe, concurrent callers observe the previous
// result instead of queueing behind the dependency.
func (h *Health) Ready() bool {
	if !h.ready.Load() {
		return false
	}
	if h.probe == nil {
		return true
	}
	h.mu.Lock()
	if h.probing || h.clock().Sub(h.checkedAt) < h.interval {
		healthy := h.healthy
		h.mu.Unlock()
		return healthy
	}
	h.probing = true
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	err := h.probe(ctx)
	cancel()

	h.mu.Lock()
	h.probing = false
	h.checkedAt = h.clock()
	h.healthy = err == nil
	h.mu.Unlock()
	return err == nil && h.ready.Load()
}

// SetReady publishes or withdraws lifecycle readiness. Publishing readiness
// follows successful startup dependency checks, so it seeds a healthy result
// that the next probe interval revalidates.
func (h *Health) SetReady(ready bool) {
	if ready {
		h.mu.Lock()
		h.healthy, h.checkedAt = true, h.clock()
		h.mu.Unlock()
	}
	h.ready.Store(ready)
}

func (h *Health) clock() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}
