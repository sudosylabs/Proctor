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

	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

// openReadOnlyDatabase opens the configured database for operator
// inspection. Every session it opens refuses writes at the PostgreSQL level,
// and it never migrates, takes the migration lock, or creates a table. The
// returned closer releases the database and the configuration.
func openReadOnlyDatabase(ctx context.Context, configPath string) (*sqlstore.SQLStore, func() error, error) {
	configuration, err := openConfiguration(ctx, configPath)
	if err != nil {
		return nil, nil, err
	}
	settings := sqlstore.SettingsFromConfig(configuration.Get().Database)
	settings.ReadOnly = true
	database, err := sqlstore.New(ctx, settings)
	if err != nil {
		return nil, nil, errors.Join(err, configuration.Close())
	}
	return database, func() error {
		return errors.Join(database.Close(), configuration.Close())
	}, nil
}

// readMigrationStatus reads the schema state through an already-open store.
func readMigrationStatus(ctx context.Context, database *sqlstore.SQLStore) (MigrationStatus, error) {
	current, err := database.GetDBSchemaVersion(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	local, err := sqlstore.LocalSchemaVersion()
	if err != nil {
		return MigrationStatus{}, err
	}
	pending, err := database.PendingMigrationCount(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	return MigrationStatus{
		DatabaseVersion:   current,
		ServerVersion:     local,
		PendingMigrations: pending,
	}, nil
}
