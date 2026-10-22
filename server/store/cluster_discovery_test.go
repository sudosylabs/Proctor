// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestClusterDiscoveryNodeValidateAndLiveness(t *testing.T) {
	t.Parallel()

	now := model.NowUTC()
	node := &store.ClusterDiscoveryNode{
		NodeID:           "node-a",
		AdvertiseAddress: "127.0.0.1:7946",
		ServerVersion:    "0.1.0",
		ProtocolMin:      1,
		ProtocolMax:      1,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(30 * time.Second),
	}
	require.NoError(t, node.Validate())
	require.True(t, node.IsLive(now), "expected live node")
	require.False(t, node.IsLive(node.ExpiresAt), "exclusive expiry treated as live")
	require.Error(t, (&store.ClusterDiscoveryNode{}).Validate(), "expected empty node validation error")
}
