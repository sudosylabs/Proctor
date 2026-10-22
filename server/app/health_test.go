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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type readinessClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *readinessClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *readinessClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newProbedHealth(t *testing.T, probe DependencyProbe) (*Health, *readinessClock) {
	t.Helper()
	health, err := NewHealth(HealthDependencies{Probe: probe, Timeout: time.Second, Interval: time.Second})
	require.NoError(t, err)
	clock := &readinessClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	health.now = clock.Now
	return health, clock
}

func TestHealthReadinessReportsDependencyOutageAndRecovery(t *testing.T) {
	t.Parallel()

	var outage atomic.Bool
	var probes atomic.Int32
	health, clock := newProbedHealth(t, func(context.Context) error {
		probes.Add(1)
		if outage.Load() {
			return errors.New("database unavailable")
		}
		return nil
	})
	require.False(t, health.Ready(), "ready before lifecycle published readiness")
	health.SetReady(true)
	require.True(t, health.Ready(), "not ready after startup")

	outage.Store(true)
	clock.Advance(time.Second)
	require.False(t, health.Ready(), "ready during a dependency outage")
	outage.Store(false)
	require.False(t, health.Ready(), "recovered before the probe interval elapsed")
	clock.Advance(time.Second)
	require.True(t, health.Ready(), "not ready after the dependency recovered")
	require.Equal(t, int32(2), probes.Load(), "dependency probes")

	health.SetReady(false)
	require.False(t, health.Ready(), "ready after lifecycle withdrew readiness")
}

func TestHealthReadinessProbeIsBoundedAndNotQueued(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	var deadlineSet atomic.Bool
	health, clock := newProbedHealth(t, func(ctx context.Context) error {
		_, ok := ctx.Deadline()
		deadlineSet.Store(ok)
		close(started)
		<-release
		return errors.New("slow dependency")
	})
	health.SetReady(true)
	clock.Advance(time.Second)

	slow := make(chan bool, 1)
	go func() { slow <- health.Ready() }()
	<-started
	// A concurrent caller observes the previous result instead of waiting.
	require.True(t, health.Ready(), "concurrent caller did not observe the previous healthy result")
	close(release)
	require.False(t, <-slow, "failed probe reported ready")
	require.True(t, deadlineSet.Load(), "dependency probe context has no deadline")
}

func TestHealthWithoutProbeReportsLifecycleFlag(t *testing.T) {
	t.Parallel()

	health := &Health{}
	require.False(t, health.Ready(), "zero health is ready")
	health.SetReady(true)
	require.True(t, health.Ready(), "flag-only health ignored SetReady")
	_, err := NewHealth(HealthDependencies{Probe: func(context.Context) error { return nil }, Timeout: 0, Interval: time.Second})
	require.Error(t, err, "probe without a timeout was accepted")
}
