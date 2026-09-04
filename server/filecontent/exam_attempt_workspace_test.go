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
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	memoryvfs "github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAttemptWorkspaceContentNonConditionalStageReplaysExactBytesAndRejectsMismatch(t *testing.T) {
	t.Parallel()
	filesystem := &examResourceNonConditionalVFS{FileSystem: memoryvfs.New()}
	content, err := New(filesystem)
	if err != nil {
		t.Fatal(err)
	}
	objectID := model.NewAttemptWorkspaceObjectID()
	first, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("same"), 4, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("same"), 4, "text/plain")
	if err != nil || *replayed != *first {
		t.Fatalf("replay=%#v error=%v", replayed, err)
	}
	if _, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("evil"), 4, "text/plain"); err == nil {
		t.Fatal("mismatched retry overwrote an existing nonconditional object")
	}
	opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(opened)
	_ = opened.Close()
	if readErr != nil || string(got) != "same" {
		t.Fatalf("bytes=%q error=%v", got, readErr)
	}
}

func TestAttemptWorkspaceContentUsesBoundedPrivateSpoolAndCleansEveryOutcome(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, test := range []struct {
		name       string
		filesystem *examResourceWriteFailureVFS
		declared   int64
		wantError  bool
	}{
		{name: "write acknowledgement unknown with matching bytes", filesystem: &examResourceWriteFailureVFS{FileSystem: memoryvfs.New()}, declared: 1 << 20},
		{name: "write acknowledgement unknown with mismatched bytes", filesystem: &examResourceWriteFailureVFS{FileSystem: memoryvfs.New(), mismatch: true}, declared: 1 << 20, wantError: true},
		{name: "invalid declared size", filesystem: &examResourceWriteFailureVFS{FileSystem: memoryvfs.New()}, declared: (1 << 20) - 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := New(test.filesystem)
			if err != nil {
				t.Fatal(err)
			}
			reader := &boundedExamResourceReader{remaining: 1 << 20, maximumRequest: 32 << 10}
			_, err = content.StageAttemptWorkspaceObject(context.Background(), model.NewAttemptWorkspaceObjectID(), reader,
				test.declared, "application/octet-stream")
			if (err != nil) != test.wantError || reader.maximumObserved > 32<<10 {
				t.Fatalf("error=%v maximum read=%d", err, reader.maximumObserved)
			}
			entries, readErr := os.ReadDir(temp)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("temporary spools remain: %#v, %v", entries, readErr)
			}
		})
	}
}

func TestAttemptWorkspaceContentStagesAndOpensOnlyTheExactOpaqueObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := New(filesystem)
			if err != nil {
				t.Fatal(err)
			}
			objectID := model.AttemptWorkspaceObjectID("oooooooooooooooooooooooooo")
			body := []byte("package main\n")
			staged, err := content.StageAttemptWorkspaceObject(context.Background(), objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
			if err != nil {
				t.Fatalf("stage: %v", err)
			}
			if staged.SizeBytes != 13 || staged.MediaType != "text/x-go" ||
				staged.SHA256 != "df1d036cbbf3df46e2045071e082245ece204c7f53ecf0a4e022bff9bb228f47" {
				t.Fatalf("staged metadata = %#v", staged)
			}
			opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			if readErr != nil || !bytes.Equal(got, body) {
				t.Fatalf("read = %q, %v", got, readErr)
			}
			const key = "exam-attempt-workspace/oo/oo/objects/oooooooooooooooooooooooooo"
			if _, err = filesystem.Stat(context.Background(), key); err != nil {
				t.Fatalf("opaque key missing: %v", err)
			}
			if strings.Contains(key, "main.go") {
				t.Fatal("logical path leaked into object key")
			}
		})
	}
}

func TestAttemptWorkspaceContentBoundsUploadsAndPreservesTheFirstObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			oversizeID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), oversizeID, strings.NewReader("x"),
				model.AttemptWorkspaceMaximumFileBytes+1, "text/plain")
			var invalid *AttemptWorkspaceInvalidContentError
			if !errors.As(err, &invalid) {
				t.Fatalf("oversize error = %T, %v", err, err)
			}
			if _, err = content.OpenAttemptWorkspaceObject(context.Background(), oversizeID); !IsNotFound(err) {
				t.Fatalf("oversize object became visible: %v", err)
			}

			objectID := model.NewAttemptWorkspaceObjectID()
			if _, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("one"), 3, "text/plain"); err != nil {
				t.Fatal(err)
			}
			if _, err = content.StageAttemptWorkspaceObject(context.Background(), objectID, strings.NewReader("two"), 3, "text/plain"); err == nil {
				t.Fatal("second stage overwrote an opaque object")
			}
			opened, err := content.OpenAttemptWorkspaceObject(context.Background(), objectID)
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			if readErr != nil || string(got) != "one" {
				t.Fatalf("preserved bytes = %q, %v", got, readErr)
			}
		})
	}
}

func TestAttemptWorkspaceContentRejectsSizeMismatchAndRemovesExactly(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			mismatchID := model.NewAttemptWorkspaceObjectID()
			_, err = content.StageAttemptWorkspaceObject(context.Background(), mismatchID, strings.NewReader("two"), 1, "text/plain")
			var invalid *AttemptWorkspaceInvalidContentError
			if !errors.As(err, &invalid) {
				t.Fatalf("size mismatch error = %T, %v", err, err)
			}
			if _, err = content.OpenAttemptWorkspaceObject(context.Background(), mismatchID); !IsNotFound(err) {
				t.Fatalf("mismatched object remained visible: %v", err)
			}

			removedID, retainedID := model.NewAttemptWorkspaceObjectID(), model.NewAttemptWorkspaceObjectID()
			for _, id := range []model.AttemptWorkspaceObjectID{removedID, retainedID} {
				if _, err = content.StageAttemptWorkspaceObject(context.Background(), id, strings.NewReader("x"), 1, "text/plain"); err != nil {
					t.Fatal(err)
				}
			}
			if err = content.RemoveAttemptWorkspaceObject(context.Background(), removedID); err != nil {
				t.Fatal(err)
			}
			if err = content.RemoveAttemptWorkspaceObject(context.Background(), removedID); err != nil {
				t.Fatalf("repeat remove: %v", err)
			}
			if opened, openErr := content.OpenAttemptWorkspaceObject(context.Background(), retainedID); openErr != nil {
				t.Fatalf("retained object: %v", openErr)
			} else {
				_ = opened.Close()
			}
		})
	}
}
