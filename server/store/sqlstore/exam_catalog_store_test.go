// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examCatalogReaderFake struct {
	calls int
	query string
}

func (f *examCatalogReaderFake) Select(_ context.Context, destination any, query string, _ ...any) error {
	f.calls++
	f.query = query
	*destination.(*[]examSummaryRow) = []examSummaryRow{}
	return nil
}

func TestExamCatalogListUsesOneExamDrivenQuery(t *testing.T) {
	t.Parallel()
	reader := &examCatalogReaderFake{}
	adapter := SQLExamAuthoringStore{catalogReader: reader}
	items, err := adapter.List(context.Background(), store.ExamListOptions{
		ArchiveFilter: store.ExamArchiveActive,
		Query:         "Systems",
		Limit:         20,
		Visibility: store.ExamListVisibility{
			ActorUserID: model.NewUserID(), OverrideInstitutionWide: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || reader.calls != 1 {
		t.Fatalf("items/query count = %d/%d, want 0/1", len(items), reader.calls)
	}
	if strings.Contains(reader.query, "visible_exams") || strings.Contains(reader.query, " UNION ") ||
		!strings.Contains(reader.query, "FROM exams e JOIN exam_drafts") ||
		!strings.Contains(reader.query, "d.title ILIKE ? ESCAPE '!'") ||
		!strings.Contains(reader.query, "ORDER BY e.updated_at DESC, e.id DESC LIMIT") {
		t.Fatalf("catalog query is not Exam-driven and bounded:\n%s", reader.query)
	}
}
