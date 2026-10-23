//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestInspectClusterListsLiveAndExpiredRowsWithoutWriting(t *testing.T) {
	StoreTest(t, func(t *testing.T, persistence store.Store) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sqlStore := persistence.(*SQLStore)
		suffix := model.NewID()[:10]
		liveNode, expiredNode := "inspect-live-"+suffix, "inspect-expired-"+suffix
		liveLease, err := sqlStore.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: liveNode, LeaseID: model.NewID(), Lifetime: time.Minute})
		require.NoError(t, err)
		_, err = sqlStore.GetMaster().Exec(ctx, `INSERT INTO serving_node_leases (node_id, lease_id, updated_at, expires_at)
			VALUES (?, ?, clock_timestamp() - interval '2 minutes', clock_timestamp() - interval '1 minute')`, expiredNode, model.NewID())
		require.NoError(t, err)
		now := model.NowUTC()
		require.NoError(t, sqlStore.ClusterDiscovery().Upsert(ctx, &store.ClusterDiscoveryNode{
			NodeID: liveNode, AdvertiseAddress: "10.0.0.1:7946", ServerVersion: "test", ProtocolMin: 1, ProtocolMax: 1,
			UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
		}))
		t.Cleanup(func() {
			_, _ = sqlStore.GetMaster().Exec(context.Background(), `DELETE FROM serving_node_leases WHERE node_id IN (?, ?)`, liveNode, expiredNode)
			_, _ = sqlStore.GetMaster().Exec(context.Background(), `DELETE FROM cluster_discovery_nodes WHERE node_id = ?`, liveNode)
		})

		settings := testSettings(t)
		settings.ReadOnly = true
		inspector, err := New(ctx, settings)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, inspector.Close()) })

		inspection, err := inspector.InspectCluster(ctx)
		require.NoError(t, err)
		leases := map[string]*store.ServingNodeLease{}
		for _, lease := range inspection.ServingNodeLeases {
			leases[lease.NodeID] = lease
		}
		require.Contains(t, leases, liveNode)
		require.Equal(t, liveLease.LeaseID, leases[liveNode].LeaseID)
		require.True(t, leases[liveNode].ExpiresAt.After(inspection.DatabaseTime), "live lease reported expired")
		require.Contains(t, leases, expiredNode)
		require.False(t, leases[expiredNode].ExpiresAt.After(inspection.DatabaseTime), "expired lease reported live")
		discovered := false
		for _, node := range inspection.DiscoveryNodes {
			discovered = discovered || (node.NodeID == liveNode && node.AdvertiseAddress == "10.0.0.1:7946")
		}
		require.True(t, discovered, "discovery row missing from inspection")

		_, err = inspector.GetMaster().Exec(ctx, `DELETE FROM serving_node_leases WHERE node_id = ?`, liveNode)
		var pqErr *pq.Error
		require.ErrorAs(t, err, &pqErr, "a read-only store accepted a write")
		require.Equal(t, "25006", string(pqErr.Code), "read_only_sql_transaction")
	})
}
