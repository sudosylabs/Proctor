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
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestStarterWorkspaceContentStagesAndOpensOnlyTheExactOpaqueObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			objectID := model.StarterWorkspaceObjectID("oooooooooooooooooooooooooo")
			body := []byte("package main\n")
			staged, err := content.StageStarterWorkspaceObject(context.Background(), objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
			require.NoError(t, err)
			require.Equal(t, int64(len(body)), staged.SizeBytes)
			require.Equal(t, "text/x-go", staged.MediaType)
			require.Equal(t, "df1d036cbbf3df46e2045071e082245ece204c7f53ecf0a4e022bff9bb228f47", staged.SHA256)
			opened, err := content.OpenStarterWorkspaceObject(context.Background(), objectID)
			require.NoError(t, err)
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			require.NoError(t, readErr, "read = %q", got)
			require.True(t, bytes.Equal(got, body), "read = %q", got)
			const key = "exam-starter-workspace/oo/oo/objects/oooooooooooooooooooooooooo"
			_, err = filesystem.Stat(context.Background(), key)
			require.NoError(t, err)
			require.NotContains(t, key, "main.go", "logical path leaked into object key")
		})
	}
}

func TestStarterWorkspaceContentAcceptsEmptyFilesAndRejectsOversizeBeforePublication(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			emptyID := model.NewStarterWorkspaceObjectID()
			staged, err := content.StageStarterWorkspaceObject(context.Background(), emptyID, bytes.NewReader(nil), 0, "text/plain")
			require.NoError(t, err, "empty stage = %#v", staged)
			require.Equal(t, int64(0), staged.SizeBytes, "empty stage = %#v", staged)
			oversizeID := model.NewStarterWorkspaceObjectID()
			err = func() error {
				_, stageErr := content.StageStarterWorkspaceObject(context.Background(), oversizeID, strings.NewReader("x"), model.StarterWorkspaceMaximumFileBytes+1, "text/plain")
				return stageErr
			}()
			require.Error(t, err, "oversized declared content was accepted")
			var invalid *StarterWorkspaceInvalidContentError
			require.ErrorAs(t, err, &invalid, "oversized content error")
			_, err = content.OpenStarterWorkspaceObject(context.Background(), oversizeID)
			require.True(t, IsNotFound(err), "oversized content became visible")
		})
	}
}

func TestStarterWorkspaceContentClassifiesDeclaredSizeMismatchAsInvalid(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			objectID := model.NewStarterWorkspaceObjectID()
			_, err = content.StageStarterWorkspaceObject(context.Background(), objectID, strings.NewReader("two"), 1, "text/plain")
			var invalid *StarterWorkspaceInvalidContentError
			require.ErrorAs(t, err, &invalid, "size mismatch error")
			_, err = content.OpenStarterWorkspaceObject(context.Background(), objectID)
			require.True(t, IsNotFound(err), "mismatched content remained visible")
		})
	}
}

func TestStarterWorkspaceContentRemovalIsExactAndIdempotent(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			removedID, retainedID := model.NewStarterWorkspaceObjectID(), model.NewStarterWorkspaceObjectID()
			for _, id := range []model.StarterWorkspaceObjectID{removedID, retainedID} {
				_, err = content.StageStarterWorkspaceObject(context.Background(), id, bytes.NewReader([]byte("x")), 1, "text/plain")
				require.NoError(t, err)
			}
			err = content.RemoveStarterWorkspaceObject(context.Background(), removedID)
			require.NoError(t, err)
			err = content.RemoveStarterWorkspaceObject(context.Background(), removedID)
			require.NoError(t, err)
			if opened, openErr := content.OpenStarterWorkspaceObject(context.Background(), retainedID); openErr != nil {
				require.NoError(t, openErr, "retained object")
			} else {
				_ = opened.Close()
			}
		})
	}
}
