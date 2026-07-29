// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/store.go. Proctor
// retains Mattermost's root SQL store, per-model store registry, centralized
// query builder, connection lifecycle, and interface-returning accessors.
// Proctor is PostgreSQL-only and omits replica, search, license, and transparent
// store-layer machinery until those capabilities have an actual consumer.

// Package sqlstore implements Proctor's PostgreSQL persistence adapter.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/store"
)

const minimumPostgresVersion = 140000

type Settings struct {
	DataSource            string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
	QueryTimeout          time.Duration
	MigrationTimeout      time.Duration
}

func SettingsFromConfig(cfg config.Database) Settings {
	return Settings{
		DataSource:            cfg.DataSource,
		MaxOpenConnections:    cfg.MaxOpenConnections,
		MaxIdleConnections:    cfg.MaxIdleConnections,
		ConnectionMaxLifetime: cfg.ConnectionMaxLifetime.Duration,
		ConnectionMaxIdleTime: cfg.ConnectionMaxIdleTime.Duration,
		QueryTimeout:          cfg.QueryTimeout.Duration,
		MigrationTimeout:      cfg.MigrationTimeout.Duration,
	}
}

func (s Settings) validate() error {
	if s.DataSource == "" {
		return errors.New("database data source is required")
	}
	if s.MaxOpenConnections <= 0 {
		return errors.New("database max open connections must be greater than zero")
	}
	if s.MaxIdleConnections < 0 || s.MaxIdleConnections > s.MaxOpenConnections {
		return errors.New("database max idle connections must be between zero and max open connections")
	}
	if s.ConnectionMaxLifetime <= 0 || s.ConnectionMaxIdleTime <= 0 ||
		s.QueryTimeout <= 0 || s.MigrationTimeout <= 0 {
		return errors.New("database timeouts and connection lifetimes must be greater than zero")
	}
	return nil
}

// SqlStoreStores holds the concrete adapters behind the model-store contracts.
// Keeping this registry separate from SqlStore mirrors Mattermost's composition
// flow and makes future store decorators possible without changing callers.
type SqlStoreStores struct {
	institution        store.InstitutionStore
	academicUnit       store.AcademicUnitStore
	programme          store.ProgrammeStore
	programmeLevel     store.ProgrammeLevelStore
	academicPeriod     store.AcademicPeriodStore
	class              store.ClassStore
	user               store.UserStore
	passwordCredential store.PasswordCredentialStore
	session            store.SessionStore
	sessionCredential  store.SessionCredentialStore
	role               store.RoleStore
	roleBinding        store.RoleBindingStore
	audit              store.AuditStore
	installation       store.InstallationStore
}

// SqlStore owns PostgreSQL connections and all concrete model stores.
type SqlStore struct {
	masterX  *sqlxDBWrapper
	stores   SqlStoreStores
	settings Settings
}

func New(ctx context.Context, settings Settings) (*SqlStore, error) {
	if err := settings.validate(); err != nil {
		return nil, err
	}

	db, err := sqlx.Open("postgres", settings.DataSource)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(settings.MaxOpenConnections)
	db.SetMaxIdleConns(settings.MaxIdleConnections)
	db.SetConnMaxLifetime(settings.ConnectionMaxLifetime)
	db.SetConnMaxIdleTime(settings.ConnectionMaxIdleTime)

	sqlStore := &SqlStore{
		masterX:  newSqlxDBWrapper(db, settings.QueryTimeout),
		settings: settings,
	}
	if err := sqlStore.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := sqlStore.verifyPostgresVersion(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	sqlStore.stores.institution = newSqlInstitutionStore(sqlStore)
	sqlStore.stores.academicUnit = newSqlAcademicUnitStore(sqlStore)
	sqlStore.stores.programme = newSqlProgrammeStore(sqlStore)
	sqlStore.stores.programmeLevel = newSqlProgrammeLevelStore(sqlStore)
	sqlStore.stores.academicPeriod = newSqlAcademicPeriodStore(sqlStore)
	sqlStore.stores.class = newSqlClassStore(sqlStore)
	sqlStore.stores.user = newSqlUserStore(sqlStore)
	sqlStore.stores.passwordCredential = newSqlPasswordCredentialStore(sqlStore)
	sqlStore.stores.session = newSqlSessionStore(sqlStore)
	sqlStore.stores.sessionCredential = newSqlSessionCredentialStore(sqlStore)
	sqlStore.stores.role = newSqlRoleStore(sqlStore)
	sqlStore.stores.roleBinding = newSqlRoleBindingStore(sqlStore)
	sqlStore.stores.audit = newSqlAuditStore(sqlStore)
	sqlStore.stores.installation = newSqlInstallationStore(sqlStore)
	return sqlStore, nil
}

func (ss *SqlStore) Close() error {
	if ss == nil || ss.masterX == nil {
		return nil
	}
	return ss.masterX.Close()
}

func (ss *SqlStore) Ping(ctx context.Context) error {
	return ss.GetMaster().Ping(ctx)
}

func (ss *SqlStore) Stats() sql.DBStats {
	return ss.GetMaster().Stats()
}

func (ss *SqlStore) Institution() store.InstitutionStore {
	return ss.stores.institution
}

func (ss *SqlStore) AcademicUnit() store.AcademicUnitStore {
	return ss.stores.academicUnit
}

func (ss *SqlStore) Programme() store.ProgrammeStore {
	return ss.stores.programme
}

func (ss *SqlStore) ProgrammeLevel() store.ProgrammeLevelStore {
	return ss.stores.programmeLevel
}

func (ss *SqlStore) AcademicPeriod() store.AcademicPeriodStore {
	return ss.stores.academicPeriod
}

func (ss *SqlStore) Class() store.ClassStore {
	return ss.stores.class
}

func (ss *SqlStore) User() store.UserStore {
	return ss.stores.user
}

func (ss *SqlStore) PasswordCredential() store.PasswordCredentialStore {
	return ss.stores.passwordCredential
}

func (ss *SqlStore) Session() store.SessionStore {
	return ss.stores.session
}

func (ss *SqlStore) SessionCredential() store.SessionCredentialStore {
	return ss.stores.sessionCredential
}

func (ss *SqlStore) Role() store.RoleStore {
	return ss.stores.role
}

func (ss *SqlStore) RoleBinding() store.RoleBindingStore {
	return ss.stores.roleBinding
}

func (ss *SqlStore) Audit() store.AuditStore {
	return ss.stores.audit
}

func (ss *SqlStore) Installation() store.InstallationStore {
	return ss.stores.installation
}

func (ss *SqlStore) GetMaster() *sqlxDBWrapper {
	return ss.masterX
}

func (ss *SqlStore) getQueryBuilder() sq.StatementBuilderType {
	return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
}

func (ss *SqlStore) verifyPostgresVersion(ctx context.Context) error {
	var raw string
	if err := ss.GetMaster().Get(ctx, &raw, "SHOW server_version_num"); err != nil {
		return fmt.Errorf("read PostgreSQL version: %w", err)
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("parse PostgreSQL version %q: %w", raw, err)
	}
	if version < minimumPostgresVersion {
		return fmt.Errorf(
			"PostgreSQL %s is unsupported; version 14.0 or newer is required",
			postgresVersionString(version),
		)
	}
	return nil
}

func postgresVersionString(version int) string {
	return strconv.Itoa(version/10000) + "." + strconv.Itoa(version%10000)
}

var _ store.Store = (*SqlStore)(nil)
