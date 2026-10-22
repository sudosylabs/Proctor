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
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/packages/vfs"
	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAttemptWorkspaceContentNonConditionalStageReplaysExactBytesAndRejectsMismatch(t *testing.T) {
	t.Parallel()
	filesystem := &examResourceNonConditionalVFS{FileSystem: memory.New()}
	content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
	require.NoError(t, err)
	objectID := model.NewAttemptWorkspaceObjectID()
	first, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("same"), 4, "text/plain")
	require.NoError(t, err)
	replayed, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("same"), 4, "text/plain")
	require.NoError(t, err, "replay=%#v error", replayed)
	require.Equal(t, *first, *replayed, "replay=%#v error", replayed)
	_, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("evil"), 4, "text/plain")
	require.Error(t, err, "mismatched retry overwrote an existing nonconditional object")
	opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
	require.NoError(t, err)
	got, readErr := io.ReadAll(opened)
	_ = opened.Close()
	require.NoError(t, readErr, "bytes")
	require.Equal(t, "same", string(got), "bytes")
}

func TestAttemptWorkspaceContentStreamsBoundedReadsAndLeavesNoSpool(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, test := range []struct {
		name       string
		filesystem *examResourceWriteFailureVFS
		declared   int64
		wantError  bool
	}{
		{name: "write acknowledgement unknown with matching bytes", filesystem: &examResourceWriteFailureVFS{FileSystem: memory.New()}, declared: 1 << 20},
		{name: "write acknowledgement unknown with mismatched bytes", filesystem: &examResourceWriteFailureVFS{FileSystem: memory.New(), mismatch: true}, declared: 1 << 20, wantError: true},
		{name: "invalid declared size", filesystem: &examResourceWriteFailureVFS{FileSystem: memory.New()}, declared: (1 << 20) - 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := New(test.filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			reader := &boundedExamResourceReader{remaining: 1 << 20, maximumRequest: 32 << 10}
			_, err = content.StageAttemptWorkspaceObject(context.Background(), model.NewAttemptWorkspaceObjectID(), reader,
				test.declared, "application/octet-stream")
			require.Equal(t, test.wantError, err != nil, "error=%v maximum read", err)
			require.LessOrEqual(t, reader.maximumObserved, 32<<10, "error=%v maximum read", err)
			entries, readErr := os.ReadDir(temp)
			require.NoError(t, readErr, "temporary spools remain")
			require.Empty(t, entries, "temporary spools remain")
		})
	}
}

func TestAttemptWorkspaceContentStagesAndOpensOnlyTheExactOpaqueObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := New(filesystem, Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			objectID := model.AttemptWorkspaceObjectID("oooooooooooooooooooooooooo")
			body := []byte("package main\n")
			staged, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
			require.NoError(t, err)
			require.Equal(t, int64(13), staged.SizeBytes)
			require.Equal(t, "text/x-go", staged.MediaType)
			require.Equal(t, "df1d036cbbf3df46e2045071e082245ece204c7f53ecf0a4e022bff9bb228f47", staged.SHA256)
			opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
			require.NoError(t, err)
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			require.NoError(t, readErr, "read = %q", got)
			require.True(t, bytes.Equal(got, body), "read = %q", got)
			const key = "exam-attempt-workspace/oo/oo/objects/oooooooooooooooooooooooooo"
			_, err = filesystem.Stat(context.Background(), key)
			require.NoError(t, err)
			require.NotContains(t, key, "main.go", "logical path leaked into object key")
		})
	}
}

func TestAttemptWorkspaceContentBoundsUploadsAndPreservesTheFirstObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			oversizeID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), oversizeID, strings.NewReader("x"),
				model.AttemptWorkspaceMaximumFileBytes+1, "text/plain")
			var invalid *AttemptWorkspaceInvalidContentError
			require.ErrorAs(t, err, &invalid, "oversize error")
			_, err = content.OpenAttemptWorkspaceObject(context.Background(), oversizeID)
			require.True(t, IsNotFound(err), "oversize object became visible")

			objectID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("one"), 3, "text/plain")
			require.NoError(t, err)
			_, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("two"), 3, "text/plain")
			require.Error(t, err, "second stage overwrote an opaque object")
			opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
			require.NoError(t, err)
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			require.NoError(t, readErr, "preserved bytes")
			require.Equal(t, "one", string(got), "preserved bytes")
		})
	}
}

func TestAttemptWorkspaceContentRejectsSizeMismatchAndRemovesExactly(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			mismatchID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), mismatchID, strings.NewReader("two"), 1, "text/plain")
			var invalid *AttemptWorkspaceInvalidContentError
			require.ErrorAs(t, err, &invalid, "size mismatch error")
			_, err = content.OpenAttemptWorkspaceObject(context.Background(), mismatchID)
			require.True(t, IsNotFound(err), "mismatched object remained visible")

			removedID, retainedID := model.NewAttemptWorkspaceObjectID(), model.NewAttemptWorkspaceObjectID()
			for _, id := range []model.AttemptWorkspaceObjectID{removedID, retainedID} {
				_, err = content.StageAttemptWorkspaceObject(context.Background(), id, strings.NewReader("x"), 1, "text/plain")
				require.NoError(t, err)
			}
			err = content.RemoveAttemptWorkspaceObject(context.Background(), removedID)
			require.NoError(t, err)
			err = content.RemoveAttemptWorkspaceObject(context.Background(), removedID)
			require.NoError(t, err)
			if opened, openErr := content.OpenAttemptWorkspaceObject(context.Background(), retainedID); openErr != nil {
				require.NoError(t, openErr, "retained object")
			} else {
				_ = opened.Close()
			}
		})
	}
}

// tempDirectoryObserverVFS records the named entries of a temporary
// directory while a backend write is in progress.
type tempDirectoryObserverVFS struct {
	vfs.FileSystem
	directory string
	observed  []string
}

func (f *tempDirectoryObserverVFS) Write(ctx context.Context, path string, body io.Reader, options vfs.WriteOptions) (vfs.Info, error) {
	entries, err := os.ReadDir(f.directory)
	if err != nil {
		return vfs.Info{}, err
	}
	for _, entry := range entries {
		f.observed = append(f.observed, entry.Name())
	}
	return f.FileSystem.Write(ctx, path, body, options)
}

func TestAttemptWorkspaceContentNeverSpoolsCandidateBytesToLocalDisk(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, conditional := range []bool{true, false} {
		var filesystem vfs.FileSystem = memory.New()
		if !conditional {
			filesystem = &examResourceNonConditionalVFS{FileSystem: filesystem}
		}
		observer := &tempDirectoryObserverVFS{FileSystem: filesystem, directory: temp}
		content, err := New(observer, Policy{MaximumConcurrentOperations: 2}, nil)
		require.NoError(t, err)
		answer := strings.Repeat("candidate answer\n", 4096)
		staged, err := content.StageAttemptWorkspaceObject(context.Background(), model.NewAttemptWorkspaceObjectID(),
			strings.NewReader(answer), int64(len(answer)), "text/plain")
		require.NoError(t, err, "conditional=%t", conditional)
		require.Equal(t, int64(len(answer)), staged.SizeBytes, "conditional=%t: staged = %#v", conditional, staged)
		require.Empty(t, observer.observed, "conditional=%t: candidate bytes were spooled locally during the write", conditional)
	}
}

func TestAttemptWorkspaceContentRejectsShortBodyWithoutVisibleObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t), Policy{MaximumConcurrentOperations: 2}, nil)
			require.NoError(t, err)
			objectID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("ab"), 3, "text/plain")
			var invalid *AttemptWorkspaceInvalidContentError
			require.ErrorAs(t, err, &invalid, "short body error")
			_, err = content.OpenAttemptWorkspaceObject(context.Background(), objectID)
			require.True(t, IsNotFound(err), "short object became visible")
		})
	}
}
