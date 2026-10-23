// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// ErrServingNodeLive reports that a deliberate rollback found a serving node
// lease that is still live by the PostgreSQL clock.
var ErrServingNodeLive = errors.New("a serving node holds a live lease; stop every node and wait for its lease to expire")

// AppliedMigration names one migration recorded as applied.
type AppliedMigration struct {
	Version int    `db:"version"`
	Name    string `db:"name"`
}

// AppliedMigrations lists the applied migrations, newest first. It only
// reads: a database without the migration bookkeeping table has none.
func (ss *SQLStore) AppliedMigrations(ctx context.Context) ([]AppliedMigration, error) {
	var applied []AppliedMigration
	err := ss.GetMaster().Select(ctx, &applied, `SELECT version, name FROM db_migrations ORDER BY version DESC`)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == postgresUndefinedTable {
		return []AppliedMigration{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return applied, nil
}

// AppliedMigrations lists the applied migrations while the Migrator holds
// the migration lock, newest first.
func (m *Migrator) AppliedMigrations(ctx context.Context) ([]AppliedMigration, error) {
	return m.store.AppliedMigrations(ctx)
}

// Down reverts the newest steps applied migrations. It is deliberate operator
// tooling, never a startup action. Besides the migration lock the Migrator
// already holds, it holds the serving node lease fence exclusively for the
// whole rollback, so no node can claim or renew a lease meanwhile, and it
// refuses with ErrServingNodeLive while any lease is live.
//
// The fence is taken on a separate pool, and the lease check runs outside
// the fence transaction, so that transaction holds no table lock that the
// rollback's DROP statements would wait on.
func (m *Migrator) Down(ctx context.Context, steps int) (_ int, resultErr error) {
	if steps <= 0 {
		return 0, errors.New("migration rollback steps must be greater than zero")
	}
	fence, err := New(ctx, m.store.settings)
	if err != nil {
		return 0, fmt.Errorf("open serving node lease fence: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, fence.Close()) }()

	return runSQLTransaction(ctx, fence.GetMaster().Begin, "migration rollback fence", func(ctx context.Context, tx *sqlxTxWrapper) (int, error) {
		if err := lockServingNodeLeaseFence(ctx, tx); err != nil {
			return 0, err
		}
		var live bool
		err := fence.GetMaster().Get(ctx, &live, `SELECT EXISTS(SELECT 1 FROM serving_node_leases WHERE expires_at > clock_timestamp())`)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && string(pqErr.Code) == postgresUndefinedTable {
			live, err = false, nil
		}
		if err != nil {
			return 0, fmt.Errorf("check live serving nodes: %w", err)
		}
		if live {
			return 0, ErrServingNodeLive
		}
		reverted, err := m.engine.ApplyDown(steps)
		if err != nil {
			return reverted, fmt.Errorf("roll back database migrations: %w", err)
		}
		return reverted, nil
	})
}
