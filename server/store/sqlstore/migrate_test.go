//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMigrationsRoundTrip(t *testing.T) {
	settings := testSettings(t)
	migrator, err := NewMigrator(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := migrator.Close(); err != nil {
			t.Errorf("close migrator: %v", err)
		}
	})
	if err := migrator.Up(); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	pending, err := migrator.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("Pending() = %d, %v", len(pending), err)
	}
	version, err := migrator.SchemaVersion(context.Background())
	if err != nil || version != 10 {
		t.Fatalf("SchemaVersion() = %d, %v", version, err)
	}

	ctx := context.Background()
	rolledBack, err := migrator.Down(1)
	if err != nil || rolledBack != 1 {
		t.Fatalf("Down(1) = %d, %v", rolledBack, err)
	}
	var externalStateTableRemoved bool
	if err := migrator.store.GetMaster().Get(ctx, &externalStateTableRemoved, `
		SELECT to_regclass('public.external_login_states') IS NULL
	`); err != nil || !externalStateTableRemoved {
		t.Fatalf("external-login-state rollback = %v, %v", externalStateTableRemoved, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("restore external-login-state migration: %v", err)
	}
	rolledBack, err = migrator.Down(2)
	if err != nil || rolledBack != 2 {
		t.Fatalf("Down(2) = %d, %v", rolledBack, err)
	}
	var sessionActionsRemoved bool
	if err := migrator.store.GetMaster().Get(ctx, &sessionActionsRemoved, `
		SELECT NOT EXISTS (
			SELECT 1 FROM roles
			 WHERE name = 'system_admin' AND built_in AND delete_at = 0
			   AND permissions && ARRAY['session.view', 'session.manage']
		)`); err != nil || !sessionActionsRemoved {
		t.Fatalf("session-action rollback = %v, %v", sessionActionsRemoved, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("restore session-action migration: %v", err)
	}
	rolledBack, err = migrator.Down(6)
	if err != nil || rolledBack != 6 {
		t.Fatalf("Down(6) = %d, %v", rolledBack, err)
	}
	if _, err := migrator.store.GetMaster().Exec(ctx, `
		TRUNCATE TABLE
			installation_state, audit_events, user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		t.Fatalf("truncate before reconciliation migration: %v", err)
	}
	if _, err := migrator.store.GetMaster().Exec(
		ctx,
		`INSERT INTO roles (
			id, create_at, update_at, delete_at, name, display_name,
			description, permissions, built_in
		) VALUES (?, ?, ?, 0, 'system_admin', 'System Administrator', '', ARRAY['institution.manage'], true)`,
		model.NewId(),
		model.GetMillis(),
		model.GetMillis(),
	); err != nil {
		t.Fatalf("insert pre-migration system administrator: %v", err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("reconciliation Up() error = %v", err)
	}
	var reconciled bool
	if err := migrator.store.GetMaster().Get(
		ctx,
		&reconciled,
		`SELECT permissions @> ARRAY[
			'user.view', 'user.manage', 'session.view', 'session.manage'
		]
		 FROM roles WHERE name = 'system_admin'`,
	); err != nil || !reconciled {
		t.Fatalf("system administrator reconciliation = %v, %v", reconciled, err)
	}
	rolledBack, err = migrator.Down(6)
	if err != nil || rolledBack != 6 {
		t.Fatalf("reconciliation Down(6) = %d, %v", rolledBack, err)
	}
	var removed bool
	if err := migrator.store.GetMaster().Get(
		ctx,
		&removed,
		`SELECT NOT (permissions && ARRAY[
			'user.view', 'user.manage', 'session.view', 'session.manage'
		])
		 FROM roles WHERE name = 'system_admin'`,
	); err != nil || !removed {
		t.Fatalf("system administrator reconciliation rollback = %v, %v", removed, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("restore reconciliation migration: %v", err)
	}

	if _, err := migrator.store.GetMaster().Exec(ctx, `
		TRUNCATE TABLE
			external_login_states, installation_state, audit_events, mfa_recovery_codes, mfa_credentials,
			user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		t.Fatalf("truncate before down migrations: %v", err)
	}
	rolledBack, err = migrator.Down(10)
	if err != nil || rolledBack != 10 {
		t.Fatalf("Down(10) = %d, %v", rolledBack, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
}
