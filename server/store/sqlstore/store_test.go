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
	settings, err := model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), user.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := persistence.User().Create(ctx, &store.UserCreation{User: user, Settings: settings, DefaultProfilePictureJob: job})
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

func PristineStoreTest(t *testing.T, test func(*testing.T, store.Store)) {
	t.Helper()
	sqlStore := openTestStore(t)
	resetPristineTestStore(t, sqlStore)
	test(t, sqlStore)
}

func StoreTestWithAuthenticationPolicy(
	t *testing.T,
	providerAdmissions map[string]model.ProviderAdmissionMode,
	test func(*testing.T, store.Store),
) {
	t.Helper()
	sqlStore := openTestStore(t)
	resetPristineTestStore(t, sqlStore)
	seedTestAuthenticationPolicy(t, sqlStore, providerAdmissions)
	test(t, sqlStore)
}

func seedTestAuthenticationPolicy(t *testing.T, sqlStore *SQLStore, providerAdmissions map[string]model.ProviderAdmissionMode) {
	t.Helper()
	if providerAdmissions == nil {
		providerAdmissions = map[string]model.ProviderAdmissionMode{}
	}
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), model.NowUTC())
	encoded, err := json.Marshal(providerAdmissions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqlStore.GetMaster().Exec(context.Background(), `INSERT INTO access_policies (
		singleton, id, revision, created_at, updated_at, local_login_enabled,
		public_registration_enabled, invitation_admission_enabled,
		invitation_local_credential_enabled, desktop_authorization_enabled,
		provider_admissions
	) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb)`, policy.ID.String(), policy.Revision,
		policy.CreatedAt, policy.UpdatedAt, policy.LocalLoginEnabled,
		policy.PublicRegistrationEnabled, policy.InvitationAdmissionEnabled,
		policy.InvitationLocalCredentialEnabled, policy.DesktopAuthorizationEnabled,
		encoded); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T) *SQLStore {
	t.Helper()
	settings := testSettings(t)
	sqlStore, err := New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlStore.ApplyPendingMigrations(context.Background()); err != nil {
		_ = sqlStore.Close()
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
	resetPristineTestStore(t, sqlStore)
	seedTestAuthenticationPolicy(t, sqlStore, nil)
}

func resetPristineTestStore(t *testing.T, sqlStore *SQLStore) {
	t.Helper()
	_, err := sqlStore.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			mail_send_rate_limit, mail_key_state, mail_fanout_bundles, mail_payload_keys, mail_deliveries, mail_occurrences, invitations, job_attempts, job_permanent_occurrences, jobs, browser_authentication_transactions, external_login_states, installation_states, access_policy_transitions, access_policies, command_outcomes, audit_events, user_tokens, personal_access_tokens, session_credentials, sessions, file_legal_holds, upload_leases, file_renditions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users, file_revisions, file_entries,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`)
	if err != nil {
		t.Fatalf("reset SQL store: %v", err)
	}
	if _, err = sqlStore.GetMaster().Exec(context.Background(), `INSERT INTO mail_key_state(singleton, required_primary_key_id, active_rekey_job_id, updated_at) VALUES (TRUE, NULL, NULL, clock_timestamp())`); err != nil {
		t.Fatalf("restore mail key state singleton: %v", err)
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
