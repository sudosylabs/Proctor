// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"
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
	if err != nil || version != 2 {
		t.Fatalf("SchemaVersion() = %d, %v", version, err)
	}

	if _, err := migrator.store.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		t.Fatalf("truncate before down migrations: %v", err)
	}
	rolledBack, err := migrator.Down(2)
	if err != nil || rolledBack != 2 {
		t.Fatalf("Down(2) = %d, %v", rolledBack, err)
	}
	if err := migrator.Up(); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
}
