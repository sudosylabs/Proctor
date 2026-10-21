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
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

type examCatalogPlanDocument struct {
	Plan examCatalogPlanNode `json:"Plan"`
}

type examCatalogPlanNode struct {
	NodeType     string                `json:"Node Type"`
	IndexName    string                `json:"Index Name"`
	RelationName string                `json:"Relation Name"`
	Alias        string                `json:"Alias"`
	ActualRows   float64               `json:"Actual Rows"`
	FilteredRows float64               `json:"Rows Removed by Filter"`
	Plans        []examCatalogPlanNode `json:"Plans"`
}

func testExamCatalogBoundedPlan(t *testing.T, persistence *SQLStore) {
	t.Helper()
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "catalog-plan", DisplayName: "Catalog Plan"})
	require.NoError(t, err)
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "catalog-plan-unit", DisplayName: "Catalog Plan Unit"})
	require.NoError(t, err)
	otherUnit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: institution.ID, Name: "catalog-plan-other-unit", DisplayName: "Catalog Plan Other Unit"})
	require.NoError(t, err)
	actor := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "catalog-plan-actor", Email: "catalog-plan@example.edu", DisplayName: "Catalog Plan Actor"})
	membershipAt := model.TimeUTC(time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC))
	_, err = persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: actor.ID, StartsAt: membershipAt.Add(-time.Hour),
	})
	require.NoError(t, err)

	const examCount = 5000
	at := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	ids := make([]model.ExamID, examCount)
	tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
	require.NoError(t, err)
	exams, err := tx.Prepare(pq.CopyIn("exams", "id", "academic_unit_id", "creator_user_id", "owner_user_id", "created_at", "updated_at", "archived_at", "revision"))
	if err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	for index := range ids {
		ids[index] = model.NewExamID()
		updatedAt := at.Add(time.Duration(index) * time.Millisecond)
		academicUnitID := unit.ID
		if index%2 != 0 {
			academicUnitID = otherUnit.ID
		}
		var archivedAt any
		if index%5 == 0 {
			archivedAt = updatedAt
		}
		if _, err := exams.Exec(ids[index].String(), academicUnitID.String(), actor.ID.String(), actor.ID.String(), at, updatedAt, archivedAt, 1); err != nil {
			_ = exams.Close()
			_ = tx.Rollback()
			require.NoError(t, err)
		}
	}
	if _, err := exams.Exec(); err != nil {
		_ = exams.Close()
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	if err := exams.Close(); err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	drafts, err := tx.Prepare(pq.CopyIn("exam_drafts", "exam_id", "title", "instructions_markdown", "policy", "updated_at", "revision"))
	if err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	for index, id := range ids {
		updatedAt := at.Add(time.Duration(index) * time.Millisecond)
		title := "Catalog plan"
		if index%100 == 0 {
			title = fmt.Sprintf("Needle catalog %04d", index)
		}
		if _, err := drafts.Exec(id.String(), title, "", `{}`, updatedAt, 1); err != nil {
			_ = drafts.Close()
			_ = tx.Rollback()
			require.NoError(t, err)
		}
	}
	if _, err := drafts.Exec(); err != nil {
		_ = drafts.Close()
		_ = tx.Rollback()
		require.NoError(t, err, "finish drafts copy")
	}
	if err := drafts.Close(); err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	managers, err := tx.Prepare(pq.CopyIn("exam_managers", "exam_id", "user_id", "granted_by_user_id", "granted_at"))
	if err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	for _, id := range ids {
		if _, err := managers.Exec(id.String(), actor.ID.String(), actor.ID.String(), at); err != nil {
			_ = managers.Close()
			_ = tx.Rollback()
			require.NoError(t, err)
		}
	}
	if _, err := managers.Exec(); err != nil {
		_ = managers.Close()
		_ = tx.Rollback()
		require.NoError(t, err, "finish managers copy")
	}
	if err := managers.Close(); err != nil {
		_ = tx.Rollback()
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	// Flush the bulk COPY pending list before measuring the settled GIN index.
	// ANALYZE alone leaves pending pages in place; their scan cost can make a
	// full primary-key scan cheaper even when the title predicate is selective.
	_, err = persistence.GetMaster().Exec(ctx, `SELECT gin_clean_pending_list('exam_drafts_title_search_idx')`)
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `ANALYZE exams; ANALYZE exam_drafts; ANALYZE exam_managers; ANALYZE academic_unit_members`)
	require.NoError(t, err)

	tests := []struct {
		name        string
		options     store.ExamListOptions
		wantIndexes []string
	}{
		{name: "active global override keyset", options: store.ExamListOptions{
			ArchiveFilter: store.ExamArchiveActive, Limit: 20, BeforeUpdatedAt: at.Add(4001 * time.Millisecond), BeforeExamID: ids[4001],
			Visibility: store.ExamListVisibility{ActorUserID: actor.ID, OverrideInstitutionWide: true},
		}, wantIndexes: []string{"exams_active_updated_at_id_idx"}},
		{name: "archived exact unit override keyset", options: store.ExamListOptions{
			AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveArchived, Limit: 20, BeforeUpdatedAt: at.Add(4000 * time.Millisecond), BeforeExamID: ids[4000],
			Visibility: store.ExamListVisibility{ActorUserID: actor.ID, OverrideAcademicUnitRootIDs: []model.AcademicUnitID{unit.ID}},
		}, wantIndexes: []string{"exams_archived_academic_unit_updated_at_id_idx", "exams_archived_updated_at_id_idx"}},
		{name: "all exact unit ordinary", options: store.ExamListOptions{
			AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveAll, Limit: 20,
			Visibility: store.ExamListVisibility{ActorUserID: actor.ID, OrdinaryInstitutionWide: true, OrdinaryMembershipAt: membershipAt},
		}, wantIndexes: []string{"exams_academic_unit_id_updated_at_id_idx", "exams_updated_at_id_idx"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, args := examCatalogQuery(test.options)
			var encoded []byte
			require.NoError(t, persistence.GetMaster().Get(ctx, &encoded, `EXPLAIN (ANALYZE, FORMAT JSON) `+query, args...))
			var documents []examCatalogPlanDocument
			err := json.Unmarshal(encoded, &documents)
			require.NoError(t, err, "decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
			require.Len(t, documents, 1, "decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
			require.Equal(t, "Limit", documents[0].Plan.NodeType, "catalog root")
			indexNode, found := findAnyExamCatalogIndex(documents[0].Plan, test.wantIndexes)
			require.True(t, found, "catalog plan lacks an ordered Exam index from %q: %s", test.wantIndexes, encoded)
			require.LessOrEqual(t, indexNode.ActualRows+indexNode.FilteredRows, float64(test.options.Limit*2), "ordered Exam index examined %.0f rows for limit %d: %s", indexNode.ActualRows+indexNode.FilteredRows, test.options.Limit, encoded)
			if test.options.Visibility.OverrideInstitutionWide {
				require.False(t, examCatalogPlanScansAlias(documents[0].Plan, "actor_manager"), "institution override unnecessarily scanned Exam Managers: %s", encoded)
			}
		})
	}

	t.Run("selective title search supports trigram plan", func(t *testing.T) {
		options := store.ExamListOptions{
			Query: "needle catalog 4900", ArchiveFilter: store.ExamArchiveAll, Limit: 20,
			Visibility: store.ExamListVisibility{ActorUserID: actor.ID, OverrideInstitutionWide: true},
		}
		query, args := examCatalogQuery(options)
		tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		// The 5,000-row fixture is intentionally small enough that PostgreSQL may
		// prefer a sequential scan. Disable it only in this transaction to prove
		// the production predicate is compatible with the trigram index.
		_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`)
		require.NoError(t, err)
		var encoded []byte
		require.NoError(t, tx.GetContext(ctx, &encoded, tx.Rebind(`EXPLAIN (ANALYZE, FORMAT JSON) `+query), args...))
		var documents []examCatalogPlanDocument
		err = json.Unmarshal(encoded, &documents)
		require.NoError(t, err, "decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
		require.Len(t, documents, 1, "decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
		_, found := findExamCatalogIndex(documents[0].Plan, "exam_drafts_title_search_idx")
		require.True(t, found, "title search plan lacks trigram index: %s", encoded)
	})
}

func examCatalogPlanScansAlias(node examCatalogPlanNode, alias string) bool {
	if node.Alias == alias {
		return true
	}
	for _, child := range node.Plans {
		if examCatalogPlanScansAlias(child, alias) {
			return true
		}
	}
	return false
}

func findAnyExamCatalogIndex(node examCatalogPlanNode, names []string) (examCatalogPlanNode, bool) {
	for _, name := range names {
		if found, ok := findExamCatalogIndex(node, name); ok {
			return found, true
		}
	}
	return examCatalogPlanNode{}, false
}

func findExamCatalogIndex(node examCatalogPlanNode, name string) (examCatalogPlanNode, bool) {
	if node.IndexName == name {
		return node, true
	}
	for _, child := range node.Plans {
		if found, ok := findExamCatalogIndex(child, name); ok {
			return found, true
		}
	}
	return examCatalogPlanNode{}, false
}
