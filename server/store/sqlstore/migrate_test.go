//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

var baselineTables = []string{
	"academic_periods",
	"academic_unit_members",
	"academic_units",
	"affiliations",
	"audit_events",
	"class_members",
	"classes",
	"cluster_discovery_nodes",
	"command_outcomes",
	"exam_drafts",
	"exam_managers",
	"exam_resources",
	"exam_revision_resources",
	"exam_revision_starter_workspace_entries",
	"exam_revisions",
	"exam_starter_workspace_entries",
	"exam_starter_workspace_objects",
	"exams",
	"external_identities",
	"external_login_states",
	"file_entries",
	"file_legal_holds",
	"file_renditions",
	"file_revisions",
	"installation_states",
	"institutions",
	"job_attempts",
	"job_permanent_occurrences",
	"jobs",
	"mfa_credentials",
	"mfa_recovery_codes",
	"password_credentials",
	"personal_access_tokens",
	"programme_levels",
	"programmes",
	"role_bindings",
	"roles",
	"session_credentials",
	"sessions",
	"upload_leases",
	"user_tokens",
	"users",
}

func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	migrator, err := NewMigrator(ctx, testSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := migrator.Close(); err != nil {
			t.Errorf("close migrator: %v", err)
		}
	})

	// Other integration tests share this disposable database and may have
	// already installed the baseline. Roll it back first so this test always
	// proves that version zero can be built solely from the squashed baseline.
	version, err := migrator.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("read initial schema version: %v", err)
	}
	switch version {
	case 0:
	case 1:
		truncateBaselineTables(t, ctx, migrator)
		rolledBack, err := migrator.Down(1)
		if err != nil || rolledBack != 1 {
			t.Fatalf("prepare version-zero database: Down(1) = %d, %v", rolledBack, err)
		}
	default:
		t.Fatalf("database schema version = %d; recreate this pre-release development database before testing baseline version 1", version)
	}
	assertBaselineAbsent(t, ctx, migrator)

	if err := migrator.Up(); err != nil {
		t.Fatalf("first Up() error = %v", err)
	}
	assertBaselineSchema(t, ctx, migrator)

	truncateBaselineTables(t, ctx, migrator)
	rolledBack, err := migrator.Down(1)
	if err != nil || rolledBack != 1 {
		t.Fatalf("Down(1) = %d, %v", rolledBack, err)
	}
	assertBaselineAbsent(t, ctx, migrator)

	if err := migrator.Up(); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
	assertBaselineSchema(t, ctx, migrator)
}

func assertBaselineSchema(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	pending, err := migrator.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("Pending() = %d, %v", len(pending), err)
	}
	version, err := migrator.SchemaVersion(ctx)
	if err != nil || version != 1 {
		t.Fatalf("SchemaVersion() = %d, %v; want 1", version, err)
	}

	var tables []string
	if err := migrator.store.GetMaster().Select(ctx, &tables, `
		SELECT table_name
		  FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_type = 'BASE TABLE'
		   AND table_name NOT IN ('db_lock', 'db_migrations')
		 ORDER BY table_name
	`); err != nil {
		t.Fatalf("list baseline tables: %v", err)
	}
	if !slices.Equal(tables, baselineTables) {
		t.Fatalf("baseline tables = %v; want %v", tables, baselineTables)
	}

	type temporalColumn struct {
		TableName  string `db:"table_name"`
		ColumnName string `db:"column_name"`
		DataType   string `db:"data_type"`
	}
	var temporalColumns []temporalColumn
	if err := migrator.store.GetMaster().Select(ctx, &temporalColumns, `
		SELECT table_name, column_name, data_type
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = ANY (
		       ARRAY(SELECT table_name
		               FROM information_schema.tables
		              WHERE table_schema = 'public'
		                AND table_name NOT IN ('db_lock', 'db_migrations'))
		   )
		   AND column_name LIKE '%\_at' ESCAPE '\'
		 ORDER BY table_name, ordinal_position
	`); err != nil {
		t.Fatalf("inspect temporal columns: %v", err)
	}
	if len(temporalColumns) == 0 {
		t.Fatal("baseline contains no temporal columns")
	}
	for _, column := range temporalColumns {
		if column.DataType != "timestamp with time zone" {
			t.Errorf("%s.%s type = %q; want timestamp with time zone", column.TableName, column.ColumnName, column.DataType)
		}
	}

	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "institutions", name: "archived_at"},
		{table: "users", name: "disabled_at"},
		{table: "affiliations", name: "end_at"},
		{table: "academic_unit_members", name: "end_at"},
		{table: "class_members", name: "end_at"},
		{table: "role_bindings", name: "end_at"},
		{table: "sessions", name: "revoked_at"},
		{table: "session_credentials", name: "used_at"},
		{table: "personal_access_tokens", name: "last_used_at"},
		{table: "user_tokens", name: "consumed_at"},
		{table: "mfa_credentials", name: "pending_expires_at"},
		{table: "mfa_recovery_codes", name: "consumed_at"},
		{table: "external_login_states", name: "consumed_at"},
		{table: "exams", name: "archived_at"},
		{table: "file_entries", name: "archived_at"},
		{table: "users", name: "profile_picture_changed_at"},
		{table: "upload_leases", name: "consumed_at"},
	} {
		var nullable string
		if err := migrator.store.GetMaster().Get(ctx, &nullable, `
			SELECT is_nullable
			  FROM information_schema.columns
			 WHERE table_schema = 'public' AND table_name = ? AND column_name = ?
		`, column.table, column.name); err != nil {
			t.Errorf("inspect %s.%s nullability: %v", column.table, column.name, err)
			continue
		}
		if nullable != "YES" {
			t.Errorf("%s.%s is_nullable = %q; want YES", column.table, column.name, nullable)
		}
	}
}

func assertBaselineAbsent(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	version, err := migrator.SchemaVersion(ctx)
	if err != nil || version != 0 {
		t.Fatalf("SchemaVersion() after rollback = %d, %v; want 0", version, err)
	}
	for _, table := range baselineTables {
		var absent bool
		query := fmt.Sprintf("SELECT to_regclass('public.%s') IS NULL", table)
		if err := migrator.store.GetMaster().Get(ctx, &absent, query); err != nil || !absent {
			t.Errorf("table %s absent after rollback = %v, %v", table, absent, err)
		}
	}
}

func truncateBaselineTables(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	if _, err := migrator.store.GetMaster().Exec(ctx, `
		TRUNCATE TABLE
			cluster_discovery_nodes, job_attempts, jobs, external_login_states, installation_states,
			audit_events, mfa_recovery_codes, mfa_credentials, user_tokens,
			personal_access_tokens, session_credentials, sessions, role_bindings,
			roles, class_members, exam_starter_workspace_entries,
			exam_starter_workspace_objects, exam_resources, exam_managers, exam_drafts, exams,
			academic_unit_members, affiliations,
			password_credentials, external_identities, upload_leases, users,
			file_renditions, file_revisions, file_entries, classes,
			academic_periods, programme_levels, programmes, academic_units,
			institutions CASCADE
	`); err != nil {
		t.Fatalf("truncate baseline tables: %v", err)
	}
}
