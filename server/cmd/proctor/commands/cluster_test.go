// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server"
)

func clusterStatusFixture() server.ClusterStatus {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return server.ClusterStatus{
		DatabaseTime: at,
		Schema:       server.ClusterSchema{DatabaseVersion: 1, ServerVersion: 1},
		ServingNodes: []server.ServingNode{
			{NodeID: "node-a", LeaseID: "lease-a", UpdatedAt: at.Add(-5 * time.Second), ExpiresAt: at.Add(15 * time.Second), Live: true},
			{NodeID: "node-b", LeaseID: "lease-b", UpdatedAt: at.Add(-time.Hour), ExpiresAt: at.Add(-time.Minute)},
		},
		Discovery: []server.DiscoveryNode{
			{NodeID: "node-a", AdvertiseAddress: "10.0.0.1:7946", ServerVersion: "1.0.0", ProtocolMin: 1, ProtocolMax: 2, UpdatedAt: at, ExpiresAt: at.Add(time.Minute), Live: true},
		},
	}
}

func TestClusterStatusPrintsLeasesDiscoveryAndSchema(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.clusterStatus = func(_ context.Context, path string) (server.ClusterStatus, error) {
		require.Equal(t, "/etc/proctor.json", path)
		return clusterStatusFixture(), nil
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"cluster", "status", "--config", "/etc/proctor.json"}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	require.Empty(t, stderr)
	require.Equal(t, "database schema version 1; server schema version 1; pending migrations 0\n"+
		"database time 2026-10-23T12:00:00Z\n"+
		"\n"+
		"Serving nodes\n"+
		"NODE    STATE    UPDATED               EXPIRES               LEASE\n"+
		"node-a  live     2026-10-23T11:59:55Z  2026-10-23T12:00:15Z  lease-a\n"+
		"node-b  expired  2026-10-23T11:00:00Z  2026-10-23T11:59:00Z  lease-b\n"+
		"\n"+
		"Cluster discovery\n"+
		"NODE    STATE  ADDRESS        VERSION  PROTOCOL  UPDATED               EXPIRES\n"+
		"node-a  live   10.0.0.1:7946  1.0.0    1-2       2026-10-23T12:00:00Z  2026-10-23T12:01:00Z\n", stdout)
}

func TestClusterStatusWritesJSON(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.clusterStatus = func(context.Context, string) (server.ClusterStatus, error) {
		return clusterStatusFixture(), nil
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"cluster", "status", "--json"}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded))
	require.Equal(t, "2026-10-23T12:00:00Z", decoded["database_time"])
	nodes, ok := decoded["serving_nodes"].([]any)
	require.True(t, ok, "serving_nodes is not a list")
	require.Len(t, nodes, 2)
	require.Equal(t, map[string]any{
		"node_id": "node-a", "lease_id": "lease-a", "live": true,
		"updated_at": "2026-10-23T11:59:55Z", "expires_at": "2026-10-23T12:00:15Z",
	}, nodes[0])
}

func TestClusterStatusReportsAnEmptySchema(t *testing.T) {
	t.Parallel()

	execute := testExecutors()
	execute.clusterStatus = func(context.Context, string) (server.ClusterStatus, error) {
		return server.ClusterStatus{Schema: server.ClusterSchema{ServerVersion: 1, PendingMigrations: 1}}, nil
	}

	code, stdout, stderr := executeForTest(context.Background(), []string{"cluster", "status"}, nil, execute)
	require.Equal(t, 0, code, "stderr=%q", stderr)
	require.Equal(t, "database schema version 0; server schema version 1; pending migrations 1\n\n"+
		"Serving nodes\n(none)\n\nCluster discovery\n(none)\n", stdout)
}
