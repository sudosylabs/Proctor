//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var baselineTables = []string{
	"academic_periods",
	"academic_unit_members",
	"academic_units",
	"access_policies",
	"access_policy_transitions",
	"administrator_recovery_records",
	"affiliations",
	"audit_events",
	"browser_activity_events",
	"browser_activity_sources",
	"browser_authentication_transactions",
	"browser_delivery_declarations",
	"browser_delivery_retired_sources",
	"browser_integrity_groups",
	"browser_integrity_overflow",
	"class_members",
	"classes",
	"cluster_discovery_nodes",
	"command_outcomes",
	"desktop_compatibility_policies",
	"desktop_registrations",
	"exam_attempt_connections",
	"exam_attempt_correction_acknowledgements",
	"exam_attempt_delivery_budgets",
	"exam_attempt_focus_loss_evaluations",
	"exam_attempt_focus_loss_pending",
	"exam_attempt_manager_end_actions",
	"exam_attempt_participations",
	"exam_attempt_security_owners",
	"exam_attempt_suspensions",
	"exam_attempt_workspace_entries",
	"exam_attempt_workspace_journal",
	"exam_attempt_workspace_objects",
	"exam_attempt_workspaces",
	"exam_attempts",
	"exam_correction_resource_stages",
	"exam_drafts",
	"exam_export_artifacts",
	"exam_export_submissions",
	"exam_exports",
	"exam_managers",
	"exam_native_delivery_batches",
	"exam_native_delivery_declarations",
	"exam_native_delivery_records",
	"exam_native_occurrences",
	"exam_native_source_resets",
	"exam_resource_identities",
	"exam_resources",
	"exam_revision_resources",
	"exam_revision_starter_workspace_entries",
	"exam_revisions",
	"exam_security_preflights",
	"exam_sitting_live_corrections",
	"exam_sitting_mail_fanouts",
	"exam_sitting_mail_recipients",
	"exam_sitting_private_actions",
	"exam_sitting_records_completions",
	"exam_sittings",
	"exam_starter_workspace_entries",
	"exam_starter_workspace_objects",
	"exam_submission_manifest_entries",
	"exam_submissions",
	"exams",
	"external_identities",
	"external_login_states",
	"file_entries",
	"file_legal_holds",
	"file_renditions",
	"file_revisions",
	"installation_states",
	"institutions",
	"integrity_discrepancies",
	"integrity_evidence",
	"integrity_flags",
	"integrity_review_decisions",
	"invitations",
	"job_attempts",
	"job_permanent_occurrences",
	"jobs",
	"mail_audience_states",
	"mail_deliveries",
	"mail_fanout_bundles",
	"mail_key_state",
	"mail_occurrences",
	"mail_payload_keys",
	"mail_send_rate_limit",
	"mfa_credentials",
	"mfa_recovery_codes",
	"native_condition_evidence",
	"onboarding_import_rows",
	"onboarding_imports",
	"password_credentials",
	"personal_access_token_mutation_preparations",
	"personal_access_tokens",
	"programme_levels",
	"programmes",
	"retention_controls",
	"retention_expiry_schedules",
	"retention_holds",
	"retention_notices",
	"retention_policies",
	"retention_previews",
	"retention_purge_objects",
	"retention_retirements",
	"retention_source_protections",
	"role_bindings",
	"roles",
	"serving_node_leases",
	"session_credentials",
	"sessions",
	"submission_review_finalizations",
	"submission_review_inventory_discrepancies",
	"submission_review_inventory_evidence",
	"submission_review_inventory_flags",
	"submission_review_release_preparations",
	"submission_review_waivers",
	"submission_reviews",
	"upload_leases",
	"user_mfa_recovery",
	"user_settings_documents",
	"user_tokens",
	"users",
}

func TestMigrationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	migrator, err := NewMigrator(ctx, testSettings(t))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, migrator.Close())
	})

	// Other integration tests share this disposable database and may have
	// already installed the baseline. Roll it back first so this test always
	// proves that version zero can be built solely from the squashed baseline.
	version, err := migrator.SchemaVersion(ctx)
	require.NoError(t, err)
	switch version {
	case 0:
	case 1:
		truncateBaselineTables(t, ctx, migrator)
		rolledBack, err := migrator.Down(1)
		require.NoError(t, err, "prepare version-zero database: Down(1)")
		require.Equal(t, 1, rolledBack, "prepare version-zero database: Down(1)")
	default:
		require.FailNow(t, fmt.Sprintf("database schema version = %d; recreate this pre-release development database before testing baseline version 1", version))
	}
	assertBaselineAbsent(t, ctx, migrator)

	require.NoError(t, migrator.Up())
	assertBaselineSchema(t, ctx, migrator)

	truncateBaselineTables(t, ctx, migrator)
	rolledBack, err := migrator.Down(1)
	require.NoError(t, err, "Down(1)")
	require.Equal(t, 1, rolledBack, "Down(1)")
	assertBaselineAbsent(t, ctx, migrator)

	require.NoError(t, migrator.Up())
	assertBaselineSchema(t, ctx, migrator)
}

func assertBaselineSchema(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	pending, err := migrator.Pending()
	require.NoError(t, err, "Pending()")
	require.Empty(t, pending, "Pending()")
	version, err := migrator.SchemaVersion(ctx)
	require.NoError(t, err, "SchemaVersion()")
	require.Equal(t, 1, version, "SchemaVersion()")

	var tables []string
	require.NoError(t, migrator.store.GetMaster().Select(ctx, &tables, `
		SELECT table_name
		  FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_type = 'BASE TABLE'
		   AND table_name NOT IN ('db_lock', 'db_migrations')
		 ORDER BY table_name
	`))
	require.True(t, slices.Equal(tables, baselineTables), "baseline tables = %v; want %v", tables, baselineTables)

	type temporalColumn struct {
		TableName  string `db:"table_name"`
		ColumnName string `db:"column_name"`
		DataType   string `db:"data_type"`
	}
	var temporalColumns []temporalColumn
	require.NoError(t, migrator.store.GetMaster().Select(ctx, &temporalColumns, `
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
	`))
	require.NotEmpty(t, temporalColumns, "baseline contains no temporal columns")
	for _, column := range temporalColumns {
		assert.Equal(t, "timestamp with time zone", column.DataType, "%s.%s type = %q; want timestamp with time zone", column.TableName, column.ColumnName, column.DataType)
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
		t.Run(column.table+"."+column.name, func(t *testing.T) {
			var nullable string
			require.NoError(t, migrator.store.GetMaster().Get(ctx, &nullable, `
				SELECT is_nullable
				  FROM information_schema.columns
				 WHERE table_schema = 'public' AND table_name = ? AND column_name = ?
			`, column.table, column.name), "inspect nullability")
			assert.Equal(t, "YES", nullable)
		})
	}
}

func assertBaselineAbsent(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	version, err := migrator.SchemaVersion(ctx)
	require.NoError(t, err, "SchemaVersion() after rollback")
	require.Equal(t, 0, version, "SchemaVersion() after rollback")
	for _, table := range baselineTables {
		var absent bool
		query := fmt.Sprintf("SELECT to_regclass('public.%s') IS NULL", table)
		assert.NoError(t, migrator.store.GetMaster().Get(ctx, &absent, query), "table %s absent after rollback", table)
		assert.True(t, absent, "table %s absent after rollback", table)
	}
}

func truncateBaselineTables(t *testing.T, ctx context.Context, migrator *Migrator) {
	t.Helper()

	_, err := migrator.store.GetMaster().Exec(ctx, `
		TRUNCATE TABLE
			cluster_discovery_nodes, mail_send_rate_limit, mail_key_state, mail_fanout_bundles, mail_payload_keys, mail_deliveries, mail_occurrences, onboarding_import_rows, onboarding_imports, invitations, job_attempts, jobs, browser_authentication_transactions, external_login_states, installation_states, desktop_compatibility_policies, access_policy_transitions, access_policies,
			audit_events, mfa_recovery_codes, mfa_credentials, user_tokens,
			user_settings_documents,
			personal_access_token_mutation_preparations, personal_access_tokens, session_credentials, sessions, desktop_registrations, role_bindings,
			roles, class_members, exam_submission_manifest_entries, exam_submissions,
			exam_attempt_suspensions, integrity_evidence, integrity_flags,
			exam_attempt_connections, exam_attempt_participations,
			exam_attempt_workspace_entries, exam_attempt_workspace_objects,
			exam_attempt_workspaces, exam_attempts, exam_starter_workspace_entries,
			exam_starter_workspace_objects, exam_resources, exam_managers, exam_drafts, exams,
			academic_unit_members, affiliations,
			password_credentials, external_identities, upload_leases, users,
			file_renditions, file_revisions, file_entries, classes,
			academic_periods, programme_levels, programmes, academic_units,
			institutions CASCADE
	`)
	require.NoError(t, err)
}
