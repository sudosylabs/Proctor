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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMigrationRollbackRefusesWhileAServingNodeLeaseIsLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	migrator, err := NewMigrator(ctx, testSettings(t))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, migrator.Close()) })
	require.NoError(t, migrator.Up())

	applied, err := migrator.store.AppliedMigrations(ctx)
	require.NoError(t, err)
	require.Equal(t, []AppliedMigration{{Version: 1, Name: "baseline"}}, applied)

	nodeID := "rollback-" + model.NewID()[:10]
	_, err = migrator.store.ServingNodeLease().Upsert(ctx, &store.ServingNodeLeaseClaim{NodeID: nodeID, LeaseID: model.NewID(), Lifetime: time.Minute})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.store.GetMaster().Exec(context.Background(), `DELETE FROM serving_node_leases WHERE node_id = ?`, nodeID)
	})

	reverted, err := migrator.Down(ctx, 1)
	require.ErrorIs(t, err, ErrServingNodeLive)
	require.Zero(t, reverted)
	version, err := migrator.SchemaVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, version, "a refused rollback changed the schema")

	_, err = migrator.Down(ctx, 0)
	require.EqualError(t, err, "migration rollback steps must be greater than zero")
}
