//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"os"
	"testing"
	"time"

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
	role, err := persistence.Role().Save(ctx, &model.Role{Name: "exam-author", DisplayName: "Exam Author", Permissions: []string{string(model.ActionExamCreate), string(model.ActionExamView), string(model.ActionExamManage)}})
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
	editedTitle := "Distributed Systems"
	clearInstructions := ""
	edited, appErr := helper.App.EditExamDraftText(ctx, invocation, application.EditExamDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &editedTitle, InstructionsMarkdown: &clearInstructions, IdempotencyKey: "exam-edit-once",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if edited.Draft.Title != editedTitle || edited.Draft.InstructionsMarkdown != "" || edited.Draft.Revision != created.Draft.Revision+1 || edited.Exam.Revision != created.Exam.Revision {
		t.Fatalf("edited = %#v", edited)
	}
	editedReplay, appErr := helper.App.EditExamDraftText(ctx, invocation, application.EditExamDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &editedTitle, InstructionsMarkdown: &clearInstructions, IdempotencyKey: "exam-edit-once",
	})
	if appErr != nil || editedReplay.Draft.Revision != edited.Draft.Revision {
		t.Fatalf("edit replay = %#v, %v", editedReplay, appErr)
	}
	focusPolicy, appErr := helper.App.ConfigureExamDraftFocusLoss(ctx, invocation, application.ConfigureExamDraftFocusLossCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: edited.Draft.Revision,
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag, IdempotencyKey: "exam-focus-loss-once",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if focusPolicy.Draft.Revision != edited.Draft.Revision+1 || focusPolicy.Draft.Policy.ConnectionLoss != edited.Draft.Policy.ConnectionLoss || focusPolicy.Draft.Policy.FocusLoss.Enabled || focusPolicy.Draft.Policy.FocusLoss.MinimumDuration != 500*time.Millisecond || focusPolicy.Draft.Policy.FocusLoss.IncidentCount != 1 || focusPolicy.Draft.Policy.FocusLoss.Window != 10*time.Second || focusPolicy.Draft.Policy.FocusLoss.Outcome != model.IntegrityOutcomeFlag || focusPolicy.Draft.Title != editedTitle || focusPolicy.Exam.Revision != edited.Exam.Revision {
		t.Fatalf("focus policy = %#v", focusPolicy)
	}
	focusReplay, appErr := helper.App.ConfigureExamDraftFocusLoss(ctx, invocation, application.ConfigureExamDraftFocusLossCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: edited.Draft.Revision,
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag, IdempotencyKey: "exam-focus-loss-once",
	})
	if appErr != nil || focusReplay.Draft.Revision != focusPolicy.Draft.Revision {
		t.Fatalf("focus policy replay = %#v, %v", focusReplay, appErr)
	}
	staleTitle := "Operating Systems"
	_, appErr = helper.App.EditExamDraftText(ctx, invocation, application.EditExamDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &staleTitle, IdempotencyKey: "exam-edit-stale",
	})
	if !application.Is(appErr, "exam.draft.revision_conflict") {
		t.Fatalf("stale edit error = %v", appErr)
	}
	got, appErr := helper.App.GetExam(ctx, invocation, application.GetExamQuery{ExamID: created.Exam.ID})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if got.Exam.ID != created.Exam.ID || got.Draft.Title != editedTitle || got.Draft.InstructionsMarkdown != "" || got.Draft.Policy != focusPolicy.Draft.Policy || got.ManagerCount != 1 || got.ResourceCount != 0 || got.HasStarterWorkspace {
		t.Fatalf("get = %#v", got)
	}
	active, appErr := helper.App.ListExams(ctx, invocation, application.ListExamsQuery{AcademicUnitID: unit.ID})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if len(active.Items) != 1 || active.Items[0].ID != created.Exam.ID || active.Items[0].Title != editedTitle || active.Items[0].ArchivedAt.Valid || active.Items[0].ManagerCount != 1 {
		t.Fatalf("active catalog = %#v", active)
	}
	outsider, appErr := helper.App.CreateLocalUser(ctx, &model.User{Username: "exam-outsider", Email: "exam-outsider@example.edu", DisplayName: "Exam Outsider"}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	outsiderLogin := loginIntegrationUser(t, helper.Handler(), outsider.Username, password, model.SessionClientCLI, "exam-outsider-cli")
	outsiderPrincipal, appErr := helper.App.AuthenticateAccess(ctx, outsiderLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	_, appErr = helper.App.GetExam(ctx, application.NewInvocation(*outsiderPrincipal, model.RequestMetadata{RequestID: "exam-get-denied-integration"}), application.GetExamQuery{ExamID: created.Exam.ID})
	if !application.Is(appErr, "resource.not_found") {
		t.Fatalf("outsider get error = %v, want concealed resource.not_found", appErr)
	}
	archived, appErr := helper.App.ArchiveExam(ctx, invocation, application.ArchiveExamCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: created.Exam.Revision, IdempotencyKey: "exam-archive-once",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !archived.IsArchived() || archived.Revision != created.Exam.Revision+1 {
		t.Fatalf("archived Exam = %#v", archived)
	}
	archiveReplay, appErr := helper.App.ArchiveExam(ctx, invocation, application.ArchiveExamCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: created.Exam.Revision, IdempotencyKey: "exam-archive-once",
	})
	if appErr != nil || archiveReplay.Revision != archived.Revision || !archiveReplay.ArchivedAt.Time.Equal(archived.ArchivedAt.Time) {
		t.Fatalf("archive replay = %#v, %v", archiveReplay, appErr)
	}
	active, appErr = helper.App.ListExams(ctx, invocation, application.ListExamsQuery{AcademicUnitID: unit.ID})
	if appErr != nil || len(active.Items) != 0 {
		t.Fatalf("active catalog after archive = %#v, %v", active, appErr)
	}
	archivedPage, appErr := helper.App.ListExams(ctx, invocation, application.ListExamsQuery{AcademicUnitID: unit.ID, ArchiveFilter: application.ExamArchiveArchived})
	if appErr != nil || len(archivedPage.Items) != 1 || archivedPage.Items[0].ID != archived.ID || !archivedPage.Items[0].ArchivedAt.Valid || archivedPage.Items[0].Revision != archived.Revision {
		t.Fatalf("archived catalog = %#v, %v", archivedPage, appErr)
	}
	archivedView, appErr := helper.App.GetExam(ctx, invocation, application.GetExamQuery{ExamID: created.Exam.ID})
	if appErr != nil || !archivedView.Exam.IsArchived() || archivedView.Draft.Title != editedTitle {
		t.Fatalf("archived exact Get = %#v, %v", archivedView, appErr)
	}
	postArchiveTitle := "Cannot edit archived Exam"
	_, appErr = helper.App.EditExamDraftText(ctx, invocation, application.EditExamDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: focusPolicy.Draft.Revision,
		Title: &postArchiveTitle, IdempotencyKey: "exam-edit-after-archive",
	})
	if !application.Is(appErr, "exam.archived") {
		t.Fatalf("post-archive edit error = %v", appErr)
	}
}
