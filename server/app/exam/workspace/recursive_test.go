// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRecursiveRemovalBindsFlagToDraftCommand(t *testing.T) {
	t.Parallel()
	f := newServiceFixture(t)
	command := RemoveEntryCommand{ExamID: f.examID, EntryID: model.NewStarterWorkspaceEntryID(), ExpectedDraftRevision: 1, IdempotencyKey: "remove"}
	_, err := f.service.RemoveExamStarterWorkspaceEntry(context.Background(), f.invocation, command)
	require.NoError(t, err)
	ordinary := *f.persistence.idempotency
	command.Recursive = true
	_, err = f.service.RemoveExamStarterWorkspaceEntry(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.True(t, f.persistence.mutation.Recursive, "recursive removal lost its Draft precondition or semantic identity")
	require.Equal(t, int64(1), f.persistence.mutation.ExpectedDraftRevision, "recursive removal lost its Draft precondition or semantic identity")
	require.Equal(t, f.persistence.idempotency.KeyDigest, ordinary.KeyDigest, "recursive removal lost its Draft precondition or semantic identity")
	require.NotEqual(t, f.persistence.idempotency.Fingerprint, ordinary.Fingerprint, "recursive removal lost its Draft precondition or semantic identity")
}
