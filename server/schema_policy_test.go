// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

type schemaPersistenceFake struct {
	migrations  int
	validations int
	validateErr error
}

func (f *schemaPersistenceFake) ApplyPendingMigrations(context.Context) (sqlstore.MigrationResult, error) {
	f.migrations++
	return sqlstore.MigrationResult{Applied: 1, SchemaVersion: 1}, nil
}

func (f *schemaPersistenceFake) ValidateSchema(context.Context) error {
	f.validations++
	return f.validateErr
}

func TestRequiredCurrentSchemaRefusesWithoutMigrating(t *testing.T) {
	t.Parallel()

	behind := &sqlstore.ErrSchemaBehind{Database: 0, Local: 1}
	persistence := &schemaPersistenceFake{validateErr: behind}
	_, err := prepareSchema(context.Background(), persistence, schemaRequireCurrent)
	require.ErrorAs(t, err, new(*sqlstore.ErrSchemaBehind), "prepareSchema()")
	require.Contains(t, err.Error(), "proctor migrate up", "prepareSchema()")
	require.Equal(t, 0, persistence.migrations, "required-current schema applied")

	persistence.validateErr = nil
	migration, err := prepareSchema(context.Background(), persistence, schemaRequireCurrent)
	require.NoError(t, err, "current schema")
	require.Nil(t, migration, "current schema")
	require.Equal(t, 0, persistence.migrations, "current schema")
}

func TestServingSchemaPolicyAppliesPendingMigrations(t *testing.T) {
	t.Parallel()

	persistence := &schemaPersistenceFake{}
	migration, err := prepareSchema(context.Background(), persistence, schemaMigrate)
	require.NoError(t, err, "serving schema")
	require.NotNil(t, migration, "serving schema")
	require.Equal(t, 1, migration.Applied, "serving schema")
	require.Equal(t, 1, persistence.migrations, "serving schema")
	require.Equal(t, 0, persistence.validations, "serving schema")
}
