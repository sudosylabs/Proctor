// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
)

// BuildInfo describes the running server build for version reporting. Its
// JSON field names are part of the public CLI contract.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// CurrentBuildInfo returns build information for the running server.
func CurrentBuildInfo() BuildInfo {
	info := app.CurrentBuildInfo()
	return BuildInfo{
		Version:   info.Version,
		Commit:    info.Commit,
		BuildTime: info.BuildTime,
		GoVersion: info.GoVersion,
	}
}

// ValidateConfig loads and strictly validates the deployment configuration at
// configPath, or the default in-memory configuration when configPath is empty,
// without constructing any infrastructure.
func ValidateConfig(ctx context.Context, configPath string) error {
	configuration, err := openConfiguration(ctx, configPath)
	if err != nil {
		return err
	}
	return configuration.Close()
}

// MigrateUp applies every pending database migration for the configured
// database and returns the resulting schema version.
func MigrateUp(ctx context.Context, configPath string) (int, error) {
	migrator, closeMigrator, err := openMigrator(ctx, configPath)
	if err != nil {
		return 0, err
	}
	defer closeMigrator()

	if err := migrator.Up(); err != nil {
		return 0, err
	}
	return migrator.SchemaVersion(ctx)
}

// MigrationStatus describes the migration state of the configured database.
type MigrationStatus struct {
	// DatabaseVersion is the schema version currently applied.
	DatabaseVersion int
	// ServerVersion is the schema version this server build expects.
	ServerVersion int
	// PendingMigrations is the number of migrations not yet applied.
	PendingMigrations int
}

// MigrateStatus reports the migration state of the configured database.
func MigrateStatus(ctx context.Context, configPath string) (MigrationStatus, error) {
	migrator, closeMigrator, err := openMigrator(ctx, configPath)
	if err != nil {
		return MigrationStatus{}, err
	}
	defer closeMigrator()

	current, err := migrator.SchemaVersion(ctx)
	if err != nil {
		return MigrationStatus{}, err
	}
	local, err := sqlstore.LocalSchemaVersion()
	if err != nil {
		return MigrationStatus{}, err
	}
	pending, err := migrator.Pending()
	if err != nil {
		return MigrationStatus{}, err
	}
	return MigrationStatus{
		DatabaseVersion:   current,
		ServerVersion:     local,
		PendingMigrations: len(pending),
	}, nil
}

// openConfiguration loads the deployment configuration from configPath, or
// returns the default in-memory configuration when configPath is empty.
func openConfiguration(ctx context.Context, configPath string) (*config.Store, error) {
	var backing config.BackingStore
	if configPath == "" {
		backing = config.NewMemoryStore(nil)
	} else {
		fileStore, err := config.NewFileStore(configPath)
		if err != nil {
			return nil, err
		}
		backing = fileStore
	}
	return config.NewStore(ctx, backing, config.StoreOptions{})
}

// openMigrator opens the migration runner for the configured database. The
// returned closer releases the migrator and configuration; its failure is
// intentionally unobserved, matching the historical command behavior.
func openMigrator(
	ctx context.Context,
	configPath string,
) (*sqlstore.Migrator, func(), error) {
	configuration, err := openConfiguration(ctx, configPath)
	if err != nil {
		return nil, nil, err
	}
	migrator, err := sqlstore.NewMigrator(
		ctx,
		sqlstore.SettingsFromConfig(configuration.Get().Database),
	)
	if err != nil {
		_ = configuration.Close()
		return nil, nil, err
	}
	return migrator, func() {
		_ = migrator.Close()
		_ = configuration.Close()
	}, nil
}
