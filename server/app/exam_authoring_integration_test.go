//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestExamAuthoringIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()
	password := "correct horse battery staple"
	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, invocation.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "northbridge", InstitutionDisplayName: "Northbridge University",
		AdministratorUsername: "exam-admin", AdministratorEmail: "exam-admin@example.edu",
		AdministratorDisplayName: "Exam Administrator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "203.0.113.10",
	})
	require.NoError(t, appErr)
	institution := bootstrap.Institution
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{InstitutionID: bootstrap.Institution.ID, Name: "computing", DisplayName: "Computing"})
	require.NoError(t, err)
	teacher, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{Username: "exam-teacher", Email: "exam-teacher@example.edu", DisplayName: "Exam Teacher"}, Password: password})
	require.NoError(t, appErr)
	login := loginIntegrationUser(t, helper.Handler(), teacher.Username, password, model.SessionClientCLI, "exam-teacher-cli")
	principal, appErr := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, appErr)
	membership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: teacher.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	require.NoError(t, err)
	role, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: "exam-author", DisplayName: "Exam Author", Permissions: []string{string(model.ActionExamCreate), string(model.ActionExamView), string(model.ActionExamManage)}})
	require.NoError(t, err)
	binding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: teacher.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	require.NoError(t, err)
	call := invocation.New(*principal, model.RequestMetadata{RequestID: "exam-create-integration"})
	created, appErr := helper.App.CreateExam(ctx, call, exam.CreateCommand{AcademicUnitID: unit.ID, Title: "  Systems Programming  ", InstructionsMarkdown: "Use **Go**.", IdempotencyKey: "exam-create-once"})
	require.NoError(t, appErr)
	require.Equal(t, teacher.ID, created.Exam.CreatorUserID)
	require.Equal(t, teacher.ID, created.Exam.OwnerUserID)
	require.Equal(t, "Systems Programming", created.Draft.Title)
	require.True(t, created.Draft.Policy.Equal(model.DefaultExamPolicySet()))
	require.Equal(t, 1, created.ManagerCount)
	replayed, appErr := helper.App.CreateExam(ctx, call, exam.CreateCommand{AcademicUnitID: unit.ID, Title: "  Systems Programming  ", InstructionsMarkdown: "Use **Go**.", IdempotencyKey: "exam-create-once"})
	require.NoError(t, appErr)
	require.Equal(t, created.Exam.ID, replayed.Exam.ID, "replay exam")
	editedTitle := "Distributed Systems"
	clearInstructions := ""
	edited, appErr := helper.App.EditExamDraftText(ctx, call, exam.EditDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &editedTitle, InstructionsMarkdown: &clearInstructions, IdempotencyKey: "exam-edit-once",
	})
	require.NoError(t, appErr)
	require.Equal(t, editedTitle, edited.Draft.Title)
	require.Empty(t, edited.Draft.InstructionsMarkdown)
	require.Equal(t, created.Draft.Revision+1, edited.Draft.Revision)
	require.Equal(t, created.Exam.Revision, edited.Exam.Revision)
	editedReplay, appErr := helper.App.EditExamDraftText(ctx, call, exam.EditDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &editedTitle, InstructionsMarkdown: &clearInstructions, IdempotencyKey: "exam-edit-once",
	})
	require.NoError(t, appErr, "edit replay = %#v", editedReplay)
	require.Equal(t, edited.Draft.Revision, editedReplay.Draft.Revision, "edit replay = %#v", editedReplay)
	focusPolicy, appErr := helper.App.ConfigureExamDraftFocusLoss(ctx, call, exam.ConfigureDraftFocusLossCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: edited.Draft.Revision,
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag, IdempotencyKey: "exam-focus-loss-once",
	})
	require.NoError(t, appErr)
	require.Equal(t, edited.Draft.Revision+1, focusPolicy.Draft.Revision)
	require.Equal(t, edited.Draft.Policy.ConnectionLoss, focusPolicy.Draft.Policy.ConnectionLoss)
	require.False(t, focusPolicy.Draft.Policy.FocusLoss.Enabled)
	require.Equal(t, 500*time.Millisecond, focusPolicy.Draft.Policy.FocusLoss.MinimumDuration)
	require.Equal(t, 1, focusPolicy.Draft.Policy.FocusLoss.IncidentCount)
	require.Equal(t, 10*time.Second, focusPolicy.Draft.Policy.FocusLoss.Window)
	require.Equal(t, model.IntegrityOutcomeFlag, focusPolicy.Draft.Policy.FocusLoss.Outcome)
	require.Equal(t, editedTitle, focusPolicy.Draft.Title)
	require.Equal(t, edited.Exam.Revision, focusPolicy.Exam.Revision)
	focusReplay, appErr := helper.App.ConfigureExamDraftFocusLoss(ctx, call, exam.ConfigureDraftFocusLossCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: edited.Draft.Revision,
		Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 1,
		Window: 10 * time.Second, Outcome: model.IntegrityOutcomeFlag, IdempotencyKey: "exam-focus-loss-once",
	})
	require.NoError(t, appErr, "focus policy replay = %#v", focusReplay)
	require.Equal(t, focusPolicy.Draft.Revision, focusReplay.Draft.Revision, "focus policy replay = %#v", focusReplay)
	staleTitle := "Operating Systems"
	_, appErr = helper.App.EditExamDraftText(ctx, call, exam.EditDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision,
		Title: &staleTitle, IdempotencyKey: "exam-edit-stale",
	})
	require.True(t, apperror.Is(appErr, "exam.draft.revision_conflict"), "stale edit error = %v", appErr)
	got, appErr := helper.App.GetExam(ctx, call, exam.GetQuery{ExamID: created.Exam.ID})
	require.NoError(t, appErr)
	require.Equal(t, created.Exam.ID, got.Exam.ID)
	require.Equal(t, editedTitle, got.Draft.Title)
	require.Empty(t, got.Draft.InstructionsMarkdown)
	require.True(t, got.Draft.Policy.Equal(focusPolicy.Draft.Policy))
	require.Equal(t, 1, got.ManagerCount)
	require.Equal(t, 0, got.ResourceCount)
	require.False(t, got.HasStarterWorkspace)
	active, appErr := helper.App.ListExams(ctx, call, exam.ListQuery{AcademicUnitID: unit.ID})
	require.NoError(t, appErr)
	require.Len(t, active.Items, 1)
	require.Equal(t, created.Exam.ID, active.Items[0].ID)
	require.Equal(t, editedTitle, active.Items[0].Title)
	require.False(t, active.Items[0].ArchivedAt.Valid)
	require.Equal(t, 1, active.Items[0].ManagerCount)
	managerUser, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{Username: "exam-manager", Email: "exam-manager@example.edu", DisplayName: "Exam Manager"}, Password: password})
	require.NoError(t, appErr)
	managerMembership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: managerUser.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	require.NoError(t, err)
	managerAdded, appErr := helper.App.AddExamManager(ctx, call, exam.AddManagerCommand{
		ExamID: created.Exam.ID, UserID: managerUser.ID, ExpectedExamRevision: 1, IdempotencyKey: "exam-manager-add",
	})
	require.NoError(t, appErr, "manager addition = %#v", managerAdded)
	require.Equal(t, int64(2), managerAdded.Exam.Revision, "manager addition = %#v", managerAdded)
	require.Equal(t, managerUser.ID, managerAdded.Manager.UserID, "manager addition = %#v", managerAdded)
	managerLogin := loginIntegrationUser(t, helper.Handler(), managerUser.Username, password, model.SessionClientCLI, "exam-manager-cli")
	managerPrincipal, appErr := helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	managerInvocation := invocation.New(*managerPrincipal, model.RequestMetadata{RequestID: "exam-manager-integration"})
	_, appErr = helper.App.ListExamManagers(ctx, managerInvocation, exam.ListManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "Manager without role error = %v", appErr)
	managerBinding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: managerUser.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	require.NoError(t, err)
	managers, appErr := helper.App.ListExamManagers(ctx, managerInvocation, exam.ListManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	require.NoError(t, appErr, "Manager list = %#v", managers)
	require.Len(t, managers.Items, 2, "Manager list = %#v", managers)
	managerBindingEndedAt := model.NowUTC().Add(-500 * time.Millisecond)
	_, err = storetest.EndRoleBinding(ctx, persistence, managerBinding.ID, managerBindingEndedAt)
	require.NoError(t, err)
	_, appErr = helper.App.ListExamManagers(ctx, managerInvocation, exam.ListManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "Manager after role revocation error = %v", appErr)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: managerUser.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), StartsAt: model.TimeUTC(managerBindingEndedAt)})
	require.NoError(t, err)
	_, err = storetest.EndAcademicUnitMember(ctx, persistence, managerMembership.ID, managerMembership.Revision, model.NowUTC().Add(-100*time.Millisecond))
	require.NoError(t, err)
	_, appErr = helper.App.ListExamManagers(ctx, managerInvocation, exam.ListManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "Manager after membership loss error = %v", appErr)
	_, err = persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{AcademicUnitID: unit.ID, UserID: managerUser.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 50)})
	require.NoError(t, err)
	accountManagerRole, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: "exam-account-manager", DisplayName: "Exam Account Manager", Permissions: []string{string(model.ActionUserManage)}})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: teacher.ID, RoleID: accountManagerRole.ID, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000)})
	require.NoError(t, err)
	disabledManager, appErr := helper.App.SetUserEnabled(ctx, call, app.SetUserEnabledCommand{ID: managerUser.ID, Enabled: false})
	require.NoError(t, appErr)
	_, appErr = helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken)
	require.True(t, apperror.Is(appErr, "authentication.token_invalid"), "disabled Manager authentication error = %v", appErr)
	reenabledAt := model.NowUTC()
	reenableAudit, err := persistence.Audit().Save(ctx, &model.AuditEvent{
		ActorID: teacher.ID, Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: managerUser.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "exam-authoring-integration",
	})
	require.NoError(t, err)
	_, err = persistence.User().SetDisabled(ctx, storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: disabledManager.ID, ExpectedRevision: disabledManager.Revision, Disabled: false,
		ChangedAt: reenabledAt, AuditEventID: reenableAudit.ID, AuditAt: reenabledAt,
	}))
	require.NoError(t, err)
	transferred, appErr := helper.App.TransferExamOwnership(ctx, call, exam.TransferOwnerCommand{
		ExamID: created.Exam.ID, UserID: managerUser.ID, ExpectedExamRevision: 2, IdempotencyKey: "exam-owner-transfer",
	})
	require.NoError(t, appErr, "ownership transfer = %#v", transferred)
	require.Equal(t, managerUser.ID, transferred.Exam.OwnerUserID, "ownership transfer = %#v", transferred)
	require.Equal(t, int64(3), transferred.Exam.Revision, "ownership transfer = %#v", transferred)
	afterTransfer, appErr := helper.App.ListExamManagers(ctx, call, exam.ListManagersQuery{ExamID: created.Exam.ID, Limit: 50})
	require.NoError(t, appErr, "Manager relationships after ownership transfer = %#v", afterTransfer)
	require.Len(t, afterTransfer.Items, 2, "Manager relationships after ownership transfer = %#v", afterTransfer)
	relationships := map[model.UserID]bool{}
	for _, item := range afterTransfer.Items {
		relationships[item.Manager.UserID] = item.IsOwner
	}
	owner, remainsManager := relationships[teacher.ID]
	require.True(t, remainsManager)
	require.False(t, owner)
	require.True(t, relationships[managerUser.ID])
	removedCreator, appErr := helper.App.RemoveExamManager(ctx, managerInvocation, exam.RemoveManagerCommand{
		ExamID: created.Exam.ID, UserID: teacher.ID, ExpectedExamRevision: 3, IdempotencyKey: "exam-manager-remove-creator",
	})
	require.NoError(t, appErr, "creator relationship removal = %#v", removedCreator)
	require.Equal(t, int64(4), removedCreator.Exam.Revision, "creator relationship removal = %#v", removedCreator)
	require.Equal(t, teacher.ID, removedCreator.Manager.UserID, "creator relationship removal = %#v", removedCreator)
	currentExamRevision := int64(4)
	membershipEndedAt := model.NowUTC().Add(-100 * time.Millisecond)
	_, err = storetest.EndAcademicUnitMember(ctx, persistence, membership.ID, membership.Revision, membershipEndedAt)
	require.NoError(t, err)
	withoutMembership, appErr := helper.App.ListExams(ctx, call, exam.ListQuery{AcademicUnitID: unit.ID})
	require.NoError(t, appErr, "catalog after exact membership ended = %#v", withoutMembership)
	require.Empty(t, withoutMembership.Items, "catalog after exact membership ended = %#v", withoutMembership)
	_, err = persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: teacher.ID, StartsAt: model.TimeUTC(membershipEndedAt),
	})
	require.NoError(t, err)
	bindingEndedAt := model.NowUTC().Add(-50 * time.Millisecond)
	_, err = storetest.EndRoleBinding(ctx, persistence, binding.ID, bindingEndedAt)
	require.NoError(t, err)
	_, appErr = helper.App.ListExams(ctx, call, exam.ListQuery{AcademicUnitID: unit.ID})
	require.True(t, apperror.Is(appErr, "authorization.denied"), "catalog after role ended error = %v", appErr)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: teacher.ID, RoleID: role.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), StartsAt: model.TimeUTC(bindingEndedAt),
	})
	require.NoError(t, err)
	outsider, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{Username: "exam-outsider", Email: "exam-outsider@example.edu", DisplayName: "Exam Outsider"}, Password: password})
	require.NoError(t, appErr)
	outsiderLogin := loginIntegrationUser(t, helper.Handler(), outsider.Username, password, model.SessionClientCLI, "exam-outsider-cli")
	outsiderPrincipal, appErr := helper.App.AuthenticateAccess(ctx, outsiderLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	outsiderInvocation := invocation.New(*outsiderPrincipal, model.RequestMetadata{RequestID: "exam-get-denied-integration"})
	_, appErr = helper.App.GetExam(ctx, outsiderInvocation, exam.GetQuery{ExamID: created.Exam.ID})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "outsider get error = %v, want concealed resource.not_found", appErr)
	_, appErr = helper.App.ArchiveExam(ctx, outsiderInvocation, exam.ArchiveCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: created.Exam.Revision, IdempotencyKey: "exam-archive-denied",
	})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "outsider archive error = %v, want concealed resource.not_found", appErr)
	overrideRole, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: "exam-system-override", DisplayName: "Exam System Override", Permissions: []string{
		string(model.ActionExamViewOverride), string(model.ActionExamManageOverride),
	}})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: outsider.ID, RoleID: overrideRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)
	overrideAdded, appErr := helper.App.AddExamManager(ctx, outsiderInvocation, exam.AddManagerCommand{
		ExamID: created.Exam.ID, UserID: teacher.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-manager-override-add",
	})
	require.NoError(t, appErr, "override Manager addition = %#v", overrideAdded)
	require.Equal(t, currentExamRevision+1, overrideAdded.Exam.Revision, "override Manager addition = %#v", overrideAdded)
	require.Equal(t, teacher.ID, overrideAdded.Manager.UserID, "override Manager addition = %#v", overrideAdded)
	currentExamRevision = overrideAdded.Exam.Revision
	overridePage, appErr := helper.App.ListExams(ctx, outsiderInvocation, exam.ListQuery{AcademicUnitID: unit.ID})
	require.NoError(t, appErr, "override catalog = %#v", overridePage)
	require.Len(t, overridePage.Items, 1, "override catalog = %#v", overridePage)
	require.Equal(t, created.Exam.ID, overridePage.Items[0].ID, "override catalog = %#v", overridePage)
	viewOverrideAudits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: outsider.ID, Action: string(model.ActionExamViewOverride),
		Resource: &model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "override list audits")
	require.Len(t, viewOverrideAudits, 1, "override list audits")
	require.Equal(t, model.AuditStatusSuccess, viewOverrideAudits[0].Status, "override list audits")
	require.Equal(t, model.RoleScopeInstitution, viewOverrideAudits[0].ScopeType, "override list audits")
	require.Equal(t, institution.ID.String(), viewOverrideAudits[0].ScopeID, "override list audits")
	archived, appErr := helper.App.ArchiveExam(ctx, outsiderInvocation, exam.ArchiveCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-archive-once",
	})
	require.NoError(t, appErr)
	require.True(t, archived.IsArchived())
	require.Equal(t, currentExamRevision+1, archived.Revision)
	manageOverrideAudits, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: outsider.ID, Action: string(model.ActionExamManageOverride),
		Resource: &model.Resource{Type: model.ResourceExam, ID: created.Exam.ID.String()}, Limit: 10,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	manageStatuses := map[model.AuditStatus]int{}
	for _, event := range manageOverrideAudits {
		manageStatuses[event.Status]++
		require.Equal(t, model.RoleScopeAcademicUnit, event.ScopeType)
		require.Equal(t, unit.ID.String(), event.ScopeID)
	}
	require.Len(t, manageOverrideAudits, 5, "override archive audits = %#v, %v", manageOverrideAudits, err)
	require.Equal(t, 4, manageStatuses[model.AuditStatusSuccess], "override archive audits = %#v, %v", manageOverrideAudits, err)
	require.Equal(t, 1, manageStatuses[model.AuditStatusFail], "override archive audits = %#v, %v", manageOverrideAudits, err)
	archiveReplay, appErr := helper.App.ArchiveExam(ctx, outsiderInvocation, exam.ArchiveCommand{
		ExamID: created.Exam.ID, ExpectedExamRevision: currentExamRevision, IdempotencyKey: "exam-archive-once",
	})
	require.NoError(t, appErr, "archive replay = %#v", archiveReplay)
	require.Equal(t, archived.Revision, archiveReplay.Revision, "archive replay = %#v", archiveReplay)
	require.True(t, archiveReplay.ArchivedAt.Time.Equal(archived.ArchivedAt.Time), "archive replay = %#v", archiveReplay)
	active, appErr = helper.App.ListExams(ctx, call, exam.ListQuery{AcademicUnitID: unit.ID})
	require.NoError(t, appErr, "active catalog after archive = %#v", active)
	require.Empty(t, active.Items, "active catalog after archive = %#v", active)
	archivedPage, appErr := helper.App.ListExams(ctx, call, exam.ListQuery{AcademicUnitID: unit.ID, ArchiveFilter: store.ExamArchiveArchived})
	require.NoError(t, appErr, "archived catalog = %#v", archivedPage)
	require.Len(t, archivedPage.Items, 1, "archived catalog = %#v", archivedPage)
	require.Equal(t, archived.ID, archivedPage.Items[0].ID, "archived catalog = %#v", archivedPage)
	require.True(t, archivedPage.Items[0].ArchivedAt.Valid, "archived catalog = %#v", archivedPage)
	require.Equal(t, archived.Revision, archivedPage.Items[0].Revision, "archived catalog = %#v", archivedPage)
	archivedView, appErr := helper.App.GetExam(ctx, call, exam.GetQuery{ExamID: created.Exam.ID})
	require.NoError(t, appErr, "archived exact Get = %#v", archivedView)
	require.True(t, archivedView.Exam.IsArchived(), "archived exact Get = %#v", archivedView)
	require.Equal(t, editedTitle, archivedView.Draft.Title, "archived exact Get = %#v", archivedView)
	postArchiveTitle := "Cannot edit archived Exam"
	_, appErr = helper.App.EditExamDraftText(ctx, call, exam.EditDraftTextCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: focusPolicy.Draft.Revision,
		Title: &postArchiveTitle, IdempotencyKey: "exam-edit-after-archive",
	})
	require.True(t, apperror.Is(appErr, "exam.archived"), "post-archive edit error = %v", appErr)

	startIntegrationServer(t, helper)
	managerMailKeys := []model.MailTemplateKey{
		model.MailTemplateExamManagerAdded,
		model.MailTemplateExamManagerRemoved,
		model.MailTemplateExamOwnershipTransferredToYou,
		model.MailTemplateExamOwnershipTransferredFromYou,
	}
	deadline := time.Now().Add(10 * time.Second)
	var managerDeliveries []*model.MailDelivery
	for {
		managerDeliveries, err = persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: managerMailKeys, Limit: 20})
		require.NoError(t, err)
		accepted := len(managerDeliveries) == 5
		for _, delivery := range managerDeliveries {
			accepted = accepted && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0
		}
		if accepted {
			break
		}
		require.False(t, time.Now().After(deadline), "Exam Manager notices did not reach accepted: %#v", managerDeliveries)
		time.Sleep(20 * time.Millisecond)
	}
	wantTargets := map[model.MailTemplateKey]map[model.UserID]int{
		model.MailTemplateExamManagerAdded:                {managerUser.ID: 1, teacher.ID: 1},
		model.MailTemplateExamManagerRemoved:              {teacher.ID: 1},
		model.MailTemplateExamOwnershipTransferredFromYou: {teacher.ID: 1},
		model.MailTemplateExamOwnershipTransferredToYou:   {managerUser.ID: 1},
	}
	for _, delivery := range managerDeliveries {
		wantTargets[delivery.TemplateKey][delivery.TargetUserID]--
	}
	for key, targets := range wantTargets {
		for target, remaining := range targets {
			require.Equal(t, 0, remaining, "Exam Manager delivery %s/%s remaining = %d; deliveries=%#v", key, target, remaining, managerDeliveries)
		}
	}
	var managerMessages int
	for _, delivery := range helper.Mailer.Deliveries() {
		data := string(delivery.Data)
		if !strings.Contains(data, "Subject: ") || !strings.Contains(data, "exam manager") && !strings.Contains(data, "Exam ownership") {
			continue
		}
		managerMessages++
		require.True(t, bytes.Contains(delivery.Data, []byte(editedTitle)), "Exam Manager mail omitted safe Exam identity: %s", delivery.Data)
		require.NotContains(t, data, "private-reason", "Exam Manager mail exposed actor or private authorization detail")
		require.NotContains(t, data, "role.manage", "Exam Manager mail exposed actor or private authorization detail")
		require.NotContains(t, data, outsider.Email, "Exam Manager mail exposed actor or private authorization detail")
	}
	require.Equal(t, 5, managerMessages, "captured Exam Manager messages")
}
