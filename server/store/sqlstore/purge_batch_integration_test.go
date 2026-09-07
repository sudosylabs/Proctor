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
	"database/sql"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestPurgeBatchProgress(t *testing.T) {
	persistence, peer := openTestStore(t), openTestStore(t)
	for _, test := range []struct {
		name     string
		decorate func(*testing.T, *SQLStore) store.Store
	}{
		{"SQL", func(_ *testing.T, s *SQLStore) store.Store { return s }},
		{"LocalCache", newLocalCacheConformanceStore}, {"Retry", newRetryConformanceStore}, {"Timer", newTimerConformanceStore},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetTestStore(t, persistence)
			decorated := test.decorate(t, persistence)
			// Establish retirement through the full ordinary audited workflow.
			// Additional exact-key queues below exercise cleanup scale only.
			storetest.TestRetentionStore(t, decorated, retentionSQLProbe(persistence))
			t.Run("retention", func(t *testing.T) {
				storetest.TestPurgeBatchProgress(t, retentionPurgeBatchProbe(persistence, decorated.Retention(), peer.Retention()))
			})
			t.Run("export", func(t *testing.T) {
				storetest.TestPurgeBatchProgress(t, exportPurgeBatchProbe(persistence, decorated.ExamExport(), peer.ExamExport()))
			})
		})
	}
}

func retentionPurgeBatchProbe(s *SQLStore, current, peer store.RetentionStore) storetest.PurgeBatchProbe[store.RetentionPurgeObject] {
	return storetest.PurgeBatchProbe[store.RetentionPurgeObject]{
		Begin: current.BeginPurgeBatch, PeerBegin: peer.BeginPurgeBatch, MaximumBatch: model.RetentionMaximumPageSize,
		Complete: func(ctx context.Context, key store.RetentionPurgeObject) error {
			return current.CompletePurge(ctx, &store.RetentionPurgeCompletion{RetirementID: key.RetirementID, ObjectID: key.ObjectID})
		},
		Seed: func(t *testing.T, ctx context.Context, count int, unknownLast bool) []store.RetentionPurgeObject {
			t.Helper()
			var retirement string
			if err := s.GetMaster().Get(ctx, &retirement, `SELECT id FROM retention_retirements WHERE category='work' AND state='retired' ORDER BY id LIMIT 1`); err != nil {
				t.Fatal(err)
			}
			keys := make([]store.RetentionPurgeObject, count)
			for i := range keys {
				key := store.RetentionPurgeObject{RetirementID: model.RetentionRetirementID(retirement), ObjectID: model.NewAttemptWorkspaceObjectID()}
				_, err := s.GetMaster().Exec(ctx, `INSERT INTO retention_purge_objects(retirement_id,object_id,writer_finished,verify_after)
					VALUES(?,?,?,statement_timestamp()-interval '10 days'+?*interval '1 second')`, key.RetirementID.String(), key.ObjectID.String(), !unknownLast || i != count-1, i)
				if err != nil {
					t.Fatal(err)
				}
				keys[i] = key
			}
			return keys
		},
		Observe: func(t *testing.T, ctx context.Context, key store.RetentionPurgeObject) storetest.PurgeBatchObservation {
			t.Helper()
			var row struct {
				Observed bool `db:"observed"`
				Verified bool `db:"verified"`
				Deferred bool `db:"deferred"`
			}
			err := s.GetMaster().Get(ctx, &row, `SELECT last_absence_observed_at IS NOT NULL AS observed,absence_verified_at IS NOT NULL AS verified,verify_after>clock_timestamp() AS deferred
				FROM retention_purge_objects WHERE retirement_id=? AND object_id=?`, key.RetirementID.String(), key.ObjectID.String())
			if err != nil {
				t.Fatal(err)
			}
			return storetest.PurgeBatchObservation{Exists: true, ObservedAbsent: row.Observed, VerifiedAbsent: row.Verified, NextAttemptDeferred: row.Deferred}
		},
		RetryNow: func(t *testing.T, ctx context.Context, keys []store.RetentionPurgeObject) {
			t.Helper()
			for _, key := range keys {
				if _, err := s.GetMaster().Exec(ctx, `UPDATE retention_purge_objects SET verify_after=clock_timestamp()-interval '1 second' WHERE retirement_id=? AND object_id=?`, key.RetirementID.String(), key.ObjectID.String()); err != nil {
					t.Fatal(err)
				}
			}
		},
		FinishWriter: func(t *testing.T, ctx context.Context, key store.RetentionPurgeObject) {
			t.Helper()
			if _, err := s.GetMaster().Exec(ctx, `UPDATE retention_purge_objects SET writer_finished=true WHERE retirement_id=? AND object_id=?`, key.RetirementID.String(), key.ObjectID.String()); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func exportPurgeBatchProbe(s *SQLStore, current, peer store.ExamExportStore) storetest.PurgeBatchProbe[store.ExamExportArtifact] {
	return storetest.PurgeBatchProbe[store.ExamExportArtifact]{
		Begin: current.BeginPurgeBatch, PeerBegin: peer.BeginPurgeBatch, Complete: current.CompletePurge, MaximumBatch: 100,
		Seed: func(t *testing.T, ctx context.Context, count int, unknownLast bool) []store.ExamExportArtifact {
			t.Helper()
			keys := make([]store.ExamExportArtifact, count)
			for i := range keys {
				key := store.ExamExportArtifact{ExportID: model.NewExamExportID(), AttemptID: model.NewJobAttemptID()}
				// Expired metadata may outlive its finite Job and source records.
				// One artifact per export respects the ordinary construction bound.
				_, err := s.GetMaster().Exec(ctx, `INSERT INTO exam_exports(id,exam_id,exam_sitting_id,submission_id,requester_user_id,categories,state,policy_revision,
					created_at,expires_at,source_expires_at,submission_count,file_count,source_bytes,job_id)
					SELECT ?,r.exam_id,r.exam_sitting_id,r.submission_id,a.candidate_user_id,ARRAY['work'],'expired',r.policy_revision,
					statement_timestamp()-interval '2 days',statement_timestamp()-interval '1 day',statement_timestamp()-interval '1 day',1,0,0,?
					FROM retention_retirements r JOIN exam_submissions sub ON sub.id=r.submission_id JOIN exam_attempts a ON a.id=sub.exam_attempt_id
					WHERE r.category='work' AND r.state='retired' ORDER BY r.id LIMIT 1`, key.ExportID.String(), model.NewJobID().String())
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.GetMaster().Exec(ctx, `INSERT INTO exam_export_artifacts(export_id,attempt_id,created_at,writer_finished,reconcile_after)
					VALUES(?,?,statement_timestamp()-interval '2 days',?,statement_timestamp()-interval '10 days'+?*interval '1 second')`, key.ExportID.String(), key.AttemptID.String(), !unknownLast || i != count-1, i)
				if err != nil {
					t.Fatal(err)
				}
				keys[i] = key
			}
			return keys
		},
		Observe: func(t *testing.T, ctx context.Context, key store.ExamExportArtifact) storetest.PurgeBatchObservation {
			t.Helper()
			var row struct {
				Observed bool `db:"observed"`
				Deferred bool `db:"deferred"`
			}
			err := s.GetMaster().Get(ctx, &row, `SELECT observed_absent_at IS NOT NULL AS observed,reconcile_after>clock_timestamp() AS deferred FROM exam_export_artifacts WHERE export_id=? AND attempt_id=?`, key.ExportID.String(), key.AttemptID.String())
			if errors.Is(err, sql.ErrNoRows) {
				return storetest.PurgeBatchObservation{}
			}
			if err != nil {
				t.Fatal(err)
			}
			return storetest.PurgeBatchObservation{Exists: true, ObservedAbsent: row.Observed, NextAttemptDeferred: row.Deferred}
		},
		RetryNow: func(t *testing.T, ctx context.Context, keys []store.ExamExportArtifact) {
			t.Helper()
			for _, key := range keys {
				if _, err := s.GetMaster().Exec(ctx, `UPDATE exam_export_artifacts SET reconcile_after=clock_timestamp()-interval '1 second' WHERE export_id=? AND attempt_id=?`, key.ExportID.String(), key.AttemptID.String()); err != nil {
					t.Fatal(err)
				}
			}
		},
		FinishWriter: func(t *testing.T, ctx context.Context, key store.ExamExportArtifact) {
			t.Helper()
			if err := current.FinishWriter(ctx, key); err != nil {
				t.Fatal(err)
			}
		},
	}
}
