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

func TestRetentionStore(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestRetentionStore(t, persistence, retentionSQLProbe(persistence))
}

func retentionSQLProbe(s *SQLStore) storetest.RetentionSQLProbe {
	return storetest.RetentionSQLProbe{
		ConcurrentPeer: newSQLRetentionStore(s),
		AgeCompletion: func(t *testing.T, ctx context.Context, id model.ExamSittingID) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_sitting_records_completions SET completed_at=clock_timestamp()-interval '100 days' WHERE exam_sitting_id=? AND stale_at IS NULL`, id.String()); err != nil {
				t.Fatal(err)
			}
		},
		ExpireGrace: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `UPDATE retention_retirements SET scheduled_at=clock_timestamp()-interval '2 days',retire_after=clock_timestamp()-interval '1 day' WHERE submission_id=? AND state='grace'`, id.String()); err != nil {
				t.Fatal(err)
			}
		},
		AssertSealedGuards: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `DELETE FROM exam_submission_manifest_entries WHERE submission_id=?`, id.String()); err == nil {
				t.Fatal("ordinary sealed-manifest deletion succeeded")
			}
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_submissions SET work_retired_at=clock_timestamp() WHERE id=?`, id.String()); err == nil {
				t.Fatal("unapproved retirement marker succeeded")
			}
		},
		AssertPrivateContentRemoved: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			t.Helper()
			var count int
			for _, query := range []string{
				`SELECT count(*) FROM exam_submission_manifest_entries WHERE submission_id=?`,
				`SELECT count(*) FROM exam_attempt_workspace_entries WHERE workspace_id=(SELECT workspace_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM exam_attempt_workspace_journal WHERE workspace_id=(SELECT workspace_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM submission_review_waivers WHERE submission_id=?`,
				`SELECT count(*) FROM command_outcomes WHERE outcome::text LIKE '%private-answer.txt%' AND user_id=(SELECT a.candidate_user_id FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id WHERE sub.id=?)`,
			} {
				if err := s.GetMaster().Get(ctx, &count, query, id.String()); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("retired private copy retained: %s (%d)", query, count)
				}
			}
			if err := s.GetMaster().Get(ctx, &count, `SELECT count(*) FROM exam_revision_starter_workspace_entries WHERE exam_revision_id=(SELECT exam_revision_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("shared published starter changed: %d", count)
			}
		},
	}
}
