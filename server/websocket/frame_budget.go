// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"sync"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	// inboundFrameBurst and inboundFramesPerSecond bound the request frames
	// one Session (or, without a Session, one User) may send to this node,
	// whatever their action and however many connections carry them.
	// Legitimate clients send a few subscriptions at start-up and then
	// renewals, signals, and batched browser activity well below the
	// sustained rate.
	inboundFrameBurst      = 64
	inboundFramesPerSecond = 16
	// inboundRefusalsBeforeClose closes a connection that keeps sending after
	// its budget is exhausted instead of answering each refused frame forever.
	inboundRefusalsBeforeClose = 64
	// frameBudgetIdleRetention is how long a budget outlives its last
	// connection. After it a released budget would have refilled to the full
	// burst, so forgetting it changes nothing; before it, reconnecting does
	// not reset a spent budget.
	frameBudgetIdleRetention = time.Duration(inboundFrameBurst/inboundFramesPerSecond) * time.Second
)

// frameBudget is a token bucket over inbound request frames. The zero value
// starts with a full burst on first use. It is not safe for concurrent use.
type frameBudget struct {
	tokens      float64
	updated     time.Time
	initialized bool
}

// allow spends one token at now and reports whether the frame is admitted.
func (b *frameBudget) allow(now time.Time) bool {
	if !b.initialized {
		b.tokens, b.updated, b.initialized = inboundFrameBurst, now, true
	}
	if elapsed := now.Sub(b.updated); elapsed > 0 {
		b.tokens = min(float64(inboundFrameBurst), b.tokens+elapsed.Seconds()*inboundFramesPerSecond)
		b.updated = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// sharedFrameBudget is the budget of one Session or User on this node, shared
// by all of its connections. The Hub shard lock guards holders and releasedAt.
type sharedFrameBudget struct {
	mu         sync.Mutex
	bucket     frameBudget
	holders    int
	releasedAt time.Time
}

func (s *sharedFrameBudget) allow(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bucket.allow(now)
}

// frameBudgetKey names the budget a principal's connections share.
func frameBudgetKey(principal model.Principal) string {
	if !principal.SessionID.IsZero() {
		return "session:" + principal.SessionID.String()
	}
	return "user:" + principal.UserID.String()
}

// inboundFrameGate is one connection's view of its shared budget. It is
// owned by the read pump. Without a Hub it uses a private budget.
type inboundFrameGate struct {
	shared   *sharedFrameBudget
	refusals int
}

// allow spends one frame from the shared budget.
func (g *inboundFrameGate) allow(now time.Time) bool {
	if g.shared == nil {
		g.shared = &sharedFrameBudget{}
	}
	if g.shared.allow(now) {
		g.refusals = 0
		return true
	}
	g.refusals++
	return false
}

// exhausted reports whether consecutive refusals warrant closing the
// connection.
func (g *inboundFrameGate) exhausted() bool {
	return g.refusals >= inboundRefusalsBeforeClose
}
