// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/store_test.go. This
// file owns SQL adapter setup and invokes reusable tests through store.Store.

package sqlstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/store"
)

func StoreTest(t *testing.T, test func(*testing.T, store.Store)) {
	t.Helper()
	sqlStore := openTestStore(t)
	resetTestStore(t, sqlStore)
	test(t, sqlStore)
}

func openTestStore(t *testing.T) *SqlStore {
	t.Helper()
	settings := testSettings(t)
	migrator, err := NewMigrator(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}

	sqlStore, err := New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlStore.ValidateSchema(context.Background()); err != nil {
		_ = sqlStore.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlStore.Close(); err != nil {
			t.Errorf("close SQL store: %v", err)
		}
	})
	return sqlStore
}

func resetTestStore(t *testing.T, sqlStore *SqlStore) {
	t.Helper()
	_, err := sqlStore.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			installation_state, audit_events, user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`)
	if err != nil {
		t.Fatalf("reset SQL store: %v", err)
	}
}

func testSettings(t *testing.T) Settings {
	t.Helper()
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	return Settings{
		DataSource:            dataSource,
		MaxOpenConnections:    10,
		MaxIdleConnections:    2,
		ConnectionMaxLifetime: time.Minute,
		ConnectionMaxIdleTime: time.Minute,
		QueryTimeout:          10 * time.Second,
		MigrationTimeout:      time.Minute,
	}
}
