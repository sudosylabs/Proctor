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
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLClusterDiscoveryStore struct {
	*SQLStore
}

type clusterDiscoveryNodeRow struct {
	NodeID           string    `db:"node_id"`
	AdvertiseAddress string    `db:"advertise_address"`
	ServerVersion    string    `db:"server_version"`
	ProtocolMin      int       `db:"protocol_min"`
	ProtocolMax      int       `db:"protocol_max"`
	ExpiresAt        time.Time `db:"expires_at"`
	UpdatedAt        time.Time `db:"updated_at"`
}

func newSQLClusterDiscoveryStore(sqlStore *SQLStore) store.ClusterDiscoveryStore {
	return &SQLClusterDiscoveryStore{SQLStore: sqlStore}
}

func (s SQLClusterDiscoveryStore) Upsert(
	ctx context.Context,
	node *store.ClusterDiscoveryNode,
) error {
	prepared, err := prepareClusterDiscoveryNode(node)
	if err != nil {
		return err
	}
	query, args, err := s.getQueryBuilder().
		Insert("cluster_discovery_nodes").
		Columns(
			"node_id",
			"advertise_address",
			"server_version",
			"protocol_min",
			"protocol_max",
			"expires_at",
			"updated_at",
		).
		Values(
			prepared.NodeID,
			prepared.AdvertiseAddress,
			prepared.ServerVersion,
			prepared.ProtocolMin,
			prepared.ProtocolMax,
			model.TimeFromMillis(prepared.ExpiresAt),
			model.TimeFromMillis(prepared.UpdatedAt),
		).
		Suffix(`
ON CONFLICT (node_id) DO UPDATE SET
	advertise_address = EXCLUDED.advertise_address,
	server_version = EXCLUDED.server_version,
	protocol_min = EXCLUDED.protocol_min,
	protocol_max = EXCLUDED.protocol_max,
	expires_at = EXCLUDED.expires_at,
	updated_at = EXCLUDED.updated_at
`).
		ToSql()
	if err != nil {
		return fmt.Errorf("build cluster discovery upsert: %w", err)
	}
	if _, err := s.GetMaster().Exec(ctx, query, args...); err != nil {
		return translateError("cluster_discovery", "upsert", err)
	}
	return nil
}

func (s SQLClusterDiscoveryStore) ListLive(
	ctx context.Context,
	nowMillis int64,
) ([]*store.ClusterDiscoveryNode, error) {
	if nowMillis <= 0 {
		return nil, fmt.Errorf("cluster discovery now is invalid")
	}
	query, args, err := s.getQueryBuilder().
		Select(
			"node_id",
			"advertise_address",
			"server_version",
			"protocol_min",
			"protocol_max",
			"expires_at",
			"updated_at",
		).
		From("cluster_discovery_nodes").
		Where(sq.Gt{"expires_at": model.TimeFromMillis(nowMillis)}).
		OrderBy("node_id ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build cluster discovery list: %w", err)
	}
	var rows []clusterDiscoveryNodeRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, translateError("cluster_discovery", "list_live", err)
	}
	nodes := make([]*store.ClusterDiscoveryNode, 0, len(rows))
	for _, row := range rows {
		node := row.model()
		if err := node.Validate(); err != nil {
			return nil, fmt.Errorf("cluster discovery integrity: %w", err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (s SQLClusterDiscoveryStore) Delete(ctx context.Context, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(nodeID) > store.ClusterDiscoveryNodeIDMaxBytes {
		return fmt.Errorf("cluster discovery node_id is invalid")
	}
	query, args, err := s.getQueryBuilder().
		Delete("cluster_discovery_nodes").
		Where(sq.Eq{"node_id": nodeID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("build cluster discovery delete: %w", err)
	}
	if _, err := s.GetMaster().Exec(ctx, query, args...); err != nil {
		return translateError("cluster_discovery", "delete", err)
	}
	return nil
}

func (s SQLClusterDiscoveryStore) DeleteExpired(
	ctx context.Context,
	nowMillis int64,
) (int64, error) {
	if nowMillis <= 0 {
		return 0, fmt.Errorf("cluster discovery now is invalid")
	}
	query, args, err := s.getQueryBuilder().
		Delete("cluster_discovery_nodes").
		Where(sq.LtOrEq{"expires_at": model.TimeFromMillis(nowMillis)}).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build cluster discovery delete expired: %w", err)
	}
	result, err := s.GetMaster().Exec(ctx, query, args...)
	if err != nil {
		return 0, translateError("cluster_discovery", "delete_expired", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cluster discovery delete expired rows affected: %w", err)
	}
	return rows, nil
}

func prepareClusterDiscoveryNode(
	node *store.ClusterDiscoveryNode,
) (*store.ClusterDiscoveryNode, error) {
	if node == nil {
		return nil, fmt.Errorf("cluster discovery node is nil")
	}
	prepared := *node
	prepared.NodeID = strings.TrimSpace(prepared.NodeID)
	prepared.AdvertiseAddress = strings.TrimSpace(prepared.AdvertiseAddress)
	prepared.ServerVersion = strings.TrimSpace(prepared.ServerVersion)
	if err := prepared.Validate(); err != nil {
		return nil, err
	}
	return &prepared, nil
}

func (r clusterDiscoveryNodeRow) model() *store.ClusterDiscoveryNode {
	return &store.ClusterDiscoveryNode{
		NodeID:           r.NodeID,
		AdvertiseAddress: r.AdvertiseAddress,
		ServerVersion:    r.ServerVersion,
		ProtocolMin:      r.ProtocolMin,
		ProtocolMax:      r.ProtocolMax,
		ExpiresAt:        model.MillisFromTime(r.ExpiresAt),
		UpdatedAt:        model.MillisFromTime(r.UpdatedAt),
	}
}
