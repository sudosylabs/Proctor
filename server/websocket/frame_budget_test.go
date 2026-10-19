// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"testing"
	"time"
)

func TestFrameBudgetAdmitsBurstThenRefillsAtSustainedRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 19, 9, 0, 0, 0, time.UTC)
	var budget frameBudget
	for index := range inboundFrameBurst {
		if !budget.allow(now) {
			t.Fatalf("frame %d of the burst refused", index)
		}
	}
	if budget.allow(now) {
		t.Fatal("frame beyond the burst admitted")
	}
	now = now.Add(time.Second)
	for index := range inboundFramesPerSecond {
		if !budget.allow(now) {
			t.Fatalf("refilled frame %d refused", index)
		}
	}
	if budget.allow(now) {
		t.Fatal("frame beyond one second of refill admitted")
	}
	if budget.exhausted() {
		t.Fatal("budget closed after a single refusal streak frame")
	}
	now = now.Add(time.Hour)
	for range inboundFrameBurst {
		budget.allow(now)
	}
	if budget.allow(now) {
		t.Fatal("refill exceeded the burst ceiling")
	}
}

// TestConnectionRuntimeRateLimitsEveryInboundFrame floods subscribe frames,
// which otherwise reach application authorization one by one. Only the burst
// reaches the application; refused frames receive a retryable error and a
// sustained flood closes the connection with the rate-limit close code.
func TestConnectionRuntimeRateLimitsEveryInboundFrame(t *testing.T) {
	t.Parallel()

	clock := newRuntimeTestClock(time.Date(2026, time.October, 19, 9, 0, 0, 0, time.UTC))
	socket := newInboundTestSocket()
	application := &inboundTestApplication{}
	runtime := newInboundRuntime(application, socket, clock)
	runtime.send = make(chan outboundMessage, inboundFrameBurst+inboundRefusalsBeforeClose+8)
	requests := make([]Request, inboundFrameBurst+inboundRefusalsBeforeClose)
	for index := range requests {
		requests[index] = *requestWithData(t, int64(index+1), "subscribe", validInboundSubscription())
	}
	go func() {
		for _, request := range requests {
			select {
			case socket.reads <- inboundReadResult{request: request}:
			case <-socket.closed:
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.readPump(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read pump did not stop after a sustained flood")
	}

	application.mu.Lock()
	authorized := len(application.authorizations)
	application.mu.Unlock()
	if authorized != inboundFrameBurst {
		t.Fatalf("authorized subscriptions = %d, want only the %d-frame burst", authorized, inboundFrameBurst)
	}
	limited := 0
	for len(runtime.send) > 0 {
		message := <-runtime.send
		if response := message.response; response != nil && response.Error != nil && response.Error.Code == "websocket.rate_limited" {
			if response.Error.RetryAfterSeconds != 1 {
				t.Fatalf("rate-limited response = %#v", response.Error)
			}
			limited++
		}
	}
	if limited != inboundRefusalsBeforeClose-1 {
		t.Fatalf("rate-limited responses = %d, want %d", limited, inboundRefusalsBeforeClose-1)
	}
	socket.mu.Lock()
	closeCode, closeReason := socket.closeCode, socket.closeReason
	socket.mu.Unlock()
	if closeCode != CloseRateLimited || closeReason != "too many requests" {
		t.Fatalf("close = %d %q, want %d", closeCode, closeReason, CloseRateLimited)
	}
}
