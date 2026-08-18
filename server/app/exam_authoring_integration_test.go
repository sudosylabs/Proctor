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
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
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
	membership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: teacher.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	if err != nil {
		t.Fatal(err)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{Name: "exam-author", DisplayName: "Exam Author", Permissions: []string{string(model.ActionExamCreate), string(model.ActionExamView), string(model.ActionExamManage)}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: teacher.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	if err != nil {
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
	managerUser, appErr := helper.App.CreateLocalUser(ctx, &model.User{Username: "exam-manager", Email: "exam-manager@example.edu", DisplayName: "Exam Manager"}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	managerMembership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: managerUser.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	if err != nil {
		t.Fatal(err)
	}
	managerAdded, appErr := helper.App.AddExamManager(ctx, invocation, application.AddExamManagerCommand{
		ExamID: created.Exam.ID, UserID: managerUser.ID, ExpectedExamRevision: 1, IdempotencyKey: "exam-manager-add",
	})
	if appErr != nil || managerAdded.Exam.Revision != 2 || managerAdded.Manager.UserID != managerUser.ID {
		t.Fatalf("manager addition = %#v, %v", managerAdded, appErr)
	}
	managerLogin := loginIntegrationUser(t, helper.Handler(), managerUser.Username, password, model.SessionClientCLI, "exam-manager-cli")
	managerPrincipal, appErr := helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	managerInvocation := application.NewInvocation(*managerPrincipal, model.RequestMetadata{RequestID: "exam-manager-integration"})
	if _, appErr := helper.App.ListExamManagers(ctx, managerInvocation, application.ListExamManagersQuery{ExamID: created.Exam.ID, Limit: 50}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("Manager without role error = %v", appErr)
	}
	managerBinding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: managerUser.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	if err != nil {
		t.Fatal(err)
	}
	managers, appErr := helper.App.ListExamManagers(ctx, managerInvocation, application.ListExamManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	if appErr != nil || len(managers.Items) != 2 {
		t.Fatalf("Manager list = %#v, %v", managers, appErr)
	}
	managerBindingEndedAt := model.GetMillis() - 500
	if _, err := persistence.RoleBinding().End(ctx, managerBinding.ID.String(), managerBindingEndedAt); err != nil {
		t.Fatal(err)
	}
	if _, appErr := helper.App.ListExamManagers(ctx, managerInvocation, application.ListExamManagersQuery{ExamID: created.Exam.ID, Limit: 50}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("Manager after role revocation error = %v", appErr)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: managerUser.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(managerBindingEndedAt)}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.AcademicUnitMember().End(ctx, managerMembership.ID.String(), managerMembership.Revision, model.GetMillis()-100); err != nil {
		t.Fatal(err)
	}
	if _, appErr := helper.App.ListExamManagers(ctx, managerInvocation, application.ListExamManagersQuery{ExamID: created.Exam.ID, Limit: 50}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("Manager after membership loss error = %v", appErr)
	}
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: managerUser.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 50)}); err != nil {
		t.Fatal(err)
	}
	accountManagerRole, err := persistence.Role().Save(ctx, &model.Role{Name: "exam-account-manager", DisplayName: "Exam Account Manager", Permissions: []string{string(model.ActionUserManage)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: teacher.ID, RoleID: accountManagerRole.ID, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)}); err != nil {
		t.Fatal(err)
	}
	disabledManager, appErr := helper.App.SetUserEnabled(ctx, invocation, application.SetUserEnabledCommand{ID: managerUser.ID.String(), Enabled: false})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken); !application.Is(appErr, "authentication.invalid_token") {
		t.Fatalf("disabled Manager authentication error = %v", appErr)
	}
	reenabledAt := model.GetMillis()
	reenableAudit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: teacher.ID, Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: managerUser.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "exam-authoring-integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.User().SetDisabledWithAudit(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledManager.ID.String(), ExpectedRevision: disabledManager.Revision, Disabled: false,
		ChangedAt: reenabledAt, AuditEventID: reenableAudit.ID.String(), AuditAt: reenabledAt,
	})); err != nil {
		t.Fatal(err)
	}
	transferred, appErr := helper.App.TransferExamOwnership(ctx, invocation, application.TransferExamOwnershipCommand{
		ExamID: created.Exam.ID, UserID: managerUser.ID, ExpectedExamRevision: 2, IdempotencyKey: "exam-owner-transfer",
	})
	if appErr != nil || transferred.Exam.OwnerUserID != managerUser.ID || transferred.Exam.Revision != 3 {
		t.Fatalf("ownership transfer = %#v, %v", transferred, appErr)
	}
	removedCreator, appErr := helper.App.RemoveExamManager(ctx, managerInvocation, application.RemoveExamManagerCommand{
		ExamID: created.Exam.ID, UserID: teacher.ID, ExpectedExamRevision: 3, IdempotencyKey: "exam-manager-remove-creator",
	})
	if appErr != nil || removedCreator.Exam.Revision != 4 || removedCreator.Manager.UserID != teacher.ID {
		t.Fatalf("creator relationship removal = %#v, %v", removedCreator, appErr)
	}
	currentExamRevision := int64(4)
	membershipEndedAt := model.GetMillis() - 100
	if _, err := persistence.AcademicUnitMember().End(ctx, membership.ID.String(), membership.Revision, membershipEndedAt); err != nil {
		t.Fatal(err)
	}
	withoutMembership, appErr := helper.App.ListExams(ctx, invocation, application.ListExamsQuery{AcademicUnitID: unit.ID})
	if appErr != nil || len(withoutMembership.Items) != 0 {
		t.Fatalf("catalog after exact membership ended = %#v, %v", withoutMembership, appErr)
	}
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: teacher.ID, StartsAt: model.TimeFromMillis(membershipEndedAt),
	}); err != nil {
		t.Fatal(err)
	}
	bindingEndedAt := model.GetMillis() - 50
	if _, err := persistence.RoleBinding().End(ctx, binding.ID.String(), bindingEndedAt); err != nil {
		t.Fatal(err)
	}
	if _, appErr := helper.App.ListExams(ctx, invocation, application.ListExamsQuery{AcademicUnitID: unit.ID}); !application.Is(appErr, "authorization.denied") {
		t.Fatalf("catalog after role ended error = %v", appErr)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: teacher.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(bindingEndedAt),
	}); err != nil {
		t.Fatal(err)
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
	outsiderInvocation := application.NewInvocation(*outsiderPrincipal, model.RequestMetadata{RequestID: "exam-get-denied-integration"})
	_, appErr = helper.App.GetExam(ctx, outsiderInvocation, application.GetExamQuery{ExamID: created.Exam.ID})
	if !application.Is(appErr, "resource.not_found") {
		t.Fatalf("outsider get error = %v, want concealed resource.not_found", appErr)
	}
	_, appErr = helper.App.ArchiveExam(ctx, outsiderInvocation, application.ArchiveExamCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: created.Exam.Revision, IdempotencyKey: "exam-archive-denied",
	})
	if !application.Is(appErr, "resource.not_found") {
		t.Fatalf("outsider archive error = %v, want concealed resource.not_found", appErr)
	}
	overrideRole, err := persistence.Role().Save(ctx, &model.Role{Name: "exam-system-override", DisplayName: "Exam System Override", Permissions: []string{
		string(model.ActionExamViewOverride), string(model.ActionExamManageOverride),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: outsider.ID, RoleID: overrideRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	}); err != nil {
		t.Fatal(err)
	}
	overrideAdded, appErr := helper.App.AddExamManager(ctx, outsiderInvocation, application.AddExamManagerCommand{
		ExamID: created.Exam.ID, UserID: teacher.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-manager-override-add",
	})
	if appErr != nil || overrideAdded.Exam.Revision != currentExamRevision+1 || overrideAdded.Manager.UserID != teacher.ID {
		t.Fatalf("override Manager addition = %#v, %v", overrideAdded, appErr)
	}
	currentExamRevision = overrideAdded.Exam.Revision
	overridePage, appErr := helper.App.ListExams(ctx, outsiderInvocation, application.ListExamsQuery{AcademicUnitID: unit.ID})
	if appErr != nil || len(overridePage.Items) != 1 || overridePage.Items[0].ID != created.Exam.ID {
		t.Fatalf("override catalog = %#v, %v", overridePage, appErr)
	}
	viewOverrideAudits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: outsider.ID.String(), Action: string(model.ActionExamViewOverride),
		Resource: &model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil || len(viewOverrideAudits) != 1 || viewOverrideAudits[0].Status != model.AuditStatusSuccess || viewOverrideAudits[0].ScopeType != model.RoleScopeInstitution || viewOverrideAudits[0].ScopeID != institution.ID.String() {
		t.Fatalf("override list audits = %#v, %v", viewOverrideAudits, err)
	}
	archived, appErr := helper.App.ArchiveExam(ctx, outsiderInvocation, application.ArchiveExamCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-archive-once",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !archived.IsArchived() || archived.Revision != currentExamRevision+1 {
		t.Fatalf("archived Exam = %#v", archived)
	}
	manageOverrideAudits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: outsider.ID.String(), Action: string(model.ActionExamManageOverride),
		Resource: &model.Resource{Type: model.ResourceExam, ID: created.Exam.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	manageStatuses := map[model.AuditStatus]int{}
	for _, event := range manageOverrideAudits {
		manageStatuses[event.Status]++
		if event.ScopeType != model.RoleScopeAcademicUnit || event.ScopeID != unit.ID.String() {
			t.Fatalf("override archive audit scope = %#v", event)
		}
	}
	if len(manageOverrideAudits) != 5 || manageStatuses[model.AuditStatusSuccess] != 4 || manageStatuses[model.AuditStatusFail] != 1 {
		t.Fatalf("override archive audits = %#v, %v", manageOverrideAudits, err)
	}
	archiveReplay, appErr := helper.App.ArchiveExam(ctx, outsiderInvocation, application.ArchiveExamCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-archive-once",
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
