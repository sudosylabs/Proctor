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
	"path"
	"slices"
	"strconv"

	"github.com/lib/pq"
	"github.com/mattermost/morph/models"

	"github.com/sudosylabs/proctor/server/migrations"
)

type ErrSchemaBehind struct {
	Database int
	Local    int
}

func (e *ErrSchemaBehind) Error() string {
	return fmt.Sprintf("database schema is behind: database=%d server=%d; run `proctor migrate up`", e.Database, e.Local)
}

type ErrSchemaAhead struct {
	Database int
	Local    int
}

func (e *ErrSchemaAhead) Error() string {
	return fmt.Sprintf("database schema is newer than this server: database=%d server=%d", e.Database, e.Local)
}

func LocalSchemaVersion() (int, error) {
	entries, err := migrations.Assets.ReadDir("postgres")
	if err != nil {
		return 0, fmt.Errorf("read embedded migrations: %w", err)
	}
	latest := 0
	for _, entry := range entries {
		match := models.Regex.FindStringSubmatch(entry.Name())
		if len(match) < 2 {
			return 0, fmt.Errorf("migration filename is invalid: %s", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return 0, fmt.Errorf("parse migration version from %s: %w", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	return latest, nil
}

// GetDBSchemaVersion returns the newest applied migration version, or 0 when
// the database has no migrations or no migration table.
func (ss *SQLStore) GetDBSchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := ss.GetMaster().Get(ctx, &version, "SELECT version FROM db_migrations ORDER BY version DESC LIMIT 1")
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == postgresUndefinedTable {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read database schema version: %w", err)
	}
	return version, nil
}

// PendingMigrationCount returns how many embedded forward migrations the
// database has not applied. It only reads: it neither takes the migration lock
// nor creates the migration bookkeeping table, so a database without that
// table has every embedded migration pending.
func (ss *SQLStore) PendingMigrationCount(ctx context.Context) (int, error) {
	var applied []string
	err := ss.GetMaster().Select(ctx, &applied, "SELECT name FROM db_migrations")
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == postgresUndefinedTable {
		applied, err = nil, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read applied migrations: %w", err)
	}
	names, err := migrationNames()
	if err != nil {
		return 0, fmt.Errorf("list embedded migrations: %w", err)
	}
	pending := 0
	for _, name := range names {
		match := models.Regex.FindStringSubmatch(name)
		if len(match) < 4 {
			return 0, fmt.Errorf("migration filename is invalid: %s", name)
		}
		if match[3] == string(models.Up) && !slices.Contains(applied, match[2]) {
			pending++
		}
	}
	return pending, nil
}

// GetLocalSchemaVersion returns the newest migration version embedded in this
// binary.
func (ss *SQLStore) GetLocalSchemaVersion() (int, error) {
	return LocalSchemaVersion()
}

// ValidateSchema compares the database and embedded schema versions, returning
// *ErrSchemaBehind or *ErrSchemaAhead when they differ.
func (ss *SQLStore) ValidateSchema(ctx context.Context) error {
	databaseVersion, err := ss.GetDBSchemaVersion(ctx)
	if err != nil {
		return err
	}
	localVersion, err := ss.GetLocalSchemaVersion()
	if err != nil {
		return err
	}
	switch {
	case databaseVersion < localVersion:
		return &ErrSchemaBehind{Database: databaseVersion, Local: localVersion}
	case databaseVersion > localVersion:
		return &ErrSchemaAhead{Database: databaseVersion, Local: localVersion}
	default:
		return nil
	}
}

func migrationNames() ([]string, error) {
	entries, err := migrations.Assets.ReadDir("postgres")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func migrationPath(name string) string {
	return path.Join("postgres", name)
}
