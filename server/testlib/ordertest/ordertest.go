// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package ordertest asserts the few orderings that are part of an application
// contract, such as an audit beginning before the durable write it describes
// or a durable commit preceding its best-effort effects.
//
// Tests assert outcomes first: what was persisted, audited, and emitted. Use
// RequireInOrder only for the events whose relative order is the contract;
// every other recorded event, such as a lookup, may move freely.
package ordertest

import (
	"slices"

	"github.com/stretchr/testify/require"
)

// RequireInOrder asserts that every want event was recorded, in the given
// relative order. Recorded events between them are ignored.
func RequireInOrder(t require.TestingT, recorded []string, want ...string) {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}
	next := 0
	for position, event := range want {
		index := slices.Index(recorded[next:], event)
		require.GreaterOrEqual(t, index, 0, "event %q is not recorded after %v: %v", event, want[:position], recorded)
		next += index + 1
	}
}
