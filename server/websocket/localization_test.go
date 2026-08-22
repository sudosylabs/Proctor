// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"strings"
	"testing"
)

func TestBoundedCloseReasonFallsBackWithinProtocolLimit(t *testing.T) {
	t.Parallel()
	fallback := websocketCloseMessages["server_shutdown"].fallback
	if got := boundedCloseReason(strings.Repeat("x", maximumCloseReasonBytes+1), fallback); got != fallback {
		t.Fatalf("bounded close reason = %q, want fallback %q", got, fallback)
	}
	translated := strings.Repeat("x", maximumCloseReasonBytes)
	if got := boundedCloseReason(translated, fallback); got != translated {
		t.Fatalf("valid close reason unexpectedly changed")
	}
}
