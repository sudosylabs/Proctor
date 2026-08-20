// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package filecontent

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/packages/vfs/memory"
	"github.com/sudosylabs/proctor/server/model"
)

func TestOnboardingImportContentStagesOpensAndRemovesPrivateObject(t *testing.T) {
	t.Parallel()
	content, err := New(memory.New())
	if err != nil {
		t.Fatal(err)
	}
	id := model.NewOnboardingImportID()
	digest, size, err := content.StageOnboardingImport(context.Background(), id, strings.NewReader("email\na@example.edu\n"), 1024)
	if err != nil || len(digest) != 64 || size != int64(len("email\na@example.edu\n")) {
		t.Fatalf("stage = %q %d %v", digest, size, err)
	}
	opened, err := content.OpenOnboardingImport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil || string(body) != "email\na@example.edu\n" {
		t.Fatalf("body = %q, %v", body, err)
	}
	if err = content.RemoveOnboardingImport(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err = content.RemoveOnboardingImport(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardingImportContentListsStaleFilesForOrphanReconciliation(t *testing.T) {
	t.Parallel()
	content, err := New(memory.New())
	if err != nil {
		t.Fatal(err)
	}
	id := model.NewOnboardingImportID()
	if _, _, err = content.StageOnboardingImport(context.Background(), id, strings.NewReader("email\na@example.edu\n"), 1024); err != nil {
		t.Fatal(err)
	}
	ids, cursor, err := content.ListOnboardingImportFiles(context.Background(), "", 10, time.Now().Add(time.Hour))
	if err != nil || cursor != "" || len(ids) != 1 || ids[0] != id {
		t.Fatalf("staged files = %v, %q, %v", ids, cursor, err)
	}
}

func TestOnboardingImportContentDeletesOverLimitObject(t *testing.T) {
	t.Parallel()
	content, _ := New(memory.New())
	id := model.NewOnboardingImportID()
	if _, _, err := content.StageOnboardingImport(context.Background(), id, strings.NewReader("12345"), 4); !errors.Is(err, ErrOnboardingImportTooLarge) {
		t.Fatalf("error = %v", err)
	}
	if opened, err := content.OpenOnboardingImport(context.Background(), id); err == nil {
		_ = opened.Close()
		t.Fatal("over-limit object remained")
	}
}
