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
	"errors"
	"slices"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestManagedFileCanonicalIDConstraints(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)

	want := []string{
		"file_entries_current_revision_id_canonical_check",
		"file_entries_id_canonical_check",
		"file_legal_holds_file_entry_id_canonical_check",
		"file_renditions_file_revision_id_canonical_check",
		"file_renditions_id_canonical_check",
		"file_revisions_file_entry_id_canonical_check",
		"file_revisions_id_canonical_check",
		"file_revisions_purge_claim_id_canonical_check",
		"upload_leases_created_by_user_id_canonical_check",
		"upload_leases_file_revision_id_canonical_check",
		"upload_leases_id_canonical_check",
	}
	var got []string
	if err := persistence.GetMaster().Select(ctx, &got, `
		SELECT conname FROM pg_constraint
		 WHERE conname = ANY(?)
		 ORDER BY conname`, pq.Array(want)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("managed-file canonical constraints = %v, want %v", got, want)
	}

	at := model.NowUTC()
	_, err := persistence.GetMaster().Exec(ctx, `
		INSERT INTO file_entries (
			id, created_at, updated_at, revision, indexing_policy, purpose
		) VALUES ('bad', ?, ?, 1, 'none', 'profile_picture_custom')`, at, at)
	var postgresErr *pq.Error
	if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != "file_entries_id_canonical_check" {
		t.Fatalf("constraint violation = %v, want file_entries_id_canonical_check", err)
	}
}
