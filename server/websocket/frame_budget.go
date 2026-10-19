// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import "time"

const (
	// inboundFrameBurst and inboundFramesPerSecond bound the request frames
	// one connection may send, whatever their action. Legitimate clients send
	// a few subscriptions at start-up and then renewals, signals, and batched
	// browser activity well below the sustained rate.
	inboundFrameBurst      = 64
	inboundFramesPerSecond = 16
	// inboundRefusalsBeforeClose closes a connection that keeps sending after
	// its budget is exhausted instead of answering each refused frame forever.
	inboundRefusalsBeforeClose = 64
)

// frameBudget is a per-connection token bucket over inbound request frames.
// It is owned by the read pump and is not safe for concurrent use. The zero
// value starts with a full burst on first use.
type frameBudget struct {
	tokens      float64
	updated     time.Time
	initialized bool
	refusals    int
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
		b.refusals = 0
		return true
	}
	b.refusals++
	return false
}

// exhausted reports whether consecutive refusals warrant closing the
// connection.
func (b *frameBudget) exhausted() bool {
	return b.refusals >= inboundRefusalsBeforeClose
}
