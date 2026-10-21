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
	"encoding/json"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func testExamSittingLifecycleDueBoundedPlan(t *testing.T, persistence *SQLStore) {
	t.Helper()
	ctx := context.Background()
	var lineage struct {
		ExamID         string `db:"exam_id"`
		ExamRevisionID string `db:"exam_revision_id"`
		ClassID        string `db:"class_id"`
	}
	require.NoError(t, persistence.GetMaster().Get(ctx, &lineage, `SELECT exam_id,exam_revision_id,class_id FROM exam_sittings LIMIT 1`))

	const rowsPerBranch = 1500
	base := model.NowUTC().Add(-2 * time.Hour)
	tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
	require.NoError(t, err)
	copyRows, err := tx.Prepare(pq.CopyIn("exam_sittings", "id", "exam_id", "exam_revision_id", "class_id",
		"scheduled_start_at", "scheduled_end_at", "state", "created_at", "updated_at", "opened_at", "paused_at",
		"closing_at", "closed_at", "canceled_at", "reason_code", "revision"))
	if err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	insert := func(state model.ExamSittingState, dueAt time.Time) {
		t.Helper()
		createdAt := dueAt.Add(-4 * time.Hour)
		startAt, endAt := dueAt.Add(-2*time.Hour), dueAt
		var openedAt, pausedAt, closingAt, reason any
		switch state {
		case model.ExamSittingScheduled:
			startAt, endAt = dueAt, dueAt.Add(4*time.Hour)
		case model.ExamSittingOpen:
			openedAt = startAt
		case model.ExamSittingPaused:
			openedAt, pausedAt = startAt, dueAt.Add(-time.Minute)
		case model.ExamSittingClosing:
			openedAt, closingAt, reason = startAt, dueAt, model.ExamSittingReasonScheduledEndReached
		}
		if _, copyErr := copyRows.Exec(model.NewExamSittingID().String(), lineage.ExamID, lineage.ExamRevisionID, lineage.ClassID,
			startAt, endAt, state, createdAt, dueAt, openedAt, pausedAt, closingAt, nil, nil, reason, 1); copyErr != nil {
			_ = copyRows.Close()
			_ = tx.Rollback()
			require.NoError(t, copyErr)
		}
	}
	for index := 0; index < rowsPerBranch; index++ {
		dueAt := base.Add(time.Duration(index) * time.Millisecond)
		insert(model.ExamSittingScheduled, dueAt)
		if index%2 == 0 {
			insert(model.ExamSittingOpen, dueAt)
		} else {
			insert(model.ExamSittingPaused, dueAt)
		}
		insert(model.ExamSittingClosing, dueAt)
	}
	if _, err = copyRows.Exec(); err != nil {
		_ = copyRows.Close()
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	if err = copyRows.Close(); err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	err = tx.Commit()
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `ANALYZE exam_sittings`)
	require.NoError(t, err)

	options := store.ExamSittingLifecycleDueOptions{
		AfterDueAt:     base.Add(750 * time.Millisecond),
		AfterSittingID: model.NewExamSittingID(),
		Limit:          20,
	}
	query, args := examSittingLifecycleDueQuery(options)
	var encoded []byte
	err = persistence.GetMaster().Get(ctx, &encoded, `EXPLAIN (ANALYZE, FORMAT JSON) `+query, args...)
	require.NoError(t, err)
	var documents []examCatalogPlanDocument
	err = json.Unmarshal(encoded, &documents)
	require.NoError(t, err, "decode lifecycle due plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
	require.Len(t, documents, 1, "decode lifecycle due plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
	require.Equal(t, "Limit", documents[0].Plan.NodeType, "lifecycle due root")
	for _, indexName := range []string{
		"exam_sittings_lifecycle_open_due_idx",
		"exam_sittings_lifecycle_deadline_due_idx",
		"exam_sittings_lifecycle_closing_due_idx",
	} {
		indexNode, found := findExamCatalogIndex(documents[0].Plan, indexName)
		require.True(t, found, "lifecycle due plan lacks %s: %s", indexName, encoded)
		examined := indexNode.ActualRows + indexNode.FilteredRows
		require.LessOrEqual(t, examined, float64(options.Limit*2), "%s examined %.0f rows for branch limit %d: %s", indexName, examined, options.Limit, encoded)
	}
}
