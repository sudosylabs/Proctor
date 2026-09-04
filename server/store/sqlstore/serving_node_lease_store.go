// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const servingNodeLeaseFenceKey = "administrator_recovery:serving_nodes"

type SQLServingNodeLeaseStore struct{ *SQLStore }

type servingNodeLeaseRow struct {
	NodeID    string    `db:"node_id"`
	LeaseID   string    `db:"lease_id"`
	UpdatedAt time.Time `db:"updated_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

func newSQLServingNodeLeaseStore(sqlStore *SQLStore) store.ServingNodeLeaseStore {
	return &SQLServingNodeLeaseStore{SQLStore: sqlStore}
}

func (s SQLServingNodeLeaseStore) Upsert(ctx context.Context, claim *store.ServingNodeLeaseClaim) (*store.ServingNodeLease, error) {
	if claim == nil {
		return nil, store.NewErrInvalidInput("serving_node_lease", "upsert", nil)
	}
	nodeID, leaseID, lifetime := strings.TrimSpace(claim.NodeID), strings.TrimSpace(claim.LeaseID), claim.Lifetime
	if !validServingNodeLeaseInput(nodeID, leaseID, lifetime) {
		return nil, store.NewErrInvalidInput("serving_node_lease", "upsert", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "upsert serving node lease", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ServingNodeLease, error) {
		if err := lockServingNodeLeaseFence(ctx, tx); err != nil {
			return nil, err
		}
		var databaseAt time.Time
		if err := tx.Get(ctx, &databaseAt, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read serving node lease time: %w", err)
		}
		var current servingNodeLeaseRow
		err := tx.Get(ctx, &current, `SELECT node_id, lease_id, updated_at, expires_at FROM serving_node_leases WHERE node_id=$1`, nodeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read serving node lease: %w", err)
		}
		if err == nil && current.LeaseID == leaseID && !current.ExpiresAt.After(databaseAt) {
			return nil, store.NewErrConflict("serving_node_lease", "lease_expired", nil)
		}
		if err == nil && current.LeaseID != leaseID && current.ExpiresAt.After(databaseAt) {
			return nil, store.NewErrConflict("serving_node_lease", "node_id_live", nil)
		}
		expiresAt := databaseAt.Add(lifetime)
		var row servingNodeLeaseRow
		if err := tx.Get(ctx, &row, `
			INSERT INTO serving_node_leases (node_id, lease_id, updated_at, expires_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (node_id) DO UPDATE
			SET lease_id = EXCLUDED.lease_id, updated_at = EXCLUDED.updated_at, expires_at = EXCLUDED.expires_at
			RETURNING node_id, lease_id, updated_at, expires_at`, nodeID, leaseID, databaseAt, expiresAt); err != nil {
			return nil, fmt.Errorf("upsert serving node lease: %w", translateError("serving_node_lease", nodeID, err))
		}
		return servingNodeLeaseModel(row)
	})
}

func (s SQLServingNodeLeaseStore) Delete(ctx context.Context, nodeID, leaseID string) error {
	nodeID, leaseID = strings.TrimSpace(nodeID), strings.TrimSpace(leaseID)
	if !validServingNodeLeaseIdentity(nodeID, leaseID) {
		return store.NewErrInvalidInput("serving_node_lease", "node_id", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "delete serving node lease", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		if err := lockServingNodeLeaseFence(ctx, tx); err != nil {
			return struct{}{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM serving_node_leases WHERE node_id=$1 AND lease_id=$2`, nodeID, leaseID); err != nil {
			return struct{}{}, fmt.Errorf("delete serving node lease: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

func lockServingNodeLeaseFence(ctx context.Context, tx *sqlxTxWrapper) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, servingNodeLeaseFenceKey); err != nil {
		return fmt.Errorf("lock serving node lease fence: %w", err)
	}
	return nil
}

func validServingNodeLeaseInput(nodeID, leaseID string, lifetime time.Duration) bool {
	return validServingNodeLeaseIdentity(nodeID, leaseID) &&
		lifetime >= store.ServingNodeLeaseMinimumLifetime && lifetime <= store.ServingNodeLeaseMaximumLifetime &&
		lifetime%time.Millisecond == 0
}

func validServingNodeLeaseIdentity(nodeID, leaseID string) bool {
	return nodeID != "" && len(nodeID) <= store.ClusterDiscoveryNodeIDMaxBytes && model.IsValidId(leaseID)
}

func servingNodeLeaseModel(row servingNodeLeaseRow) (*store.ServingNodeLease, error) {
	lease := &store.ServingNodeLease{NodeID: row.NodeID, LeaseID: row.LeaseID, UpdatedAt: model.TimeUTC(row.UpdatedAt), ExpiresAt: model.TimeUTC(row.ExpiresAt)}
	if err := lease.Validate(); err != nil {
		return nil, fmt.Errorf("serving node lease persisted state: %w", err)
	}
	return lease, nil
}

var _ store.ServingNodeLeaseStore = (*SQLServingNodeLeaseStore)(nil)
