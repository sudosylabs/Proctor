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

func (s *SQLStore) GetDBSchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.GetMaster().Get(ctx, &version, "SELECT version FROM db_migrations ORDER BY version DESC LIMIT 1")
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

func (s *SQLStore) GetLocalSchemaVersion() (int, error) {
	return LocalSchemaVersion()
}

func (s *SQLStore) ValidateSchema(ctx context.Context) error {
	databaseVersion, err := s.GetDBSchemaVersion(ctx)
	if err != nil {
		return err
	}
	localVersion, err := s.GetLocalSchemaVersion()
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
