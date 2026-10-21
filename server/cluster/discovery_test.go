// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryNodeValidateAndLiveness(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	ttl := 30 * time.Second
	node := DiscoveryNode{
		NodeID:           "node-a",
		AdvertiseAddress: "10.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(ttl),
	}
	require.NoError(t, node.Validate())
	require.True(t, node.IsLive(now), "live node reported expired at lease start")
	require.False(t, node.IsLive(node.ExpiresAt), "exclusive expiry treated as still live")

	invalid := []DiscoveryNode{
		{},
		{
			NodeID: "bad id", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 2, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now.Add(time.Second),
		},
		{
			NodeID: "node-a", AdvertiseAddress: "a", ServerVersion: "v",
			ProtocolMin: 1, ProtocolMax: 1, UpdatedAt: now, ExpiresAt: now,
		},
	}
	for index, candidate := range invalid {
		require.Error(t, candidate.Validate(), "case %d: expected validation error", index)
	}
}
