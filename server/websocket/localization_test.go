// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedCloseReasonFallsBackWithinProtocolLimit(t *testing.T) {
	t.Parallel()
	fallback := websocketCloseMessages["server_shutdown"].fallback
	require.Equal(t, fallback, boundedCloseReason(strings.Repeat("x", maximumCloseReasonBytes+1), fallback), "bounded close reason")
	translated := strings.Repeat("x", maximumCloseReasonBytes)
	require.Equal(t, translated, boundedCloseReason(translated, fallback), "valid close reason unexpectedly changed")
}
