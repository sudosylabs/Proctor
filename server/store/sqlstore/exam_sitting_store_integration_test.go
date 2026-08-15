//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestExamSittingStoreAdapter(t *testing.T) {
	StoreTest(t, storetest.TestExamSittingStore)
}

func TestExamSittingStoreSQLGuards(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingStoreSQLGuards(t, persistence, storetest.ExamSittingSQLProbe{
		ArchiveProgrammeLevel: func(t *testing.T, ctx context.Context, id model.ProgrammeLevelID) {
			t.Helper()
			at := model.NowUTC().Add(time.Millisecond)
			result, err := persistence.GetMaster().Exec(ctx, `UPDATE programme_levels
				SET archived_at=?,updated_at=?,revision=revision+1 WHERE id=? AND archived_at IS NULL`, at, at, id.String())
			if err != nil {
				t.Fatal(err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				t.Fatalf("archive Programme Level affected=%d error=%v", affected, err)
			}
		},
	})
}

func TestExamSittingLifecycleStore(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	storetest.TestExamSittingLifecycleStore(t, persistence, storetest.ExamSittingLifecycleSQLProbe{
		SetSchedule: func(t *testing.T, ctx context.Context, id model.ExamSittingID, startAt, endAt time.Time) {
			t.Helper()
			// The shared fixture intentionally starts with a future Academic
			// Period so scheduling is valid. Widen that same Period when the
			// lifecycle probe advances PostgreSQL time across a boundary; this
			// keeps opening revalidation about lifecycle state, not fixture dates.
			if _, err := persistence.GetMaster().Exec(ctx, `UPDATE academic_periods ap
				SET start_at=LEAST(ap.start_at,?),end_at=GREATEST(ap.end_at,?)
				FROM classes c JOIN exam_sittings s ON s.class_id=c.id
				WHERE s.id=? AND ap.id=c.academic_period_id`, startAt.Add(-time.Hour), endAt.Add(time.Hour), id.String()); err != nil {
				t.Fatal(err)
			}
			result, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sittings SET scheduled_start_at=?,scheduled_end_at=? WHERE id=?`,
				startAt, endAt, id.String())
			if err != nil {
				t.Fatal(err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				t.Fatalf("set Sitting schedule affected=%d error=%v", affected, err)
			}
		},
		PrivateActions: func(t *testing.T, ctx context.Context, id model.ExamSittingID) []storetest.ExamSittingPrivateActionProbe {
			t.Helper()
			var rows []struct {
				ActionCode    string `db:"action_code"`
				PrivateReason string `db:"private_reason"`
				Revision      int64  `db:"sitting_revision"`
			}
			if err := persistence.GetMaster().Select(ctx, &rows, `SELECT action_code,private_reason,sitting_revision
				FROM exam_sitting_private_actions WHERE exam_sitting_id=? ORDER BY sitting_revision`, id.String()); err != nil {
				t.Fatal(err)
			}
			result := make([]storetest.ExamSittingPrivateActionProbe, len(rows))
			for index, row := range rows {
				result[index] = storetest.ExamSittingPrivateActionProbe{ActionCode: row.ActionCode,
					PrivateReason: row.PrivateReason, Revision: row.Revision}
			}
			return result
		},
		AssertAppendOnly: func(t *testing.T, ctx context.Context, id model.ExamSittingID) {
			t.Helper()
			if _, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sitting_private_actions SET private_reason='changed'
				WHERE exam_sitting_id=?`, id.String()); err == nil {
				t.Fatal("private action UPDATE unexpectedly succeeded")
			}
			if _, err := persistence.GetMaster().Exec(ctx, `DELETE FROM exam_sitting_private_actions WHERE exam_sitting_id=?`, id.String()); err == nil {
				t.Fatal("private action DELETE unexpectedly succeeded")
			}
		},
	})
	testExamSittingLifecycleDueBoundedPlan(t, persistence)
}
