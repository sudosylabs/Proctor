//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestExamSittingIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	secondaryPersistence := openAdditionalUserSettingsStore(t, dataSource)
	configure := func(nodeID string) func(*config.Config) {
		return func(cfg *config.Config) {
			cfg.Cluster.NodeID = nodeID
			cfg.Server.ListenAddress = "127.0.0.1:0"
		}
	}
	helper := testlib.Setup(t, testlib.WithConfig(configure("exam-sitting-node-a")), testlib.WithStore(persistence))
	secondary := testlib.Setup(t, testlib.WithConfig(configure("exam-sitting-node-b")), testlib.WithStore(secondaryPersistence))
	ctx := context.Background()
	const password = "correct horse battery staple"

	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, invocation.Invocation{}, app.BootstrapInstallationCommand{
		InstitutionName: "sitting-university", InstitutionDisplayName: "Sitting University",
		AdministratorUsername: "sitting-administrator", AdministratorEmail: "sitting-administrator@example.edu",
		AdministratorDisplayName: "Sitting Administrator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr)
	institution := bootstrap.Institution
	unit, err := storetest.SaveAcademicUnit(ctx, persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "sitting-computing", DisplayName: "Sitting Computing",
	})
	require.NoError(t, err)
	programme, err := storetest.SaveProgramme(ctx, persistence, &model.Programme{
		AcademicUnitID: unit.ID, Name: "sitting-computer-science", DisplayName: "Sitting Computer Science",
	})
	require.NoError(t, err)
	level, err := storetest.SaveProgrammeLevel(ctx, persistence, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "sitting-year-one", DisplayName: "Sitting Year One",
	})
	require.NoError(t, err)
	now := time.Now().UTC()
	period, err := storetest.SaveAcademicPeriod(ctx, persistence, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "sitting-period", DisplayName: "Sitting Period",
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	class, err := storetest.SaveClass(ctx, persistence, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "sitting-class-a", DisplayName: "Sitting Class A",
	})
	require.NoError(t, err)

	candidate, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "sitting-candidate", Email: "sitting-candidate@example.edu", DisplayName: "Sitting Candidate",
		EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, Password: password})
	require.NoError(t, appErr)
	_, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: period.StartsAt,
	})
	require.NoError(t, err)
	_, err = persistence.ClassMember().SeedEnroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: candidate.ID, StartsAt: period.StartsAt,
	})
	require.NoError(t, err)
	manager, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "sitting-manager", Email: "sitting-manager@example.edu", DisplayName: "Sitting Manager",
	}, Password: password})
	require.NoError(t, appErr)
	managerLogin, appErr := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{
		LoginID: manager.Username, Password: password, ClientType: model.SessionClientCLI,
		DeviceID: "sitting-manager-cli", Source: "127.0.0.1:1",
	})
	require.NoError(t, appErr, "manager login: %v; logs=%s", appErr, helper.Logs.String())
	managerPrincipal, appErr := helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	managerInvocation := invocation.New(*managerPrincipal, model.RequestMetadata{RequestID: "exam-sitting-manager-integration"})
	managerMembership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: manager.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)
	managerRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "exam-sitting-manager", DisplayName: "Exam Sitting Manager",
		Permissions: []string{
			string(model.ActionExamCreate), string(model.ActionExamView), string(model.ActionExamManage),
			string(model.ActionExamPublish), string(model.ActionExamSittingCreate),
			string(model.ActionExamSittingView), string(model.ActionExamSittingManage),
		},
	})
	require.NoError(t, err)
	managerBinding, err := storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: manager.ID, RoleID: managerRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)

	created, appErr := helper.App.CreateExam(ctx, managerInvocation, exam.CreateCommand{
		AcademicUnitID: unit.ID, Title: "Concurrent Systems", InstructionsMarkdown: "Write **Go**.", IdempotencyKey: "sitting-exam-create",
	})
	require.NoError(t, appErr)
	published, appErr := helper.App.PublishExamRevision(ctx, managerInvocation, exam.PublishRevisionCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision, IdempotencyKey: "sitting-exam-publish",
	})
	require.NoError(t, appErr)
	startIntegrationServer(t, helper)
	startIntegrationServer(t, secondary)

	startAt := now.Add(2 * time.Hour).Truncate(time.Millisecond)
	endAt := startAt.Add(2 * time.Hour)
	schedule := sitting.ScheduleCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: startAt, ScheduledEndAt: endAt, IdempotencyKey: "sitting-schedule-once",
	}
	scheduled, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, schedule)
	require.NoError(t, appErr)
	require.NotNil(t, scheduled.Sitting)
	require.Equal(t, created.Exam.ID, scheduled.Sitting.ExamID)
	require.Equal(t, published.ID, scheduled.Sitting.ExamRevisionID)
	require.Equal(t, class.ID, scheduled.Sitting.ClassID)
	require.Equal(t, model.ExamSittingScheduled, scheduled.Sitting.State)
	require.Equal(t, int64(1), scheduled.Sitting.Revision)
	scheduledReplay, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, schedule)
	require.NoError(t, appErr, "schedule replay = %#v", scheduledReplay)
	require.NotNil(t, scheduledReplay.Sitting, "schedule replay = %#v", scheduledReplay)
	require.Equal(t, scheduled.Sitting.ID, scheduledReplay.Sitting.ID, "schedule replay = %#v", scheduledReplay)
	require.Equal(t, int64(1), scheduledReplay.Sitting.Revision, "schedule replay = %#v", scheduledReplay)
	waitForExamSittingMailDelivery(t, ctx, persistence, helper, secondary, candidate.ID,
		model.MailTemplateExamSittingScheduled)

	got, appErr := helper.App.GetExamSitting(ctx, managerInvocation, sitting.GetQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	})
	require.NoError(t, appErr, "get Sitting = %#v", got)
	require.NotNil(t, got.Sitting, "get Sitting = %#v", got)
	require.Equal(t, scheduled.Sitting.ID, got.Sitting.ID, "get Sitting = %#v", got)
	page, appErr := helper.App.ListExamSittings(ctx, managerInvocation, sitting.ListQuery{
		ExamID: created.Exam.ID, ClassID: class.ID, States: []model.ExamSittingState{model.ExamSittingScheduled}, Limit: 50,
	})
	require.NoError(t, appErr, "list Sittings = %#v", page)
	require.False(t, page.HasMore, "list Sittings = %#v", page)
	require.Len(t, page.Items, 1, "list Sittings = %#v", page)
	require.NotNil(t, page.Items[0].Sitting, "list Sittings = %#v", page)
	require.Equal(t, scheduled.Sitting.ID, page.Items[0].Sitting.ID, "list Sittings = %#v", page)

	updatedEndAt := endAt.Add(30 * time.Minute)
	update := sitting.UpdateScheduleCommand{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID, ExpectedRevision: scheduled.Sitting.Revision,
		ScheduledEndAt: &updatedEndAt, IdempotencyKey: "sitting-update-once",
	}
	updated, appErr := helper.App.UpdateExamSittingSchedule(ctx, managerInvocation, update)
	require.NoError(t, appErr)
	require.NotNil(t, updated.Sitting)
	require.Equal(t, int64(2), updated.Sitting.Revision)
	require.True(t, updated.Sitting.ScheduledEndAt.Equal(updatedEndAt))
	updatedReplay, appErr := helper.App.UpdateExamSittingSchedule(ctx, managerInvocation, update)
	require.NoError(t, appErr, "update replay = %#v", updatedReplay)
	require.NotNil(t, updatedReplay.Sitting, "update replay = %#v", updatedReplay)
	require.Equal(t, updated.Sitting.Revision, updatedReplay.Sitting.Revision, "update replay = %#v", updatedReplay)
	require.True(t, updatedReplay.Sitting.UpdatedAt.Equal(updated.Sitting.UpdatedAt), "update replay = %#v", updatedReplay)

	cancel := sitting.CancelCommand{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID, ExpectedRevision: updated.Sitting.Revision,
		PrivateReason: "Schedule superseded by the department", IdempotencyKey: "sitting-cancel-once",
	}
	canceled, appErr := helper.App.CancelExamSitting(ctx, managerInvocation, cancel)
	require.NoError(t, appErr)
	require.NotNil(t, canceled.Sitting)
	require.Equal(t, model.ExamSittingCanceled, canceled.Sitting.State)
	require.Equal(t, int64(3), canceled.Sitting.Revision)
	require.Equal(t, model.ExamSittingReasonManagerCanceled, canceled.Sitting.ReasonCode)
	require.True(t, canceled.Sitting.CanceledAt.Valid)
	canceledReplay, appErr := helper.App.CancelExamSitting(ctx, managerInvocation, cancel)
	require.NoError(t, appErr, "cancel replay = %#v", canceledReplay)
	require.NotNil(t, canceledReplay.Sitting, "cancel replay = %#v", canceledReplay)
	require.Equal(t, canceled.Sitting.Revision, canceledReplay.Sitting.Revision, "cancel replay = %#v", canceledReplay)
	require.True(t, canceledReplay.Sitting.CanceledAt.Time.Equal(canceled.Sitting.CanceledAt.Time), "cancel replay = %#v", canceledReplay)

	// Exercise the production lifecycle Job factory through the public manager
	// facade and the real PostgreSQL Store. The direct state advance stands in
	// for the separately tested delayed opening Job so this test can focus on
	// the user-command composition seam.
	live, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, sitting.ScheduleCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(10 * time.Hour).Truncate(time.Millisecond),
		ScheduledEndAt:   now.Add(12 * time.Hour).Truncate(time.Millisecond),
		IdempotencyKey:   "sitting-live-schedule",
	})
	require.NoError(t, appErr, "live schedule = %#v", live)
	require.NotNil(t, live.Sitting, "live schedule = %#v", live)
	openedAt := time.Now().UTC().Truncate(time.Millisecond)
	result, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sittings SET state='open',opened_at=?,updated_at=?,revision=2 WHERE id=? AND state='scheduled' AND revision=1`,
		openedAt, openedAt, live.Sitting.ID.String())
	require.NoError(t, err)
	affected, affectedErr := result.RowsAffected()
	require.NoError(t, affectedErr, "open live Sitting affected")
	require.Equal(t, int64(1), affected, "open live Sitting affected")
	paused, appErr := helper.App.PauseExamSitting(ctx, managerInvocation, sitting.PauseCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 2,
		PrivateReason: "Investigating a delivery interruption", IdempotencyKey: "sitting-live-pause",
	})
	require.NoError(t, appErr, "pause live Sitting = %#v", paused)
	require.NotNil(t, paused.Sitting, "pause live Sitting = %#v", paused)
	require.Equal(t, model.ExamSittingPaused, paused.Sitting.State, "pause live Sitting = %#v", paused)
	require.Equal(t, int64(3), paused.Sitting.Revision, "pause live Sitting = %#v", paused)
	resumed, appErr := helper.App.ResumeExamSitting(ctx, managerInvocation, sitting.ResumeCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 3,
		PrivateReason: "Delivery interruption resolved", IdempotencyKey: "sitting-live-resume",
	})
	require.NoError(t, appErr, "resume live Sitting = %#v", resumed)
	require.NotNil(t, resumed.Sitting, "resume live Sitting = %#v", resumed)
	require.Equal(t, model.ExamSittingOpen, resumed.Sitting.State, "resume live Sitting = %#v", resumed)
	require.Equal(t, int64(4), resumed.Sitting.Revision, "resume live Sitting = %#v", resumed)
	liveEnd := live.Sitting.ScheduledEndAt.Add(30 * time.Minute)
	extended, appErr := helper.App.ExtendExamSitting(ctx, managerInvocation, sitting.ExtendCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 4, ScheduledEndAt: liveEnd,
		PrivateReason: "Compensating for the interruption", IdempotencyKey: "sitting-live-extend",
	})
	require.NoError(t, appErr, "extend live Sitting = %#v", extended)
	require.NotNil(t, extended.Sitting, "extend live Sitting = %#v", extended)
	require.Equal(t, int64(5), extended.Sitting.Revision, "extend live Sitting = %#v", extended)
	require.True(t, extended.Sitting.ScheduledEndAt.Equal(liveEnd), "extend live Sitting = %#v", extended)
	closedEarly, appErr := helper.App.CloseExamSitting(ctx, managerInvocation, sitting.EarlyCloseCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 5,
		PrivateReason: "Authorized early close", IdempotencyKey: "sitting-live-close",
	})
	require.NoError(t, appErr, "close live Sitting = %#v", closedEarly)
	require.NotNil(t, closedEarly.Sitting, "close live Sitting = %#v", closedEarly)
	require.Equal(t, model.ExamSittingClosing, closedEarly.Sitting.State, "close live Sitting = %#v", closedEarly)
	require.Equal(t, int64(6), closedEarly.Sitting.Revision, "close live Sitting = %#v", closedEarly)
	require.Equal(t, model.ExamSittingReasonManagerClosed, closedEarly.Sitting.ReasonCode, "close live Sitting = %#v", closedEarly)
	closedReplay, appErr := helper.App.CloseExamSitting(ctx, managerInvocation, sitting.EarlyCloseCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 5,
		PrivateReason: "Authorized early close", IdempotencyKey: "sitting-live-close",
	})
	require.NoError(t, appErr, "close live replay = %#v", closedReplay)
	require.NotNil(t, closedReplay.Sitting, "close live replay = %#v", closedReplay)
	require.Equal(t, closedEarly.Sitting.Revision, closedReplay.Sitting.Revision, "close live replay = %#v", closedReplay)
	require.True(t, closedReplay.Sitting.ClosingAt.Time.Equal(closedEarly.Sitting.ClosingAt.Time), "close live replay = %#v", closedReplay)

	_, err = storetest.EndAcademicUnitMember(ctx, persistence, managerMembership.ID, managerMembership.Revision, model.NowUTC().Add(-100*time.Millisecond))
	require.NoError(t, err)
	_, appErr = helper.App.GetExamSitting(ctx, managerInvocation, sitting.GetQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "get after membership revocation error = %v", appErr)
	appErr = helper.App.AuthorizeWebSocketSubscription(ctx, invocation.New(*managerPrincipal, model.RequestMetadata{}),
		model.ActionExamSittingView, model.Resource{Type: model.ResourceExamSitting, ID: scheduled.Sitting.ID.String()})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "subscription after membership revocation error = %v", appErr)
	_, err = persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: manager.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 50),
	})
	require.NoError(t, err)
	_, err = storetest.EndRoleBinding(ctx, persistence, managerBinding.ID, model.NowUTC().Add(-25*time.Millisecond))
	require.NoError(t, err)
	_, appErr = helper.App.GetExamSitting(ctx, managerInvocation, sitting.GetQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "get after permission revocation error = %v", appErr)
	appErr = helper.App.AuthorizeWebSocketSubscription(ctx, invocation.New(*managerPrincipal, model.RequestMetadata{}),
		model.ActionExamSittingView, model.Resource{Type: model.ResourceExamSitting, ID: scheduled.Sitting.ID.String()})
	require.True(t, apperror.Is(appErr, "resource.not_found"), "subscription after permission revocation error = %v", appErr)

	overrideUser, appErr := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "sitting-override", Email: "sitting-override@example.edu", DisplayName: "Sitting Override",
	}, Password: password})
	require.NoError(t, appErr)
	overrideLogin := loginIntegrationUser(t, helper.Handler(), overrideUser.Username, password, model.SessionClientCLI, "sitting-override-cli")
	overridePrincipal, appErr := helper.App.AuthenticateAccess(ctx, overrideLogin.Tokens.AccessToken)
	require.NoError(t, appErr)
	overrideInvocation := invocation.New(*overridePrincipal, model.RequestMetadata{RequestID: "exam-sitting-override-integration"})
	overrideRole, err := storetest.SaveRole(ctx, persistence, &model.Role{
		Name: "exam-sitting-override", DisplayName: "Exam Sitting Override",
		Permissions: []string{
			string(model.ActionExamViewOverride), string(model.ActionExamSittingCreateOverride),
			string(model.ActionExamSittingViewOverride), string(model.ActionExamSittingManageOverride),
		},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{
		UserID: overrideUser.ID, RoleID: overrideRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	require.NoError(t, err)

	overrideSchedule := sitting.ScheduleCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(6 * time.Hour).Truncate(time.Millisecond),
		ScheduledEndAt:   now.Add(8 * time.Hour).Truncate(time.Millisecond),
		IdempotencyKey:   "sitting-override-schedule",
	}
	overrideScheduled, appErr := helper.App.ScheduleExamSitting(ctx, overrideInvocation, overrideSchedule)
	require.NoError(t, appErr, "override schedule = %#v", overrideScheduled)
	require.NotNil(t, overrideScheduled.Sitting, "override schedule = %#v", overrideScheduled)
	_, appErr = helper.App.GetExamSitting(ctx, overrideInvocation, sitting.GetQuery{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID,
	})
	require.NoError(t, appErr)
	overridePage, appErr := helper.App.ListExamSittings(ctx, overrideInvocation, sitting.ListQuery{
		ExamID: created.Exam.ID, Limit: 50,
	})
	require.NoError(t, appErr, "override list = %#v", overridePage)
	require.Len(t, overridePage.Items, 3, "override list = %#v", overridePage)
	overrideEndAt := overrideSchedule.ScheduledEndAt.Add(30 * time.Minute)
	overrideUpdated, appErr := helper.App.UpdateExamSittingSchedule(ctx, overrideInvocation, sitting.UpdateScheduleCommand{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID, ExpectedRevision: 1,
		ScheduledEndAt: &overrideEndAt, IdempotencyKey: "sitting-override-update",
	})
	require.NoError(t, appErr, "override update = %#v", overrideUpdated)
	require.NotNil(t, overrideUpdated.Sitting, "override update = %#v", overrideUpdated)
	require.Equal(t, int64(2), overrideUpdated.Sitting.Revision, "override update = %#v", overrideUpdated)
	_, appErr = helper.App.CancelExamSitting(ctx, overrideInvocation, sitting.CancelCommand{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID, ExpectedRevision: 2,
		PrivateReason: "Administrator canceled the delivery", IdempotencyKey: "sitting-override-cancel",
	})
	require.NoError(t, appErr)

	assertExamSittingOverrideAudit(t, ctx, persistence, overrideUser.ID, model.ActionExamSittingCreateOverride,
		model.Resource{Type: model.ResourceExam, ID: created.Exam.ID.String()}, unit.ID)
	assertExamSittingOverrideAudit(t, ctx, persistence, overrideUser.ID, model.ActionExamSittingManageOverride,
		model.Resource{Type: model.ResourceExamSitting, ID: overrideScheduled.Sitting.ID.String()}, unit.ID)
}

func waitForExamSittingMailDelivery(t *testing.T, ctx context.Context, persistence store.Store,
	primary, secondary *testlib.Helper, target model.UserID, templateKey model.MailTemplateKey,
) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		deliveries, err := persistence.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{
			TemplateKeys: []model.MailTemplateKey{templateKey}, Limit: 20,
		})
		require.NoError(t, err)
		for _, delivery := range deliveries {
			if delivery.TargetUserID == target && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0 {
				require.NotEqual(t, 0, len(primary.Mailer.Deliveries())+len(secondary.Mailer.Deliveries()), "accepted Sitting delivery was not observed by either server.New mail adapter")
				return
			}
		}
		require.False(t, time.Now().After(deadline), "Sitting %s mail did not converge across two nodes: %#v; primary logs=%s; secondary logs=%s", templateKey, deliveries, primary.Logs.String(), secondary.Logs.String())
		time.Sleep(20 * time.Millisecond)
	}
}

func assertExamSittingOverrideAudit(t *testing.T, ctx context.Context, persistence store.Store, actorID model.UserID,
	action model.Action, resource model.Resource, unitID model.AcademicUnitID,
) {
	t.Helper()
	events, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorID: actorID, Action: string(action), Resource: &resource, Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess && event.ScopeType == model.RoleScopeAcademicUnit && event.ScopeID == unitID.String() {
			return
		}
	}
	require.FailNow(t, fmt.Sprintf("no successful %s audit for %s in Academic Unit %s: %#v", action, resource.ID, unitID, events))
}
