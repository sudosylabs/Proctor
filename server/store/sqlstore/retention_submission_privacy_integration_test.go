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

	"github.com/stretchr/testify/require"

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
			require.NoError(t, s.GetMaster().Get(ctx, &row, `SELECT browser_retained_records AS records,browser_retained_bytes AS bytes,browser_allocated_positions AS positions,control_metadata_bytes AS metadata,browser_retired_at IS NOT NULL AS browser,security_retired_at IS NOT NULL AS security FROM exam_attempt_delivery_budgets WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()))
			if !retired {
				retainedRecords, retainedBytes, positions, metadata = row.Records, row.Bytes, row.Positions, row.Metadata
				return
			}
			require.Equal(t, retainedRecords, row.Records)
			require.Equal(t, retainedBytes, row.Bytes)
			require.Equal(t, positions, row.Positions)
			require.Equal(t, metadata, row.Metadata)
			require.True(t, row.Browser)
			require.True(t, row.Security)
			for _, query := range []string{
				`SELECT count(*) FROM browser_activity_events WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`,
				`SELECT count(*) FROM exam_attempt_security_owners WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?) AND (octet_length(binding_canonical)>2 OR octet_length(latest_report_canonical)>2 OR control_body_canonical IS NOT NULL OR summary_canonical IS NOT NULL)`,
			} {
				var count int
				require.NoError(t, s.GetMaster().Get(ctx, &count, query, id.String()))
				require.Equal(t, 0, count, "private delivery payload survived retirement")
			}
			var stubs int
			require.NoError(t, s.GetMaster().Get(ctx, &stubs, `SELECT count(*) FROM browser_delivery_retired_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()), "minimal owner stubs")
			require.Equal(t, 2, stubs, "minimal owner stubs")
			_, err := s.GetMaster().Exec(ctx, `UPDATE exam_attempt_delivery_budgets SET browser_retired_at=NULL WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String())
			require.Error(t, err, "retirement marker removed")
		},
		Inspect: func(t *testing.T, ctx context.Context, id model.SubmissionID, retired bool) {
			t.Helper()
			var sources, privateOutcomes int
			require.NoError(t, s.GetMaster().Get(ctx, &sources, `SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()))
			require.NoError(t, s.GetMaster().Get(ctx, &privateOutcomes, `SELECT count(*) FROM command_outcomes o JOIN audit_events a ON a.id=o.original_audit_event_id WHERE a.resource_type='submission' AND a.resource_id=? AND o.outcome::text LIKE '%private-retirement-review-note%'`, id.String()))
			var nativeCopies, nativeDeliveryCopies int
			require.NoError(t, s.GetMaster().Get(ctx, &nativeCopies, `SELECT count(*) FROM native_condition_evidence WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?)`, id.String()))
			require.NoError(t, s.GetMaster().Get(ctx, &nativeDeliveryCopies, `SELECT count(*) FROM exam_native_delivery_records WHERE kind='occurrence' AND participation_id IN (SELECT participation_id FROM exam_attempt_security_owners WHERE exam_attempt_id=(SELECT exam_attempt_id FROM exam_submissions WHERE id=?))`, id.String()))
			expected := 1
			if retired {
				expected = 0
			}
			require.Equal(t, expected, nativeCopies, "native condition retirement copies")
			require.Equal(t, expected, nativeDeliveryCopies, "native condition retirement copies")
			if !retired {
				require.Equal(t, 2, sources, "fixture lacks source chain or replay copies")
				require.GreaterOrEqual(t, privateOutcomes, 1, "fixture lacks source chain or replay copies")
				return
			}
			require.Equal(t, 2, sources, "independent Browser source chain lost or retired Review copies survived")
			require.Equal(t, 0, privateOutcomes, "independent Browser source chain lost or retired Review copies survived")
			var redacted bool
			require.NoError(t, s.GetMaster().Get(ctx, &redacted, `SELECT integrity_retired_at IS NOT NULL AND integrity_state='retired'
				AND final_focus_loss_sequence IS NULL AND unresolved_integrity_count IS NULL
				AND work_retired_at IS NULL AND manifest_digest IS NOT NULL AND workspace_cursor IS NOT NULL
				AND manifest_entry_count IS NOT NULL AND manifest_total_file_bytes IS NOT NULL FROM exam_submissions WHERE id=?`, id.String()), "physical integrity-header redaction failed")
			require.True(t, redacted, "physical integrity-header redaction failed")
			_, err := s.GetMaster().Exec(ctx, `UPDATE exam_submissions SET integrity_state='settled',integrity_retired_at=NULL,final_focus_loss_sequence=0,unresolved_integrity_count=0 WHERE id=?`, id.String())
			require.Error(t, err, "retirement marker could be removed")
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
			require.NoError(t, s.GetMaster().Get(ctx, &row, `SELECT
			(SELECT count(*) FROM native_condition_evidence WHERE exam_attempt_id=sub.exam_attempt_id) AS copies,
			(SELECT count(*) FROM exam_native_delivery_records r JOIN exam_attempt_security_owners o ON o.participation_id=r.participation_id WHERE o.exam_attempt_id=sub.exam_attempt_id) AS raw,
			b.security_retired_at IS NOT NULL AS retired, sub.integrity_retired_at IS NULL AS integrity_retained
			FROM exam_submissions sub JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=sub.exam_attempt_id WHERE sub.id=?`, id.String()))
			require.Equal(t, 1, row.Copies)
			require.Equal(t, 0, row.Raw)
			require.True(t, row.Retired)
			require.True(t, row.IntegrityRetained)
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
			require.NoError(t, s.GetMaster().Get(ctx, &row, `SELECT (SELECT count(*) FROM integrity_evidence WHERE exam_attempt_id=sub.exam_attempt_id AND browser_detail_canonical IS NOT NULL) AS copies,(SELECT count(*) FROM browser_activity_events WHERE exam_attempt_id=sub.exam_attempt_id) AS history,(SELECT count(*) FROM browser_activity_sources WHERE exam_attempt_id=sub.exam_attempt_id) AS sources,b.browser_retired_at IS NOT NULL AS retired FROM exam_submissions sub JOIN exam_attempt_delivery_budgets b ON b.exam_attempt_id=sub.exam_attempt_id WHERE sub.id=?`, id.String()))
			require.Equal(t, 1, row.Copies)
			require.Equal(t, 0, row.History)
			require.Equal(t, 0, row.Sources)
			require.True(t, row.Retired)
		},
	})
}
