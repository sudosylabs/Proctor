// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
	"time"
)

// ClusterStatus is a read-only operator snapshot of the installation's
// serving nodes, cluster discovery rows, and schema. Its JSON field names are
// part of the public CLI contract.
type ClusterStatus struct {
	// DatabaseTime is the PostgreSQL clock that decides liveness.
	DatabaseTime time.Time       `json:"database_time"`
	Schema       ClusterSchema   `json:"schema"`
	ServingNodes []ServingNode   `json:"serving_nodes"`
	Discovery    []DiscoveryNode `json:"discovery_nodes"`
	// Truncated reports that a list stopped at its row bound.
	Truncated bool `json:"truncated"`
}

// ClusterSchema is MigrationStatus as the cluster status reports it.
type ClusterSchema struct {
	DatabaseVersion   int `json:"database_version"`
	ServerVersion     int `json:"server_version"`
	PendingMigrations int `json:"pending_migrations"`
}

// ServingNode is one serving node lease. A node serves only while its lease
// is live by the database clock.
type ServingNode struct {
	NodeID    string    `json:"node_id"`
	LeaseID   string    `json:"lease_id"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Live      bool      `json:"live"`
}

// DiscoveryNode is one disposable cluster discovery row.
type DiscoveryNode struct {
	NodeID           string    `json:"node_id"`
	AdvertiseAddress string    `json:"advertise_address"`
	ServerVersion    string    `json:"server_version"`
	ProtocolMin      int       `json:"protocol_min"`
	ProtocolMax      int       `json:"protocol_max"`
	UpdatedAt        time.Time `json:"updated_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	Live             bool      `json:"live"`
}

// InspectCluster reports serving node leases, cluster discovery rows (expired
// ones included), and the schema version through a read-only database
// session. It never migrates, takes the migration lock or any advisory lock,
// or writes, so it is safe beside running nodes. An empty schema reports no
// nodes.
func InspectCluster(ctx context.Context, configPath string) (_ ClusterStatus, resultErr error) {
	database, closeDatabase, err := openReadOnlyDatabase(ctx, configPath)
	if err != nil {
		return ClusterStatus{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeDatabase()) }()

	schema, err := readMigrationStatus(ctx, database)
	if err != nil {
		return ClusterStatus{}, err
	}
	status := ClusterStatus{
		Schema: ClusterSchema{
			DatabaseVersion:   schema.DatabaseVersion,
			ServerVersion:     schema.ServerVersion,
			PendingMigrations: schema.PendingMigrations,
		},
		ServingNodes: []ServingNode{},
		Discovery:    []DiscoveryNode{},
	}
	if schema.DatabaseVersion == 0 {
		return status, nil
	}
	inspection, err := database.InspectCluster(ctx)
	if err != nil {
		return ClusterStatus{}, err
	}
	status.DatabaseTime = inspection.DatabaseTime
	status.Truncated = inspection.ServingNodeLeasesTruncated || inspection.DiscoveryNodesTruncated
	for _, lease := range inspection.ServingNodeLeases {
		status.ServingNodes = append(status.ServingNodes, ServingNode{
			NodeID: lease.NodeID, LeaseID: lease.LeaseID, UpdatedAt: lease.UpdatedAt, ExpiresAt: lease.ExpiresAt,
			Live: lease.ExpiresAt.After(inspection.DatabaseTime),
		})
	}
	for _, node := range inspection.DiscoveryNodes {
		status.Discovery = append(status.Discovery, DiscoveryNode{
			NodeID: node.NodeID, AdvertiseAddress: node.AdvertiseAddress, ServerVersion: node.ServerVersion,
			ProtocolMin: node.ProtocolMin, ProtocolMax: node.ProtocolMax,
			UpdatedAt: node.UpdatedAt, ExpiresAt: node.ExpiresAt, Live: node.IsLive(inspection.DatabaseTime),
		})
	}
	return status, nil
}
