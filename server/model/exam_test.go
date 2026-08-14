// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"strings"
	"testing"
	"time"
)

func TestNewExamAuthoringState(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	examID := NewExamID()
	unitID := NewAcademicUnitID()
	creatorID := NewUserID()

	exam, err := NewExam(examID, unitID, creatorID, at)
	if err != nil {
		t.Fatalf("new exam: %v", err)
	}
	draft, err := NewExamDraft(examID, "  Systems Programming  ", "", DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	manager, err := NewExamManager(examID, creatorID, creatorID, at)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if exam.CreatorUserID != creatorID || exam.OwnerUserID != creatorID {
		t.Fatalf("creator/owner = %s/%s, want %s", exam.CreatorUserID, exam.OwnerUserID, creatorID)
	}
	if !exam.DefaultRevisionID.IsZero() || exam.Revision != 1 {
		t.Fatalf("default revision/revision = %q/%d, want empty/1", exam.DefaultRevisionID, exam.Revision)
	}
	if draft.ExamID != examID || draft.Title != "Systems Programming" || draft.InstructionsMarkdown != "" || draft.Revision != 1 {
		t.Fatalf("unexpected draft: %#v", draft)
	}
	if manager.ExamID != examID || manager.UserID != creatorID || manager.GrantedByUserID != creatorID {
		t.Fatalf("unexpected manager: %#v", manager)
	}
	wantUTC := at.UTC()
	if !exam.CreatedAt.Equal(wantUTC) || !exam.UpdatedAt.Equal(wantUTC) || !draft.UpdatedAt.Equal(wantUTC) || !manager.GrantedAt.Equal(wantUTC) {
		t.Fatal("authoring timestamps were not normalized to UTC")
	}
}

func TestExamArchiveRecordsImmutableTimeAndAdvancesRevision(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	exam, err := NewExam(NewExamID(), NewAcademicUnitID(), NewUserID(), createdAt)
	if err != nil {
		t.Fatal(err)
	}
	archivedAt := createdAt.Add(time.Hour)
	if err := exam.Archive(archivedAt); err != nil {
		t.Fatal(err)
	}
	if !exam.ArchivedAt.Valid || !exam.ArchivedAt.Time.Equal(archivedAt) || !exam.UpdatedAt.Equal(archivedAt) || exam.Revision != 2 {
		t.Fatalf("archived Exam = %#v", exam)
	}
	first := *exam
	if err := exam.Archive(archivedAt.Add(time.Hour)); err == nil {
		t.Fatal("second Archive succeeded")
	}
	if *exam != first {
		t.Fatalf("second Archive mutated Exam: %#v", exam)
	}
}

func TestExamAndDraftValidateIndependently(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	exam, err := NewExam(NewExamID(), NewAcademicUnitID(), NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := NewExamDraft(exam.ID, "Compiler Construction", "Read **carefully**.", DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}

	draft.Revision = 7
	if err := draft.Validate(); err != nil {
		t.Fatalf("valid independent draft revision: %v", err)
	}
	if exam.Revision != 1 {
		t.Fatalf("draft revision changed exam revision to %d", exam.Revision)
	}

	exam.OwnerUserID = UserID("")
	if err := exam.Validate(); err == nil {
		t.Fatal("expected missing owner to fail")
	}
	exam.OwnerUserID = exam.CreatorUserID
	draft.Title = strings.Repeat("x", ExamTitleMaxRunes+1)
	if err := draft.Validate(); err == nil {
		t.Fatal("expected oversized title to fail")
	}
	draft.Title = " Compiler Construction "
	if err := draft.Validate(); err == nil {
		t.Fatal("expected persisted untrimmed title to fail")
	}
}

func TestExamDraftApplyTextPatchPreservesPresenceAndRevision(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	draft, err := NewExamDraft(NewExamID(), "Systems", "Use **Go**.", DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}
	title := "  Distributed Systems  "
	clearInstructions := ""
	changed, err := draft.ApplyTextPatch(&title, &clearInstructions, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !changed || draft.Title != "Distributed Systems" || draft.InstructionsMarkdown != "" || draft.Revision != 2 || !draft.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("patched draft = %#v", draft)
	}

	unchangedTitle := draft.Title
	changed, err = draft.ApplyTextPatch(&unchangedTitle, nil, at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if changed || draft.Revision != 2 || !draft.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("no-op changed draft = %#v", draft)
	}
}

func TestExamDraftApplyTextPatchRejectsMissingOrInvalidFieldsAtomically(t *testing.T) {
	t.Parallel()
	draft, err := NewExamDraft(NewExamID(), "Systems", "Instructions", DefaultExamPolicySet(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	original := *draft
	if _, err := draft.ApplyTextPatch(nil, nil, time.Now().UTC()); err == nil {
		t.Fatal("ApplyTextPatch accepted no authored field")
	}
	invalidTitle := "   "
	if _, err := draft.ApplyTextPatch(&invalidTitle, nil, time.Now().UTC()); err == nil {
		t.Fatal("ApplyTextPatch accepted an empty title")
	}
	invalidUTF8Title := string([]byte{0xff})
	if _, err := draft.ApplyTextPatch(&invalidUTF8Title, nil, time.Now().UTC()); err == nil {
		t.Fatal("ApplyTextPatch accepted an invalid UTF-8 title")
	}
	tooLarge := strings.Repeat("x", ExamInstructionsMarkdownMaxBytes+1)
	if _, err := draft.ApplyTextPatch(nil, &tooLarge, time.Now().UTC()); err == nil {
		t.Fatal("ApplyTextPatch accepted oversized instructions")
	}
	if *draft != original {
		t.Fatalf("failed patch mutated draft: got %#v want %#v", draft, original)
	}
}

func TestNewExamDraftRejectsInvalidUTF8Title(t *testing.T) {
	t.Parallel()
	if _, err := NewExamDraft(NewExamID(), string([]byte{0xff}), "", DefaultExamPolicySet(), time.Now().UTC()); err == nil {
		t.Fatal("NewExamDraft accepted an invalid UTF-8 title")
	}
}
