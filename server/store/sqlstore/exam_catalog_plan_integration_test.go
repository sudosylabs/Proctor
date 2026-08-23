//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
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
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "catalog-plan-unit", DisplayName: "Catalog Plan Unit"})
	if err != nil {
		t.Fatal(err)
	}
	otherUnit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "catalog-plan-other-unit", DisplayName: "Catalog Plan Other Unit"})
	if err != nil {
		t.Fatal(err)
	}
	actor := saveIntegrationUser(t, ctx, persistence, &model.User{Username: "catalog-plan-actor", Email: "catalog-plan@example.edu", DisplayName: "Catalog Plan Actor"})
	membershipAt := model.TimeUTC(time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC))
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: actor.ID, StartsAt: membershipAt.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	const examCount = 5000
	at := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	ids := make([]model.ExamID, examCount)
	tx, err := persistence.GetMaster().DB().BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exams, err := tx.Prepare(pq.CopyIn("exams", "id", "academic_unit_id", "creator_user_id", "owner_user_id", "created_at", "updated_at", "archived_at", "revision"))
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
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
			t.Fatal(err)
		}
	}
	if _, err := exams.Exec(); err != nil {
		_ = exams.Close()
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := exams.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	drafts, err := tx.Prepare(pq.CopyIn("exam_drafts", "exam_id", "title", "instructions_markdown", "policy", "updated_at", "revision"))
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
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
			t.Fatal(err)
		}
	}
	if _, err := drafts.Exec(); err != nil {
		_ = drafts.Close()
		_ = tx.Rollback()
		t.Fatalf("finish drafts copy: %v", err)
	}
	if err := drafts.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	managers, err := tx.Prepare(pq.CopyIn("exam_managers", "exam_id", "user_id", "granted_by_user_id", "granted_at"))
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := managers.Exec(id.String(), actor.ID.String(), actor.ID.String(), at); err != nil {
			_ = managers.Close()
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if _, err := managers.Exec(); err != nil {
		_ = managers.Close()
		_ = tx.Rollback()
		t.Fatalf("finish managers copy: %v", err)
	}
	if err := managers.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `ANALYZE exams; ANALYZE exam_drafts; ANALYZE exam_managers; ANALYZE academic_unit_members`); err != nil {
		t.Fatal(err)
	}

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
			Visibility: store.ExamListVisibility{ActorUserID: actor.ID, OverrideAcademicUnitRootIDs: []string{unit.ID.String()}},
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
			if err := persistence.GetMaster().Get(ctx, &encoded, `EXPLAIN (ANALYZE, FORMAT JSON) `+query, args...); err != nil {
				t.Fatal(err)
			}
			var documents []examCatalogPlanDocument
			if err := json.Unmarshal(encoded, &documents); err != nil || len(documents) != 1 {
				t.Fatalf("decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
			}
			if documents[0].Plan.NodeType != "Limit" {
				t.Fatalf("catalog root = %q, want Limit: %s", documents[0].Plan.NodeType, encoded)
			}
			indexNode, found := findAnyExamCatalogIndex(documents[0].Plan, test.wantIndexes)
			if !found {
				t.Fatalf("catalog plan lacks an ordered Exam index from %q: %s", test.wantIndexes, encoded)
			}
			if indexNode.ActualRows+indexNode.FilteredRows > float64(test.options.Limit*2) {
				t.Fatalf("ordered Exam index examined %.0f rows for limit %d: %s", indexNode.ActualRows+indexNode.FilteredRows, test.options.Limit, encoded)
			}
			if test.options.Visibility.OverrideInstitutionWide && examCatalogPlanScansAlias(documents[0].Plan, "actor_manager") {
				t.Fatalf("institution override unnecessarily scanned Exam Managers: %s", encoded)
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
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		// The 5,000-row fixture is intentionally small enough that PostgreSQL may
		// prefer a sequential scan. Disable it only in this transaction to prove
		// the production predicate is compatible with the trigram index.
		if _, err := tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			t.Fatal(err)
		}
		var encoded []byte
		if err := tx.GetContext(ctx, &encoded, tx.Rebind(`EXPLAIN (ANALYZE, FORMAT JSON) `+query), args...); err != nil {
			t.Fatal(err)
		}
		var documents []examCatalogPlanDocument
		if err := json.Unmarshal(encoded, &documents); err != nil || len(documents) != 1 {
			t.Fatalf("decode plan: documents=%d err=%v plan=%s", len(documents), err, encoded)
		}
		if _, found := findExamCatalogIndex(documents[0].Plan, "exam_drafts_title_search_idx"); !found {
			t.Fatalf("title search plan lacks trigram index: %s", encoded)
		}
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
