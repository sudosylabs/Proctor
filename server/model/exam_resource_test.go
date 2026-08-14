// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceRequiresBoundedAuthoredMetadataAndExactFileIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	resource, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "  Reference sheet  ", "Use **section 2**.", 0, at)
	if err != nil {
		t.Fatalf("NewExamResource() error = %v", err)
	}
	if resource.DisplayName != "Reference sheet" || resource.Position != 0 || resource.IsArchived() {
		t.Fatalf("resource = %#v", resource)
	}
	if err := resource.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for name, description := range map[string]string{
		"empty name":        "",
		"invalid name UTF8": string([]byte{0xff}),
		"long name":         strings.Repeat("é", model.ExamResourceDisplayNameMaxRunes+1),
	} {
		if _, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), description, "", 0, at); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Notes", strings.Repeat("a", model.ExamResourceDescriptionMaxBytes+1), 0, at); err == nil {
		t.Fatal("oversized Markdown description accepted")
	}
	if _, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Notes", string([]byte{0xff}), 0, at); err == nil {
		t.Fatal("invalid Markdown UTF-8 accepted")
	}
	if got := utf8.RuneCountInString(resource.DisplayName); got != 15 {
		t.Fatalf("display name rune count = %d", got)
	}
}

func TestExamResourceChangesDraftProjectionWithoutMutatingPublishedIdentity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	resource, err := model.NewExamResource(model.NewExamResourceID(), model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), "Reference", "", 0, at)
	if err != nil {
		t.Fatal(err)
	}
	originalRevision := resource.SelectedFileRevisionID
	replacement := model.NewFileRevisionID()
	changed, err := resource.ReplaceContent(replacement, at.Add(time.Minute))
	if err != nil || !changed || resource.SelectedFileRevisionID != replacement || originalRevision == replacement {
		t.Fatalf("ReplaceContent() changed=%v resource=%#v error=%v", changed, resource, err)
	}
	changed, err = resource.ApplyMetadata("Reference", "", at.Add(2*time.Minute))
	if err != nil || changed {
		t.Fatalf("no-op ApplyMetadata() changed=%v error=%v", changed, err)
	}
	changed, err = resource.ApplyMetadata(" Updated ", "New *note*", at.Add(2*time.Minute))
	if err != nil || !changed || resource.DisplayName != "Updated" {
		t.Fatalf("ApplyMetadata() changed=%v resource=%#v error=%v", changed, resource, err)
	}
	if err = resource.Archive(at.Add(3 * time.Minute)); err != nil || !resource.IsArchived() {
		t.Fatalf("Archive() resource=%#v error=%v", resource, err)
	}
}
