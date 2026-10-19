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
	health, err := NewHealth(probe, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
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
	if health.Ready() {
		t.Fatal("ready before lifecycle published readiness")
	}
	health.SetReady(true)
	if !health.Ready() {
		t.Fatal("not ready after startup")
	}

	outage.Store(true)
	clock.Advance(time.Second)
	if health.Ready() {
		t.Fatal("ready during a dependency outage")
	}
	outage.Store(false)
	if health.Ready() {
		t.Fatal("recovered before the probe interval elapsed")
	}
	clock.Advance(time.Second)
	if !health.Ready() {
		t.Fatal("not ready after the dependency recovered")
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("dependency probes = %d, want one per elapsed interval (2)", got)
	}

	health.SetReady(false)
	if health.Ready() {
		t.Fatal("ready after lifecycle withdrew readiness")
	}
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
	if !health.Ready() {
		t.Fatal("concurrent caller did not observe the previous healthy result")
	}
	close(release)
	if <-slow {
		t.Fatal("failed probe reported ready")
	}
	if !deadlineSet.Load() {
		t.Fatal("dependency probe context has no deadline")
	}
}

func TestHealthWithoutProbeReportsLifecycleFlag(t *testing.T) {
	t.Parallel()

	health := &Health{}
	if health.Ready() {
		t.Fatal("zero health is ready")
	}
	health.SetReady(true)
	if !health.Ready() {
		t.Fatal("flag-only health ignored SetReady")
	}
	if _, err := NewHealth(func(context.Context) error { return nil }, 0, time.Second); err == nil {
		t.Fatal("probe without a timeout was accepted")
	}
}
