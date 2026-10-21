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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBaselineHotPathIndexes guards the leading columns and partial predicates
// of indexes that serve hot lookups and referencing-side foreign-key probes.
// Each predicate must be implied by the queries it serves, and each key must
// lead with the filtered or referencing columns.
func TestBaselineHotPathIndexes(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	for name, definition := range map[string]string{
		"session_credentials_session_id_idx":                           "(session_id)",
		"sessions_user_id_created_at_idx":                              "(user_id, created_at DESC, id) WHERE (archived_at IS NULL)",
		"academic_unit_members_user_id_start_at_idx":                   "(user_id, start_at DESC, id) WHERE (archived_at IS NULL)",
		"users_default_profile_picture_file_id_idx":                    "(default_profile_picture_file_id) WHERE (default_profile_picture_file_id IS NOT NULL)",
		"users_custom_profile_picture_file_id_idx":                     "(custom_profile_picture_file_id) WHERE (custom_profile_picture_file_id IS NOT NULL)",
		"file_revisions_file_entry_id_idx":                             "(file_entry_id)",
		"external_identities_user_id_provider_idx":                     "(user_id, provider) WHERE (archived_at IS NULL)",
		"class_members_user_id_academic_period_id_idx":                 "(user_id, academic_period_id) WHERE (archived_at IS NULL)",
		"exam_submission_manifest_entries_workspace_entry_idx":         "(workspace_id, entry_id, workspace_object_id)",
		"exam_attempt_workspace_objects_retired_by_audit_event_id_idx": "(retired_by_audit_event_id) WHERE (retired_by_audit_event_id IS NOT NULL)",
		"browser_authentication_transactions_invitation_id_idx":        "(invitation_id) WHERE (invitation_id IS NOT NULL)",
		"integrity_review_decisions_integrity_flag_id_idx":             "(integrity_flag_id, exam_attempt_id)",
	} {
		var definitions []string
		require.NoError(t, persistence.GetMaster().Select(ctx, &definitions, `SELECT indexdef FROM pg_indexes
			WHERE schemaname = current_schema() AND indexname = ?`, name))
		want := "USING btree " + definition
		if assert.Len(t, definitions, 1, "index %s", name) {
			assert.True(t, strings.HasSuffix(definitions[0], want), "index %s = %q; want suffix %q", name, definitions[0], want)
		}
	}

	var stale bool
	require.NoError(t, persistence.GetMaster().Get(ctx, &stale,
		`SELECT to_regclass('sessions_user_id_last_activity_at_idx') IS NOT NULL`))
	assert.False(t, stale, "misnamed sessions_user_id_last_activity_at_idx still exists")
}

// TestBaselineHasNoRedundantIndexes rejects a non-unique btree index whose key
// columns and predicate repeat the leading columns of another index on the
// same table: every write maintains it and no query needs it.
func TestBaselineHasNoRedundantIndexes(t *testing.T) {
	persistence := openTestStore(t)
	var redundant []string
	require.NoError(t, persistence.GetMaster().Select(context.Background(), &redundant, `WITH idx AS (
		SELECT i.indexrelid::regclass::text AS name, i.indrelid AS tbl, i.indisunique AS uniq,
			string_to_array(i.indkey::text, ' ') AS cols, coalesce(pg_get_expr(i.indexprs, i.indrelid), '') AS exprs,
			coalesce(pg_get_expr(i.indpred, i.indrelid), '') AS pred, am.amname AS am
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_am am ON am.oid = c.relam
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema())
	SELECT DISTINCT a.name || ' repeats ' || b.name FROM idx a JOIN idx b ON a.tbl = b.tbl AND a.name <> b.name
		AND a.am = 'btree' AND b.am = 'btree' AND a.exprs = '' AND b.exprs = '' AND a.pred = b.pred AND NOT a.uniq
		AND array_length(a.cols, 1) <= array_length(b.cols, 1) AND a.cols = b.cols[1:array_length(a.cols, 1)]
		AND (array_length(a.cols, 1) < array_length(b.cols, 1) OR b.uniq OR a.name > b.name)
	ORDER BY 1`))
	require.Empty(t, redundant, "redundant baseline indexes")
}
