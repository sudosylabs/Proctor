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
	"fmt"

	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

// ErrServingNodeLive reports that a rollback found a serving node lease that
// is still live by the PostgreSQL clock.
var ErrServingNodeLive = sqlstore.ErrServingNodeLive

// MigrationRollbackPlan describes what reverting the newest Steps migrations
// would do, read without the migration lock.
type MigrationRollbackPlan struct {
	DatabaseVersion int
	// TargetVersion is the schema version after the rollback; zero is an
	// empty schema.
	TargetVersion int
	// Migrations are the migrations to revert, newest first.
	Migrations []RevertedMigration
	// LiveServingNodes names the nodes whose serving lease is live now.
	LiveServingNodes []string
}

// RevertedMigration names one migration a rollback reverts.
type RevertedMigration struct {
	Version int
	Name    string
}

// PlanMigrateDown reads, through a read-only session, which migrations
// reverting steps would undo and which serving nodes are live. It refuses a
// step count larger than the applied migrations.
func PlanMigrateDown(ctx context.Context, configPath string, steps int) (_ MigrationRollbackPlan, resultErr error) {
	if steps <= 0 {
		return MigrationRollbackPlan{}, errors.New("migration rollback steps must be greater than zero")
	}
	database, closeDatabase, err := openReadOnlyDatabase(ctx, configPath)
	if err != nil {
		return MigrationRollbackPlan{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, closeDatabase()) }()

	applied, err := database.AppliedMigrations(ctx)
	if err != nil {
		return MigrationRollbackPlan{}, err
	}
	if err := requireRevertible(applied, steps); err != nil {
		return MigrationRollbackPlan{}, err
	}
	plan := MigrationRollbackPlan{DatabaseVersion: applied[0].Version, LiveServingNodes: []string{}}
	for _, migration := range applied[:steps] {
		plan.Migrations = append(plan.Migrations, RevertedMigration{Version: migration.Version, Name: migration.Name})
	}
	if steps < len(applied) {
		plan.TargetVersion = applied[steps].Version
	}
	inspection, err := database.InspectCluster(ctx)
	if err != nil {
		return MigrationRollbackPlan{}, err
	}
	for _, lease := range inspection.ServingNodeLeases {
		if lease.ExpiresAt.After(inspection.DatabaseTime) {
			plan.LiveServingNodes = append(plan.LiveServingNodes, lease.NodeID)
		}
	}
	return plan, nil
}

// MigrateDown reverts the newest steps migrations under the migration lock
// and the exclusive serving node lease fence, refusing with
// ErrServingNodeLive while any node holds a live lease. It returns the
// resulting schema version. Reverting the baseline leaves an empty schema
// and drops every table with its data.
func MigrateDown(ctx context.Context, configPath string, steps int) (int, error) {
	migrator, closeMigrator, err := openMigrator(ctx, configPath)
	if err != nil {
		return 0, err
	}
	defer closeMigrator()

	applied, err := migrator.AppliedMigrations(ctx)
	if err != nil {
		return 0, err
	}
	if err := requireRevertible(applied, steps); err != nil {
		return 0, err
	}
	if _, err := migrator.Down(ctx, steps); err != nil {
		return 0, err
	}
	return migrator.SchemaVersion(ctx)
}

func requireRevertible(applied []sqlstore.AppliedMigration, steps int) error {
	if len(applied) == 0 {
		return errors.New("the database has no applied migrations to revert")
	}
	if steps > len(applied) {
		return fmt.Errorf("cannot revert %d migrations: only %d are applied", steps, len(applied))
	}
	return nil
}
