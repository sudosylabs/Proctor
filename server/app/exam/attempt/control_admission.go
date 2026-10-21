// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package attempt

import (
	"context"
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	controlWorkers              = 32
	controlUsers                = 4096
	controlBurst                = 40_000
	controlTokensPerMillisecond = 20

	// Live Participation renewal and security-coverage updates queue for a
	// worker instead of being refused: a refused renewal is indistinguishable
	// from a lost client, and enough consecutive refusals expire the lease,
	// producing Connection Loss evidence and a suspension for a well-behaved
	// candidate. The queue stays bounded in length and in wait time; a wait
	// longer than one renewal interval is superseded by the next renewal.
	liveControlWaiters = 4096
	liveControlWait    = model.AttemptParticipationRenewalInterval
)

// controlLane is the fixed shape of one per-node admission lane.
type controlLane struct {
	workers int
	// waiters bounds requests queued for a busy worker; zero refuses at once.
	waiters int
	wait    time.Duration
	// admitUntracked admits a new User whose allowance cannot be retained
	// because the bounded identity table is full of recently active Users,
	// instead of refusing them. Worker and queue bounds still apply.
	admitUntracked bool
}

var (
	deliveryControlLane = controlLane{workers: controlWorkers}
	liveControlLane     = controlLane{workers: controlWorkers, waiters: liveControlWaiters, wait: liveControlWait, admitUntracked: true}
)

type controlAllowance struct {
	tokens int64
	at     time.Time
}

// Each node bounds live renewal/security work separately from recovery/status
// work. Neither allowance is shared with detail intake or grants authority.
// Every bound here is per process: N nodes admit N times the concurrency, and
// each User's allowance is tracked independently on every node they reach.
// The zero value is the delivery (recovery/status) lane.
type controlAdmission struct {
	lane    controlLane
	mu      sync.Mutex
	slots   chan struct{}
	waiting int
	users   map[model.UserID]controlAllowance
}

func (a *controlAdmission) enter(ctx context.Context, user model.UserID, now time.Time) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !user.IsValid() {
		return nil, apperror.New("authentication.token_invalid")
	}
	a.mu.Lock()
	lane := a.lane
	if lane.workers < 1 {
		lane = deliveryControlLane
	}
	if a.slots == nil {
		a.slots = make(chan struct{}, lane.workers)
	}
	slots := a.slots
	if lane.waiters == 0 && len(slots) >= lane.workers {
		a.mu.Unlock()
		return nil, apperror.New("exam.delivery.control_rate_limited")
	}
	if !a.spend(user, now, lane) {
		a.mu.Unlock()
		return nil, apperror.New("exam.delivery.control_rate_limited")
	}
	select {
	case slots <- struct{}{}:
		a.mu.Unlock()
		return releaseControl(slots), nil
	default:
	}
	if a.waiting >= lane.waiters {
		a.refund(user)
		a.mu.Unlock()
		return nil, apperror.New("exam.delivery.control_rate_limited")
	}
	a.waiting++
	a.mu.Unlock()
	timer := time.NewTimer(lane.wait)
	defer timer.Stop()
	var err error
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = apperror.New("exam.delivery.control_rate_limited")
	}
	a.mu.Lock()
	a.waiting--
	if err != nil {
		a.refund(user)
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return releaseControl(slots), nil
}

func releaseControl(slots chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { <-slots }) }
}

// spend consumes one request from the User's allowance. The caller holds mu.
func (a *controlAdmission) spend(user model.UserID, now time.Time, lane controlLane) bool {
	if a.users == nil {
		a.users = make(map[model.UserID]controlAllowance)
	}
	value, exists := a.users[user]
	if !exists {
		if len(a.users) >= controlUsers {
			for id, old := range a.users {
				if now.Sub(old.at) >= time.Minute {
					delete(a.users, id)
					break
				}
			}
			if len(a.users) >= controlUsers {
				return lane.admitUntracked
			}
		}
		value = controlAllowance{tokens: controlBurst, at: now}
	}
	// A clock rollback cannot mint tokens or move the refill origin backwards.
	if now.After(value.at) {
		elapsed := min(now.Sub(value.at).Milliseconds(), int64(2000))
		value.tokens = min(int64(controlBurst), value.tokens+elapsed*controlTokensPerMillisecond)
		value.at = now
	}
	if value.tokens < 1000 {
		a.users[user] = value
		return false
	}
	value.tokens -= 1000
	a.users[user] = value
	return true
}

// refund returns a request that never reached a worker, so queue timeout or
// cancellation does not cost the User allowance. The caller holds mu.
func (a *controlAdmission) refund(user model.UserID) {
	if value, exists := a.users[user]; exists {
		value.tokens = min(int64(controlBurst), value.tokens+1000)
		a.users[user] = value
	}
}

func (s *Service) enterControl(ctx context.Context, invocation invocation.Invocation, live bool) (func(), error) {
	budget := &s.deliveryControls
	if live {
		budget = &s.liveControls
	}
	return budget.enter(ctx, invocation.Principal().UserID, s.deps.Now())
}
