// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ClusterInspectionMaximumRows bounds each list a cluster inspection returns.
const ClusterInspectionMaximumRows = 500

// ClusterInspection is one consistent, read-only snapshot of the serving
// fence and the cluster discovery rows, with expired rows included, taken at
// one PostgreSQL instant. It is operator host tooling, not an application
// Store contract: nothing in the application reads it.
type ClusterInspection struct {
	// DatabaseTime is the PostgreSQL clock that decides which rows are live.
	DatabaseTime      time.Time
	ServingNodeLeases []*store.ServingNodeLease
	// ServingNodeLeasesTruncated reports rows beyond ClusterInspectionMaximumRows.
	ServingNodeLeasesTruncated bool
	DiscoveryNodes             []*store.ClusterDiscoveryNode
	// DiscoveryNodesTruncated reports rows beyond ClusterInspectionMaximumRows.
	DiscoveryNodesTruncated bool
}

// InspectCluster reads every serving node lease and cluster discovery row,
// ordered by node ID, inside one REPEATABLE READ, READ ONLY transaction. It
// takes no advisory lock, so it neither waits for nor delays a serving node.
func (ss *SQLStore) InspectCluster(ctx context.Context) (*ClusterInspection, error) {
	return executeSQLTransaction(ctx, ss.GetMaster().Begin, sqlTransactionPolicy[*ClusterInspection]{
		beginError: func(err error) error { return fmt.Errorf("begin cluster inspection: %w", err) },
	}, func(ctx context.Context, tx *sqlxTxWrapper) (*ClusterInspection, error) {
		if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY`); err != nil {
			return nil, fmt.Errorf("begin cluster inspection snapshot: %w", err)
		}
		inspection := &ClusterInspection{}
		if err := tx.Get(ctx, &inspection.DatabaseTime, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read cluster inspection time: %w", err)
		}
		inspection.DatabaseTime = model.TimeUTC(inspection.DatabaseTime)

		var leases []servingNodeLeaseRow
		if err := tx.Select(ctx, &leases, `
			SELECT node_id, lease_id, updated_at, expires_at FROM serving_node_leases
			ORDER BY node_id LIMIT ?`, ClusterInspectionMaximumRows+1); err != nil {
			return nil, fmt.Errorf("list serving node leases: %w", err)
		}
		if len(leases) > ClusterInspectionMaximumRows {
			leases, inspection.ServingNodeLeasesTruncated = leases[:ClusterInspectionMaximumRows], true
		}
		inspection.ServingNodeLeases = make([]*store.ServingNodeLease, 0, len(leases))
		for _, row := range leases {
			lease, err := servingNodeLeaseModel(row)
			if err != nil {
				return nil, err
			}
			inspection.ServingNodeLeases = append(inspection.ServingNodeLeases, lease)
		}

		var nodes []clusterDiscoveryNodeRow
		if err := tx.Select(ctx, &nodes, `
			SELECT node_id, advertise_address, server_version, protocol_min, protocol_max, expires_at, updated_at
			FROM cluster_discovery_nodes ORDER BY node_id LIMIT ?`, ClusterInspectionMaximumRows+1); err != nil {
			return nil, fmt.Errorf("list cluster discovery nodes: %w", err)
		}
		if len(nodes) > ClusterInspectionMaximumRows {
			nodes, inspection.DiscoveryNodesTruncated = nodes[:ClusterInspectionMaximumRows], true
		}
		inspection.DiscoveryNodes = make([]*store.ClusterDiscoveryNode, 0, len(nodes))
		for _, row := range nodes {
			node := row.model()
			node.ExpiresAt, node.UpdatedAt = model.TimeUTC(node.ExpiresAt), model.TimeUTC(node.UpdatedAt)
			if err := node.Validate(); err != nil {
				return nil, fmt.Errorf("cluster discovery integrity: %w", err)
			}
			inspection.DiscoveryNodes = append(inspection.DiscoveryNodes, node)
		}
		return inspection, nil
	})
}
