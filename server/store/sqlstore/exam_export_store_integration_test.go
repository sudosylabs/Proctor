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
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestExamExportStore(t *testing.T) {
	persistence := openTestStore(t)
	peer := openTestStore(t)
	for _, test := range []struct {
		name     string
		decorate func(*testing.T, *SQLStore) store.Store
	}{
		{"SQL", func(_ *testing.T, s *SQLStore) store.Store { return s }},
		{"LocalCache", newLocalCacheConformanceStore}, {"Retry", newRetryConformanceStore}, {"Timer", newTimerConformanceStore},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetTestStore(t, persistence)
			storetest.TestExamExportStore(t, test.decorate(t, persistence), storetest.ExamExportSQLProbe{Peer: peer.ExamExport(),
				SetPolicy: func(t *testing.T, ctx context.Context, days int) {
					t.Helper()
					if _, err := persistence.GetMaster().Exec(ctx, `UPDATE retention_policies SET export_retention_days=?`, days); err != nil {
						t.Fatal(err)
					}
				},
				SourceProtectionCount: func(t *testing.T, ctx context.Context, id model.ExamExportID) int {
					t.Helper()
					var count int
					if err := persistence.GetMaster().Get(ctx, &count, `SELECT count(*) FROM retention_source_protections WHERE export_id=?`, id.String()); err != nil {
						t.Fatal(err)
					}
					return count
				},
				Expire: func(t *testing.T, ctx context.Context, id model.ExamExportID) {
					t.Helper()
					if _, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_exports SET created_at=created_at-interval '2 days',expires_at=expires_at-interval '2 days',source_expires_at=source_expires_at-interval '2 days',ready_at=ready_at-interval '2 days' WHERE id=?`, id.String()); err != nil {
						t.Fatal(err)
					}
				},
				ArtifactCount: func(t *testing.T, ctx context.Context, id model.ExamExportID) int {
					t.Helper()
					var count int
					if err := persistence.GetMaster().Get(ctx, &count, `SELECT count(*) FROM exam_export_artifacts WHERE export_id=?`, id.String()); err != nil {
						t.Fatal(err)
					}
					return count
				},
			})
		})
	}
}

func TestExamExportRetention(t *testing.T) {
	persistence := openTestStore(t)
	peer := openTestStore(t)
	for _, test := range []struct {
		name     string
		decorate func(*testing.T, *SQLStore) store.Store
	}{
		{"SQL", func(_ *testing.T, s *SQLStore) store.Store { return s }},
		{"LocalCache", newLocalCacheConformanceStore}, {"Retry", newRetryConformanceStore}, {"Timer", newTimerConformanceStore},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetTestStore(t, persistence)
			storetest.TestExamExportRetention(t, test.decorate(t, persistence), retentionSQLProbe(persistence), storetest.ExamExportRetentionSQLProbe{
				Peer: peer.Retention(),
				SeedExpiryGrace: func(t *testing.T, ctx context.Context, scope model.RetentionHoldScope) string {
					t.Helper()
					var id string
					if err := persistence.GetMaster().Get(ctx, &id, `SELECT id FROM audit_events WHERE resource_type='submission' AND resource_id=? AND status='success' ORDER BY id LIMIT 1`, scope.SubmissionID.String()); err != nil {
						t.Fatal(err)
					}
					if _, err := persistence.GetMaster().Exec(ctx, `INSERT INTO retention_expiry_schedules(record_kind,record_id,policy_revision,control_revision,scheduled_at,expires_after) VALUES ('audit',?,1,1,clock_timestamp()-interval '2 days',clock_timestamp()-interval '1 day')`, id); err != nil {
						t.Fatal(err)
					}
					return id
				},
				HasExpiryGrace: func(t *testing.T, ctx context.Context, id string) bool {
					t.Helper()
					var found bool
					if err := persistence.GetMaster().Get(ctx, &found, `SELECT EXISTS(SELECT 1 FROM retention_expiry_schedules WHERE record_kind='audit' AND record_id=?)`, id); err != nil {
						t.Fatal(err)
					}
					return found
				},
			})
		})
	}
}
