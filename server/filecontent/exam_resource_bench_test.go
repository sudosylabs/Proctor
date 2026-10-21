// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func BenchmarkStoreExamResourcePNG512(b *testing.B) {
	input := benchmarkContentPNG512(b)
	content, err := New(memoryvfs.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(b, err)
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		revisionID := model.NewFileRevisionID()
		rendition, err := content.StoreExamResource(ctx, revisionID, model.ExamResourceMediaPNG, bytes.NewReader(input), int64(len(input)), time.Unix(1, 0))
		require.NoError(b, err)
		b.StopTimer()
		require.NoError(b, content.RemoveExamResource(ctx, revisionID, rendition.ID))
		b.StartTimer()
	}
}
