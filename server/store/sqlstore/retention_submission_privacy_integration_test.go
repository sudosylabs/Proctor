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
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestRetentionSubmissionPrivacy(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	base := retentionSQLProbe(s)
	storetest.TestRetentionSubmissionPrivacy(t, s, storetest.RetentionSubmissionPrivacyProbe{
		AgeCompletion: base.AgeCompletion, ExpireGrace: base.ExpireGrace,
		Inspect: func(t *testing.T, ctx context.Context, id model.SubmissionID, retired bool) {
			t.Helper()
			var sources, privateOutcomes int
			if err := s.GetMaster().Get(ctx, &sources, `SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil {
				t.Fatal(err)
			}
			if err := s.GetMaster().Get(ctx, &privateOutcomes, `SELECT count(*) FROM command_outcomes o JOIN audit_events a ON a.id=o.original_audit_event_id WHERE a.resource_type='submission' AND a.resource_id=? AND o.outcome::text LIKE '%private-retirement-review-note%'`, id.String()); err != nil {
				t.Fatal(err)
			}
			if !retired {
				if sources != 2 || privateOutcomes < 1 {
					t.Fatalf("fixture lacks source chain or replay copies: %d / %d", sources, privateOutcomes)
				}
				return
			}
			if sources != 0 || privateOutcomes != 0 {
				t.Fatalf("retired source chain or replay copies survived: %d / %d", sources, privateOutcomes)
			}
			var redacted bool
			if err := s.GetMaster().Get(ctx, &redacted, `SELECT integrity_retired_at IS NOT NULL AND integrity_state='retired'
				AND final_focus_loss_sequence IS NULL AND unresolved_integrity_count IS NULL AND browser_activity_state IS NULL
				AND browser_activity_source_session_id IS NULL AND browser_activity_final_sequence IS NULL AND browser_activity_gap_reason IS NULL
				AND work_retired_at IS NULL AND manifest_digest IS NOT NULL AND workspace_cursor IS NOT NULL
				AND manifest_entry_count IS NOT NULL AND manifest_total_file_bytes IS NOT NULL FROM exam_submissions WHERE id=?`, id.String()); err != nil || !redacted {
				t.Fatalf("physical integrity-header redaction failed: %v %v", redacted, err)
			}
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_submissions SET integrity_state='settled',integrity_retired_at=NULL,final_focus_loss_sequence=0,unresolved_integrity_count=0,browser_activity_state='not_applicable' WHERE id=?`, id.String()); err == nil {
				t.Fatal("retirement marker could be removed")
			}
		},
	})
}
