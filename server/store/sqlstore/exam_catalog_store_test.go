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
	require.NoError(t, err)
	require.Empty(t, items, "items/query count")
	require.Equal(t, 1, reader.calls, "items/query count")
	require.NotContains(t, reader.query, "visible_exams")
	require.NotContains(t, reader.query, " UNION ")
	require.Contains(t, reader.query, "FROM exams e JOIN exam_drafts")
	require.Contains(t, reader.query, "d.title ILIKE ? ESCAPE '!'")
	require.Contains(t, reader.query, "ORDER BY e.updated_at DESC, e.id DESC LIMIT")
}
