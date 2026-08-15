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
