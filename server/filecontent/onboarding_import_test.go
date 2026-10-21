// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package filecontent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestOnboardingImportContentStagesOpensAndRemovesPrivateObject(t *testing.T) {
	t.Parallel()
	content, err := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	id := model.NewOnboardingImportID()
	digest, size, err := content.StageOnboardingImport(context.Background(), id, strings.NewReader("email\na@example.edu\n"), 1024)
	require.NoError(t, err, "stage")
	require.Len(t, digest, 64, "stage")
	require.Equal(t, int64(len("email\na@example.edu\n")), size, "stage")
	opened, err := content.OpenOnboardingImport(context.Background(), id)
	require.NoError(t, err)
	body, err := io.ReadAll(opened)
	_ = opened.Close()
	require.NoError(t, err, "body")
	require.Equal(t, "email\na@example.edu\n", string(body), "body")
	err = content.RemoveOnboardingImport(context.Background(), id)
	require.NoError(t, err)
	err = content.RemoveOnboardingImport(context.Background(), id)
	require.NoError(t, err)
}

func TestOnboardingImportContentListsStaleFilesForOrphanReconciliation(t *testing.T) {
	t.Parallel()
	content, err := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	id := model.NewOnboardingImportID()
	_, _, err = content.StageOnboardingImport(context.Background(), id, strings.NewReader("email\na@example.edu\n"), 1024)
	require.NoError(t, err)
	ids, cursor, err := content.ListOnboardingImportFiles(context.Background(), "", 10, time.Now().Add(time.Hour))
	require.NoError(t, err, "staged files")
	require.Empty(t, cursor, "staged files")
	require.Len(t, ids, 1, "staged files")
	require.Equal(t, id, ids[0], "staged files")
}

func TestOnboardingImportContentDeletesOverLimitObject(t *testing.T) {
	t.Parallel()
	content, _ := New(memory.New(), Policy{MaximumConcurrentOperations: 2}, nil)
	id := model.NewOnboardingImportID()
	_, _, err := content.StageOnboardingImport(context.Background(), id, strings.NewReader("12345"), 4)
	require.ErrorIs(t, err, ErrOnboardingImportTooLarge)
	if opened, err := content.OpenOnboardingImport(context.Background(), id); err == nil {
		_ = opened.Close()
		require.FailNow(t, "over-limit object remained")
	}
}
