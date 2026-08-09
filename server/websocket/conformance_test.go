// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket_test

import (
	"testing"

	"github.com/sudosylabs/proctor/server/websocket"
)

// Conformance ownership map for WebSocket lifecycle and protocol behavior.
//
// Covered in this package (unit):
//   - construction is inert; Start/Close ownership (lifecycle_test.go)
//   - origin allow/deny and missing-origin bearer policy (lifecycle_test.go)
//   - wire event/subscription validation (protocol_test.go)
//   - stable close codes below
//
// Covered in //go:build integration app/websocket_integration_test.go:
//   - authenticated upgrade and unauthenticated 401
//   - subscription success, sequence monotonicity, local replay, resync_required
//   - session revocation and authorization-change connection close
//   - application ping/pong command
//
// Backpressure disconnect remains implementation-stable via CloseBackpressure
// when the outbound queue is full; a dedicated unit harness for a full send
// queue requires a deeper connection seam and is not yet extracted.
func TestConformanceCloseCodesRemainStable(t *testing.T) {
	t.Parallel()

	codes := map[string]int{
		"server":                websocket.CloseServer,
		"session_revoked":       websocket.CloseSessionRevoked,
		"backpressure":          websocket.CloseBackpressure,
		"authorization_changed": websocket.CloseAuthorizationChanged,
		"connection_limit":      websocket.CloseLimit,
	}
	want := map[string]int{
		"server":                4000,
		"session_revoked":       4001,
		"backpressure":          4002,
		"authorization_changed": 4003,
		"connection_limit":      4004,
	}
	for name, code := range codes {
		if code != want[name] {
			t.Fatalf("%s close code = %d, want %d", name, code, want[name])
		}
	}
}
