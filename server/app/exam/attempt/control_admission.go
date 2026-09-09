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

	"github.com/sudosylabs/proctor/server/model"
)

const (
	controlWorkers              = 32
	controlUsers                = 4096
	controlBurst                = 40_000
	controlTokensPerMillisecond = 20
)

type controlAllowance struct {
	tokens int64
	at     time.Time
}

// Each node bounds live renewal/security work separately from recovery/status
// work. Neither allowance is shared with detail intake or grants authority.
type controlAdmission struct {
	mu     sync.Mutex
	active int
	users  map[model.UserID]controlAllowance
}

func (a *controlAdmission) enter(ctx context.Context, user model.UserID, now time.Time) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !user.IsValid() {
		return nil, &Fault{Code: "authentication.invalid_token"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active >= controlWorkers {
		return nil, &Fault{Code: "exam.delivery.control_rate_limited"}
	}
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
				return nil, &Fault{Code: "exam.delivery.control_rate_limited"}
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
		return nil, &Fault{Code: "exam.delivery.control_rate_limited"}
	}
	value.tokens -= 1000
	a.users[user] = value
	a.active++
	return func() { a.mu.Lock(); a.active--; a.mu.Unlock() }, nil
}

func (service *Service) enterControl(ctx context.Context, call Call, live bool) (func(), error) {
	budget := &service.deliveryControls
	if live {
		budget = &service.liveControls
	}
	return budget.enter(ctx, call.principal.UserID, service.deps.Now())
}
