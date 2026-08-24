// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestListExamsNormalizesDefaultsAndMapsBoundedSummaries(t *testing.T) {
	t.Parallel()
	userID, examID := model.NewUserID(), model.NewExamID()
	child := &examUseCasesFake{}
	child.catalog = examengine.CatalogPage{Items: []store.ExamSummary{{ID: examID, Title: "Systems", Revision: 2, ManagerCount: 1}}}
	application := &App{exams: child}
	page, err := application.ListExams(context.Background(), NewInvocation(testExamPrincipal(userID), model.RequestMetadata{}), ListExamsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if child.list.Limit != 50 || child.list.ArchiveFilter != store.ExamArchiveActive || len(page.Items) != 1 || page.Items[0].ID != examID {
		t.Fatalf("query/page = %#v / %#v", child.list, page)
	}
}

func TestArchiveExamBuildsIdempotentChildCommand(t *testing.T) {
	t.Parallel()
	userID, examID := model.NewUserID(), model.NewExamID()
	archivedAt := time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	child := &examUseCasesFake{archived: model.Exam{ID: examID, ArchivedAt: model.OptionalTimeFrom(archivedAt), Revision: 4}}
	application := &App{exams: child}
	got, err := application.ArchiveExam(context.Background(), NewInvocation(testExamPrincipal(userID), model.RequestMetadata{}), ArchiveExamCommand{ExamID: examID, ExpectedExamRevision: 3, IdempotencyKey: "archive-once"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != examID || child.archive.ExamID != examID || child.archive.ExpectedExamRevision != 3 || child.archive.IdempotencyKey != "archive-once" {
		t.Fatalf("result/command = %#v / %#v", got, child.archive)
	}
}

func TestArchiveExamConcealsUnauthorizedAndMissingTargets(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{NewError("authorization.denied"), &examengine.Fault{Code: "exam.not_found", Cause: errors.New("missing")}} {
		application := &App{exams: &examUseCasesFake{err: failure}}
		_, err := application.ArchiveExam(context.Background(), NewInvocation(testExamPrincipal(model.NewUserID()), model.RequestMetadata{}), ArchiveExamCommand{
			ExamID: model.NewExamID(), ExpectedExamRevision: 1, IdempotencyKey: "archive-concealed",
		})
		if !Is(err, "resource.not_found") {
			t.Fatalf("ArchiveExam error = %v, want concealed resource.not_found", err)
		}
	}
}
