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
	"strings"
	"testing"

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
	if !errors.As(err, new(*sqlstore.ErrSchemaBehind)) || !strings.Contains(err.Error(), "proctor migrate up") {
		t.Fatalf("prepareSchema() = %v, want a behind-schema refusal naming the migrate command", err)
	}
	if persistence.migrations != 0 {
		t.Fatalf("required-current schema applied %d migrations", persistence.migrations)
	}

	persistence.validateErr = nil
	if migration, err := prepareSchema(context.Background(), persistence, schemaRequireCurrent); err != nil || migration != nil || persistence.migrations != 0 {
		t.Fatalf("current schema = %#v, %v after %d migrations", migration, err, persistence.migrations)
	}
}

func TestServingSchemaPolicyAppliesPendingMigrations(t *testing.T) {
	t.Parallel()

	persistence := &schemaPersistenceFake{}
	migration, err := prepareSchema(context.Background(), persistence, schemaMigrate)
	if err != nil || migration == nil || migration.Applied != 1 || persistence.migrations != 1 || persistence.validations != 0 {
		t.Fatalf("serving schema = %#v, %v after migrations=%d validations=%d", migration, err, persistence.migrations, persistence.validations)
	}
}
