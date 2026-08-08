// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/migrate.go. Proctor
// uses the same Morph embedded-source and PostgreSQL-driver pattern, while
// keeping migrations separate from normal server startup.

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/mattermost/morph"
	postgresdriver "github.com/mattermost/morph/drivers/postgres"
	"github.com/mattermost/morph/models"
	embeddedsrc "github.com/mattermost/morph/sources/embedded"

	"github.com/sudosylabs/proctor/server/migrations"
)

type Migrator struct {
	store  *SQLStore
	engine *morph.Morph
}

func NewMigrator(ctx context.Context, settings Settings) (*Migrator, error) {
	store, err := New(ctx, settings)
	if err != nil {
		return nil, err
	}
	engine, err := newMigrationEngine(ctx, store, settings, io.Discard)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &Migrator{store: store, engine: engine}, nil
}

func newMigrationEngine(ctx context.Context, store *SQLStore, settings Settings, output io.Writer) (*morph.Morph, error) {
	names, err := migrationNames()
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	source, err := embeddedsrc.WithInstance(&embeddedsrc.AssetSource{
		Names: names,
		AssetFunc: func(name string) ([]byte, error) {
			return migrations.Assets.ReadFile(migrationPath(name))
		},
	})
	if err != nil {
		return nil, fmt.Errorf("construct migration source: %w", err)
	}
	driver, err := postgresdriver.WithInstance(store.GetMaster().DB().DB)
	if err != nil {
		return nil, fmt.Errorf("construct PostgreSQL migration driver: %w", err)
	}
	if output == nil {
		output = io.Discard
	}
	timeoutSeconds := int(settings.MigrationTimeout.Seconds())
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	engine, err := morph.New(
		ctx,
		driver,
		source,
		morph.WithLogger(log.New(output, "", 0)),
		morph.WithLock("proctor-schema-migrations"),
		morph.SetStatementTimeoutInSeconds(timeoutSeconds),
	)
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("construct migration engine: %w", err)
	}
	return engine, nil
}

func (m *Migrator) Up() error {
	if err := m.engine.ApplyAll(); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

func (m *Migrator) Pending() ([]*models.Migration, error) {
	pending, err := m.engine.Diff(models.Up)
	if err != nil {
		return nil, fmt.Errorf("inspect pending migrations: %w", err)
	}
	return pending, nil
}

// Down rolls back a bounded number of migrations. It is exposed for migration
// verification and deliberate operator tooling, not normal server startup.
func (m *Migrator) Down(steps int) (int, error) {
	if steps <= 0 {
		return 0, errors.New("migration rollback steps must be greater than zero")
	}
	applied, err := m.engine.ApplyDown(steps)
	if err != nil {
		return applied, fmt.Errorf("roll back database migrations: %w", err)
	}
	return applied, nil
}

func (m *Migrator) SchemaVersion(ctx context.Context) (int, error) {
	return m.store.GetDBSchemaVersion(ctx)
}

func (m *Migrator) Close() error {
	if m == nil {
		return nil
	}
	var engineErr error
	if m.engine != nil {
		engineErr = m.engine.Close()
	}
	return errors.Join(engineErr, m.store.Close())
}
