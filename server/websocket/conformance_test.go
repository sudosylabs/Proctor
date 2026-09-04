// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
//   - deterministic connection pumps, liveness, commands, and validation
//   - ordered outbound delivery, backpressure disconnect, and close-once
//   - immutable final snapshots and Hub-owned replay retention
//   - stable close codes below
//
// Covered in //go:build integration app/websocket_integration_test.go:
//   - authenticated upgrade and unauthenticated 401
//   - subscription success, sequence monotonicity, local replay, resync_required
//   - session revocation and authorization-change connection close
//   - application ping/pong command
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
