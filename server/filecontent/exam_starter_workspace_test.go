// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestStarterWorkspaceContentStagesAndOpensOnlyTheExactOpaqueObject(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			filesystem := backend.open(t)
			content, err := New(filesystem)
			if err != nil {
				t.Fatal(err)
			}
			objectID := model.StarterWorkspaceObjectID("oooooooooooooooooooooooooo")
			body := []byte("package main\n")
			staged, err := content.StageStarterWorkspaceObject(context.Background(), objectID, bytes.NewReader(body), int64(len(body)), "text/x-go")
			if err != nil {
				t.Fatalf("stage: %v", err)
			}
			if staged.SizeBytes != int64(len(body)) || staged.MediaType != "text/x-go" || staged.SHA256 != "df1d036cbbf3df46e2045071e082245ece204c7f53ecf0a4e022bff9bb228f47" {
				t.Fatalf("staged metadata = %#v", staged)
			}
			opened, err := content.OpenStarterWorkspaceObject(context.Background(), objectID)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			got, readErr := io.ReadAll(opened)
			_ = opened.Close()
			if readErr != nil || !bytes.Equal(got, body) {
				t.Fatalf("read = %q, %v", got, readErr)
			}
			const key = "exam-starter-workspace/oo/oo/objects/oooooooooooooooooooooooooo"
			if _, err = filesystem.Stat(context.Background(), key); err != nil {
				t.Fatalf("opaque key missing: %v", err)
			}
			if strings.Contains(key, "main.go") {
				t.Fatal("logical path leaked into object key")
			}
		})
	}
}

func TestStarterWorkspaceContentAcceptsEmptyFilesAndRejectsOversizeBeforePublication(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			emptyID := model.NewStarterWorkspaceObjectID()
			staged, err := content.StageStarterWorkspaceObject(context.Background(), emptyID, bytes.NewReader(nil), 0, "text/plain")
			if err != nil || staged.SizeBytes != 0 {
				t.Fatalf("empty stage = %#v, %v", staged, err)
			}
			oversizeID := model.NewStarterWorkspaceObjectID()
			err = func() error {
				_, stageErr := content.StageStarterWorkspaceObject(context.Background(), oversizeID, strings.NewReader("x"), model.StarterWorkspaceMaximumFileBytes+1, "text/plain")
				return stageErr
			}()
			if err == nil {
				t.Fatal("oversized declared content was accepted")
			}
			var invalid *StarterWorkspaceInvalidContentError
			if !errors.As(err, &invalid) {
				t.Fatalf("oversized content error = %T, %v", err, err)
			}
			if _, err = content.OpenStarterWorkspaceObject(context.Background(), oversizeID); !IsNotFound(err) {
				t.Fatalf("oversized content became visible: %v", err)
			}
		})
	}
}

func TestStarterWorkspaceContentClassifiesDeclaredSizeMismatchAsInvalid(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			objectID := model.NewStarterWorkspaceObjectID()
			_, err = content.StageStarterWorkspaceObject(context.Background(), objectID, strings.NewReader("two"), 1, "text/plain")
			var invalid *StarterWorkspaceInvalidContentError
			if !errors.As(err, &invalid) {
				t.Fatalf("size mismatch error = %T, %v", err, err)
			}
			if _, err = content.OpenStarterWorkspaceObject(context.Background(), objectID); !IsNotFound(err) {
				t.Fatalf("mismatched content remained visible: %v", err)
			}
		})
	}
}

func TestStarterWorkspaceContentRemovalIsExactAndIdempotent(t *testing.T) {
	for _, backend := range contentTestBackends() {
		t.Run(backend.name, func(t *testing.T) {
			content, err := New(backend.open(t))
			if err != nil {
				t.Fatal(err)
			}
			removedID, retainedID := model.NewStarterWorkspaceObjectID(), model.NewStarterWorkspaceObjectID()
			for _, id := range []model.StarterWorkspaceObjectID{removedID, retainedID} {
				if _, err = content.StageStarterWorkspaceObject(context.Background(), id, bytes.NewReader([]byte("x")), 1, "text/plain"); err != nil {
					t.Fatal(err)
				}
			}
			if err = content.RemoveStarterWorkspaceObject(context.Background(), removedID); err != nil {
				t.Fatal(err)
			}
			if err = content.RemoveStarterWorkspaceObject(context.Background(), removedID); err != nil {
				t.Fatalf("repeat remove: %v", err)
			}
			if opened, openErr := content.OpenStarterWorkspaceObject(context.Background(), retainedID); openErr != nil {
				t.Fatalf("retained object: %v", openErr)
			} else {
				_ = opened.Close()
			}
		})
	}
}
