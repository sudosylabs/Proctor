//go:build integration

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/store_test.go. This
// file owns SQL adapter setup and invokes reusable tests through store.Store.

package sqlstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func saveIntegrationUser(t *testing.T, ctx context.Context, persistence store.Store, user *model.User) *model.User {
	t.Helper()
	user.PrepareCreate(model.NewUserID(), model.NowUTC())
	command, err := json.Marshal(map[string]string{"user_id": user.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(model.NewJobID(), model.JobTypeProfilePictureGenerateDefault, 1, command, user.ID.String(), user.CreatedAt, user.CreatedAt, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := persistence.User().Create(ctx, &store.UserCreation{User: user, DefaultProfilePictureJob: job})
	if err != nil {
		t.Fatal(err)
	}
	return result.User
}

func StoreTest(t *testing.T, test func(*testing.T, store.Store)) {
	t.Helper()
	sqlStore := openTestStore(t)
	resetTestStore(t, sqlStore)
	test(t, sqlStore)
}

func openTestStore(t *testing.T) *SQLStore {
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

func resetTestStore(t *testing.T, sqlStore *SQLStore) {
	t.Helper()
	_, err := sqlStore.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			job_attempts, job_permanent_occurrences, jobs, external_login_states, installation_states, command_outcomes, audit_events, user_tokens, personal_access_tokens, session_credentials, sessions, file_legal_holds, upload_leases, file_renditions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users, file_revisions, file_entries,
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
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
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
