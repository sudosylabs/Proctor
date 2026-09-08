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
	var retainedRecords, retainedBytes, positions, metadata int64
	storetest.TestRetentionSubmissionPrivacy(t, s, storetest.RetentionSubmissionPrivacyProbe{
		AgeCompletion: base.AgeCompletion, ExpireGrace: base.ExpireGrace,

		InspectDelivery: func(t *testing.T, ctx context.Context, id model.SubmissionID, retired bool) {
			var row struct {
				Records   int64 `db:"records"`
				Bytes     int64 `db:"bytes"`
				Positions int64 `db:"positions"`
				Metadata  int64 `db:"metadata"`
				Browser   bool  `db:"browser"`
				Security  bool  `db:"security"`
			}
			if err := s.GetMaster().Get(ctx, &row, `SELECT browser_retained_records AS records,browser_retained_bytes AS bytes,browser_allocated_positions AS positions,control_metadata_bytes AS metadata,browser_retired_at IS NOT NULL AS browser,security_retired_at IS NOT NULL AS security FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil {
				t.Fatal(err)
			}
			if !retired {
				retainedRecords, retainedBytes, positions, metadata = row.Records, row.Bytes, row.Positions, row.Metadata
				return
			}
			if row.Records != retainedRecords || row.Bytes != retainedBytes || row.Positions != positions || row.Metadata != metadata || !row.Browser || !row.Security {
				t.Fatalf("retirement refunded lifetime budgets or lost marker: %#v", row)
			}
			for _, query := range []string{
				`SELECT count(*) FROM browser_activity_events WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM exam_attempt_security_owners WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?) AND (octet_length(binding_canonical)>2 OR octet_length(latest_report_canonical)>2 OR control_body_canonical IS NOT NULL OR summary_canonical IS NOT NULL)`,
			} {
				var count int
				if err := s.GetMaster().Get(ctx, &count, query, id.String()); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("private delivery payload survived retirement: %d", count)
				}
			}
			var stubs int
			if err := s.GetMaster().Get(ctx, &stubs, `SELECT count(*) FROM browser_delivery_retired_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil || stubs != 2 {
				t.Fatalf("minimal owner stubs=%d: %v", stubs, err)
			}
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_retired_at=NULL WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err == nil {
				t.Fatal("retirement marker removed")
			}
		},
		Inspect: func(t *testing.T, ctx context.Context, id model.SubmissionID, retired bool) {
			t.Helper()
			var sources, privateOutcomes int
			if err := s.GetMaster().Get(ctx, &sources, `SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil {
				t.Fatal(err)
			}
			if err := s.GetMaster().Get(ctx, &privateOutcomes, `SELECT count(*) FROM command_outcomes o JOIN audit_events a ON a.id=o.original_audit_event_id WHERE a.resource_type='submission' AND a.resource_id=? AND o.outcome::text LIKE '%private-retirement-review-note%'`, id.String()); err != nil {
				t.Fatal(err)
			}
			var nativeCopies, nativeDeliveryCopies int
			if err := s.GetMaster().Get(ctx, &nativeCopies, `SELECT count(*) FROM native_condition_evidence WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()); err != nil {
				t.Fatal(err)
			}
			if err := s.GetMaster().Get(ctx, &nativeDeliveryCopies, `SELECT count(*) FROM exam_native_delivery_records WHERE kind='occurrence' AND participation_id IN (SELECT participation_id FROM exam_attempt_security_owners WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?))`, id.String()); err != nil {
				t.Fatal(err)
			}
			expected := 1
			if retired {
				expected = 0
			}
			if nativeCopies != expected || nativeDeliveryCopies != expected {
				t.Fatalf("native condition retirement copies=%d delivery=%d want=%d", nativeCopies, nativeDeliveryCopies, expected)
			}
			if !retired {
				if sources != 2 || privateOutcomes < 1 {
					t.Fatalf("fixture lacks source chain or replay copies: %d / %d", sources, privateOutcomes)
				}
				return
			}
			if sources != 2 || privateOutcomes != 0 {
				t.Fatalf("independent Browser source chain lost or retired Review copies survived: %d / %d", sources, privateOutcomes)
			}
			var redacted bool
			if err := s.GetMaster().Get(ctx, &redacted, `SELECT integrity_retired_at IS NOT NULL AND integrity_state='retired'
				AND final_focus_loss_sequence IS NULL AND unresolved_integrity_count IS NULL
				AND work_retired_at IS NULL AND manifest_digest IS NOT NULL AND workspace_cursor IS NOT NULL
				AND manifest_entry_count IS NOT NULL AND manifest_total_file_bytes IS NOT NULL FROM exam_submissions WHERE id=?`, id.String()); err != nil || !redacted {
				t.Fatalf("physical integrity-header redaction failed: %v %v", redacted, err)
			}
			if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_submissions SET integrity_state='settled',integrity_retired_at=NULL,final_focus_loss_sequence=0,unresolved_integrity_count=0 WHERE id=?`, id.String()); err == nil {
				t.Fatal("retirement marker could be removed")
			}
		},
	})
}

func TestRetentionNativeOperationalIndependence(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	base := retentionSQLProbe(s)
	storetest.TestRetentionNativeOperationalIndependence(t, s, storetest.RetentionSubmissionPrivacyProbe{
		AgeCompletion: base.AgeCompletion, ExpireGrace: base.ExpireGrace,
		Inspect: func(t *testing.T, ctx context.Context, id model.SubmissionID, retired bool) {},
		InspectOperational: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			var row struct {
				Copies            int  `db:"copies"`
				Raw               int  `db:"raw"`
				Retired           bool `db:"retired"`
				IntegrityRetained bool `db:"integrity_retained"`
			}
			if err := s.GetMaster().Get(ctx, &row, `SELECT
			(SELECT count(*) FROM native_condition_evidence WHERE exam_attempt_id=sub.exam_attempt_id) AS copies,
			(SELECT count(*) FROM exam_native_delivery_records r JOIN exam_attempt_security_owners o ON o.participation_id=r.participation_id WHERE o.exam_attempt_id=sub.exam_attempt_id) AS raw,
			b.security_retired_at IS NOT NULL AS retired, sub.integrity_retired_at IS NULL AS integrity_retained
			FROM exam_submissions sub JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=sub.exam_attempt_id WHERE sub.id=?`, id.String()); err != nil {
				t.Fatal(err)
			}
			if row.Copies != 1 || row.Raw != 0 || !row.Retired || !row.IntegrityRetained {
				t.Fatalf("independent native retirement = %#v", row)
			}
		},
	})
}

func TestRetentionBrowserEvidenceIndependence(t *testing.T) {
	s := openTestStore(t)
	resetTestStore(t, s)
	base := retentionSQLProbe(s)
	storetest.TestRetentionBrowserEvidenceIndependence(t, s, storetest.RetentionSubmissionPrivacyProbe{
		AgeCompletion: base.AgeCompletion, ExpireGrace: base.ExpireGrace,
		Inspect: func(*testing.T, context.Context, model.SubmissionID, bool) {},
		InspectBrowser: func(t *testing.T, ctx context.Context, id model.SubmissionID) {
			var row struct {
				Copies  int  `db:"copies"`
				History int  `db:"history"`
				Sources int  `db:"sources"`
				Retired bool `db:"retired"`
			}
			if err := s.GetMaster().Get(ctx, &row, `SELECT (SELECT count(*) FROM integrity_evidence WHERE exam_attempt_id=sub.exam_attempt_id AND browser_detail_canonical IS NOT NULL) AS copies,(SELECT count(*) FROM browser_activity_events WHERE exam_attempt_id=sub.exam_attempt_id) AS history,(SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=sub.exam_attempt_id) AS sources,b.browser_retired_at IS NOT NULL AS retired FROM exam_submissions sub JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=sub.exam_attempt_id WHERE sub.id=?`, id.String()); err != nil {
				t.Fatal(err)
			}
			if row.Copies != 1 || row.History != 0 || row.Sources != 0 || !row.Retired {
				t.Fatalf("Browser retention crossed evidence boundary: %#v", row)
			}
		},
	})
}
