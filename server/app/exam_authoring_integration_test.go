//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"os"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestExamAuthoringIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "northbridge", DisplayName: "Northbridge University"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{InstitutionID: institution.ID, Name: "computing", DisplayName: "Computing"})
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	teacher, appErr := helper.App.CreateLocalUser(ctx, &model.User{Username: "exam-teacher", Email: "exam-teacher@example.edu", DisplayName: "Exam Teacher"}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	login := loginIntegrationUser(t, helper.Handler(), teacher.Username, password, model.SessionClientCLI, "exam-teacher-cli")
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: teacher.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)}); err != nil {
		t.Fatal(err)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{Name: "exam-author", DisplayName: "Exam Author", Permissions: []string{string(model.ActionExamCreate), string(model.ActionExamView)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: teacher.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)}); err != nil {
		t.Fatal(err)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "exam-create-integration"})
	created, appErr := helper.App.CreateExam(ctx, invocation, application.CreateExamCommand{AcademicUnitID: unit.ID, Title: "  Systems Programming  ", InstructionsMarkdown: "Use **Go**.", IdempotencyKey: "exam-create-once"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if created.Exam.CreatorUserID != teacher.ID || created.Exam.OwnerUserID != teacher.ID || created.Draft.Title != "Systems Programming" || created.Draft.Policy != model.DefaultExamPolicySet() || created.ManagerCount != 1 {
		t.Fatalf("created = %#v", created)
	}
	replayed, appErr := helper.App.CreateExam(ctx, invocation, application.CreateExamCommand{AcademicUnitID: unit.ID, Title: "  Systems Programming  ", InstructionsMarkdown: "Use **Go**.", IdempotencyKey: "exam-create-once"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if replayed.Exam.ID != created.Exam.ID {
		t.Fatalf("replay exam = %s, want %s", replayed.Exam.ID, created.Exam.ID)
	}
	got, appErr := helper.App.GetExam(ctx, invocation, application.GetExamQuery{ExamID: created.Exam.ID})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if got.Exam.ID != created.Exam.ID || got.Draft.Title != created.Draft.Title || got.ManagerCount != 1 || got.ResourceCount != 0 || got.HasStarterWorkspace {
		t.Fatalf("get = %#v", got)
	}
}
