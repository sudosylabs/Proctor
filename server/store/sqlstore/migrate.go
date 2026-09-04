// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/sqlstore/migrate.go. Proctor
// uses the same Morph embedded-source and PostgreSQL-driver pattern. Normal
// startup applies pending forward migrations under Morph's named lock;
// deliberate rollback remains available only through operator tooling.

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/mattermost/morph"
	postgresdriver "github.com/mattermost/morph/drivers/postgres"
	"github.com/mattermost/morph/models"
	embeddedsrc "github.com/mattermost/morph/sources/embedded"

	"github.com/sudosylabs/proctor/server/migrations"
)

type Migrator struct {
	store  *SQLStore
	engine *migrationEngine
}

type migrationEngine struct {
	*morph.Morph
	cancelLockContext context.CancelCauseFunc
}

func (e *migrationEngine) Close() error {
	if e == nil {
		return nil
	}
	err := e.Morph.Close()
	// Morph's refresh goroutine has stopped in Close, so the acquisition
	// context can now be canceled without allowing the lock to expire while a
	// migration statement is still running.
	e.cancelLockContext(context.Canceled)
	return err
}

// MigrationResult describes the schema convergence performed during normal
// startup. Applied is zero when the database was already current.
type MigrationResult struct {
	Applied       int
	SchemaVersion int
}

// ApplyPendingMigrations converges an already-open operational store to the
// embedded schema under Morph's named PostgreSQL migration lock. Closing the
// engine releases only its dedicated connection; the supplied store remains
// open for normal application use.
func (s *SQLStore) ApplyPendingMigrations(ctx context.Context) (result MigrationResult, resultErr error) {
	if s == nil {
		return result, errors.New("apply database migrations: SQL store is nil")
	}
	engine, err := newMigrationEngine(ctx, s, s.settings, io.Discard)
	if err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, engine.Close())
	}()

	pending, err := engine.Diff(models.Up)
	if err != nil {
		return result, fmt.Errorf("inspect pending migrations: %w", err)
	}
	if err := engine.ApplyAll(); err != nil {
		return result, fmt.Errorf("apply database migrations: %w", err)
	}
	result.Applied = len(pending)
	result.SchemaVersion, err = s.GetDBSchemaVersion(ctx)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("read database schema version after migration: %w", err)
	}
	return result, nil
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

func newMigrationEngine(ctx context.Context, store *SQLStore, settings Settings, output io.Writer) (*migrationEngine, error) {
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
	lockCtx, finishAcquisition, cancelLockContext := newMigrationLockContext(
		ctx,
		settings.MigrationTimeout,
	)
	engine, err := morph.New(
		lockCtx,
		driver,
		source,
		morph.WithLogger(log.New(output, "", 0)),
		morph.WithLock("proctor-schema-migrations"),
		morph.SetStatementTimeoutInSeconds(timeoutSeconds),
	)
	acquisitionErr := finishAcquisition()
	if err != nil {
		cancelLockContext(context.Canceled)
		_ = driver.Close()
		return nil, fmt.Errorf("construct migration engine: %w", err)
	}
	if acquisitionErr != nil {
		closeErr := engine.Close()
		cancelLockContext(context.Canceled)
		return nil, errors.Join(
			fmt.Errorf("acquire migration lock: %w", acquisitionErr),
			closeErr,
		)
	}
	return &migrationEngine{
		Morph:             engine,
		cancelLockContext: cancelLockContext,
	}, nil
}

// newMigrationLockContext bounds only lock acquisition. Once acquisition has
// completed, it deliberately detaches from the caller so Morph can refresh the
// database mutex until migrationEngine.Close stops the refresh goroutine and
// releases the lock. Migration statements retain Morph's separate per-
// statement timeout.
func newMigrationLockContext(
	parent context.Context,
	timeout time.Duration,
) (context.Context, func() error, context.CancelCauseFunc) {
	lockCtx, cancelLockContext := context.WithCancelCause(context.Background())
	acquired := make(chan struct{})
	watcherDone := make(chan struct{})
	timer := time.NewTimer(timeout)
	go func() {
		defer close(watcherDone)
		select {
		case <-parent.Done():
			cancelLockContext(context.Cause(parent))
		case <-timer.C:
			cancelLockContext(context.DeadlineExceeded)
		case <-acquired:
		}
	}()
	finishAcquisition := func() error {
		timer.Stop()
		close(acquired)
		<-watcherDone
		return context.Cause(lockCtx)
	}
	return lockCtx, finishAcquisition, cancelLockContext
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
