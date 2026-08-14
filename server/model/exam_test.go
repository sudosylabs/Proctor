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
