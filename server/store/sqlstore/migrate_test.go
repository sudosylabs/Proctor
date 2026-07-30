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
	if err != nil || version != 5 {
		t.Fatalf("SchemaVersion() = %d, %v", version, err)
	}

	ctx := context.Background()
	rolledBack, err := migrator.Down(1)
	if err != nil || rolledBack != 1 {
		t.Fatalf("Down(1) = %d, %v", rolledBack, err)
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
		`SELECT permissions @> ARRAY['user.view', 'user.manage']
		 FROM roles WHERE name = 'system_admin'`,
	); err != nil || !reconciled {
		t.Fatalf("system administrator reconciliation = %v, %v", reconciled, err)
	}
	rolledBack, err = migrator.Down(1)
	if err != nil || rolledBack != 1 {
		t.Fatalf("reconciliation Down(1) = %d, %v", rolledBack, err)
	}
	var removed bool
	if err := migrator.store.GetMaster().Get(
		ctx,
		&removed,
		`SELECT NOT (permissions && ARRAY['user.view', 'user.manage'])
		 FROM roles WHERE name = 'system_admin'`,
	); err != nil || !removed {
		t.Fatalf("system administrator reconciliation rollback = %v, %v", removed, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("restore reconciliation migration: %v", err)
	}

	if _, err := migrator.store.GetMaster().Exec(ctx, `
		TRUNCATE TABLE
			installation_state, audit_events, user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		t.Fatalf("truncate before down migrations: %v", err)
	}
	rolledBack, err = migrator.Down(5)
	if err != nil || rolledBack != 5 {
		t.Fatalf("Down(5) = %d, %v", rolledBack, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
}
