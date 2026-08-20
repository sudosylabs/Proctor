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
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestExamSittingIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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

	bootstrap, appErr := helper.App.BootstrapInstallation(ctx, application.Invocation{}, application.BootstrapInstallationCommand{
		InstitutionName: "sitting-university", InstitutionDisplayName: "Sitting University",
		AdministratorUsername: "sitting-administrator", AdministratorEmail: "sitting-administrator@example.edu",
		AdministratorDisplayName: "Sitting Administrator", AdministratorLocale: "en", AdministratorTimezone: "UTC",
		Password: password, BootstrapSecret: testlib.BootstrapSecret, Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	institution := bootstrap.Institution
	unit, err := persistence.AcademicUnit().Save(ctx, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "sitting-computing", DisplayName: "Sitting Computing",
	})
	if err != nil {
		t.Fatal(err)
	}
	programme, err := persistence.Programme().Save(ctx, &model.Programme{
		AcademicUnitID: unit.ID, Name: "sitting-computer-science", DisplayName: "Sitting Computer Science",
	})
	if err != nil {
		t.Fatal(err)
	}
	level, err := persistence.ProgrammeLevel().Save(ctx, &model.ProgrammeLevel{
		ProgrammeID: programme.ID, Name: "sitting-year-one", DisplayName: "Sitting Year One",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period, err := persistence.AcademicPeriod().Save(ctx, &model.AcademicPeriod{
		Owner: model.NewInstitutionAcademicPeriodOwner(institution.ID), Name: "sitting-period", DisplayName: "Sitting Period",
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	class, err := persistence.Class().Save(ctx, &model.Class{
		ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "sitting-class-a", DisplayName: "Sitting Class A",
	})
	if err != nil {
		t.Fatal(err)
	}

	candidate, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "sitting-candidate", Email: "sitting-candidate@example.edu", DisplayName: "Sitting Candidate",
		EmailVerified: true, Locale: "en", Timezone: "UTC",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, err = persistence.Affiliation().Save(ctx, &model.Affiliation{
		UserID: candidate.ID, Kind: model.AffiliationStudent, StartsAt: period.StartsAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.ClassMember().Enroll(ctx, &model.ClassMember{
		ClassID: class.ID, UserID: candidate.ID, StartsAt: period.StartsAt,
	}); err != nil {
		t.Fatal(err)
	}
	manager, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "sitting-manager", Email: "sitting-manager@example.edu", DisplayName: "Sitting Manager",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	managerLogin, appErr := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID: manager.Username, Password: password, ClientType: model.SessionClientCLI,
		DeviceID: "sitting-manager-cli", Source: "127.0.0.1:1",
	})
	if appErr != nil {
		t.Fatalf("manager login: %v; logs=%s", appErr, helper.Logs.String())
	}
	managerPrincipal, appErr := helper.App.AuthenticateAccess(ctx, managerLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	managerInvocation := application.NewInvocation(*managerPrincipal, model.RequestMetadata{RequestID: "exam-sitting-manager-integration"})
	managerMembership, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: manager.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	managerRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "exam-sitting-manager", DisplayName: "Exam Sitting Manager",
		Permissions: []string{
			string(model.ActionExamCreate), string(model.ActionExamView), string(model.ActionExamManage),
			string(model.ActionExamPublish), string(model.ActionExamSittingCreate),
			string(model.ActionExamSittingView), string(model.ActionExamSittingManage),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	managerBinding, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: manager.ID, RoleID: managerRole.ID, ScopeType: model.RoleScopeAcademicUnit,
		ScopeID: unit.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	})
	if err != nil {
		t.Fatal(err)
	}

	created, appErr := helper.App.CreateExam(ctx, managerInvocation, application.CreateExamCommand{
		AcademicUnitID: unit.ID, Title: "Concurrent Systems", InstructionsMarkdown: "Write **Go**.", IdempotencyKey: "sitting-exam-create",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	published, appErr := helper.App.PublishExamRevision(ctx, managerInvocation, application.PublishExamRevisionCommand{
		ExamID: created.Exam.ID, ExpectedDraftRevision: created.Draft.Revision, IdempotencyKey: "sitting-exam-publish",
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	startIntegrationServer(t, helper)
	startIntegrationServer(t, secondary)

	startAt := now.Add(2 * time.Hour).Truncate(time.Millisecond)
	endAt := startAt.Add(2 * time.Hour)
	schedule := application.ScheduleExamSittingCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: startAt, ScheduledEndAt: endAt, IdempotencyKey: "sitting-schedule-once",
	}
	scheduled, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, schedule)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if scheduled.Sitting == nil || scheduled.Sitting.ExamID != created.Exam.ID || scheduled.Sitting.ExamRevisionID != published.ID ||
		scheduled.Sitting.ClassID != class.ID || scheduled.Sitting.State != model.ExamSittingScheduled || scheduled.Sitting.Revision != 1 {
		t.Fatalf("scheduled Sitting = %#v", scheduled)
	}
	scheduledReplay, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, schedule)
	if appErr != nil || scheduledReplay.Sitting == nil || scheduledReplay.Sitting.ID != scheduled.Sitting.ID || scheduledReplay.Sitting.Revision != 1 {
		t.Fatalf("schedule replay = %#v, %v", scheduledReplay, appErr)
	}
	waitForExamSittingMailDelivery(t, ctx, persistence, helper, secondary, candidate.ID,
		model.MailTemplateExamSittingScheduled)

	got, appErr := helper.App.GetExamSitting(ctx, managerInvocation, application.GetExamSittingQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	})
	if appErr != nil || got.Sitting == nil || got.Sitting.ID != scheduled.Sitting.ID {
		t.Fatalf("get Sitting = %#v, %v", got, appErr)
	}
	page, appErr := helper.App.ListExamSittings(ctx, managerInvocation, application.ListExamSittingsQuery{
		ExamID: created.Exam.ID, ClassID: class.ID, States: []model.ExamSittingState{model.ExamSittingScheduled}, Limit: 50,
	})
	if appErr != nil || page.HasMore || len(page.Items) != 1 || page.Items[0].Sitting == nil || page.Items[0].Sitting.ID != scheduled.Sitting.ID {
		t.Fatalf("list Sittings = %#v, %v", page, appErr)
	}

	updatedEndAt := endAt.Add(30 * time.Minute)
	update := application.UpdateExamSittingScheduleCommand{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID, ExpectedRevision: scheduled.Sitting.Revision,
		ScheduledEndAt: &updatedEndAt, IdempotencyKey: "sitting-update-once",
	}
	updated, appErr := helper.App.UpdateExamSittingSchedule(ctx, managerInvocation, update)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if updated.Sitting == nil || updated.Sitting.Revision != 2 || !updated.Sitting.ScheduledEndAt.Equal(updatedEndAt) {
		t.Fatalf("updated Sitting = %#v", updated)
	}
	updatedReplay, appErr := helper.App.UpdateExamSittingSchedule(ctx, managerInvocation, update)
	if appErr != nil || updatedReplay.Sitting == nil || updatedReplay.Sitting.Revision != updated.Sitting.Revision ||
		!updatedReplay.Sitting.UpdatedAt.Equal(updated.Sitting.UpdatedAt) {
		t.Fatalf("update replay = %#v, %v", updatedReplay, appErr)
	}

	cancel := application.CancelExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID, ExpectedRevision: updated.Sitting.Revision,
		PrivateReason: "Schedule superseded by the department", IdempotencyKey: "sitting-cancel-once",
	}
	canceled, appErr := helper.App.CancelExamSitting(ctx, managerInvocation, cancel)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if canceled.Sitting == nil || canceled.Sitting.State != model.ExamSittingCanceled || canceled.Sitting.Revision != 3 ||
		canceled.Sitting.ReasonCode != model.ExamSittingReasonManagerCanceled || !canceled.Sitting.CanceledAt.Valid {
		t.Fatalf("canceled Sitting = %#v", canceled)
	}
	canceledReplay, appErr := helper.App.CancelExamSitting(ctx, managerInvocation, cancel)
	if appErr != nil || canceledReplay.Sitting == nil || canceledReplay.Sitting.Revision != canceled.Sitting.Revision ||
		!canceledReplay.Sitting.CanceledAt.Time.Equal(canceled.Sitting.CanceledAt.Time) {
		t.Fatalf("cancel replay = %#v, %v", canceledReplay, appErr)
	}

	// Exercise the production lifecycle Job factory through the public manager
	// facade and the real PostgreSQL Store. The direct state advance stands in
	// for the separately tested delayed opening Job so this test can focus on
	// the user-command composition seam.
	live, appErr := helper.App.ScheduleExamSitting(ctx, managerInvocation, application.ScheduleExamSittingCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(10 * time.Hour).Truncate(time.Millisecond),
		ScheduledEndAt:   now.Add(12 * time.Hour).Truncate(time.Millisecond),
		IdempotencyKey:   "sitting-live-schedule",
	})
	if appErr != nil || live.Sitting == nil {
		t.Fatalf("live schedule = %#v, %v", live, appErr)
	}
	openedAt := time.Now().UTC().Truncate(time.Millisecond)
	result, err := persistence.GetMaster().Exec(ctx, `UPDATE exam_sittings SET state='open',opened_at=?,updated_at=?,revision=2 WHERE id=? AND state='scheduled' AND revision=1`,
		openedAt, openedAt, live.Sitting.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		t.Fatalf("open live Sitting affected=%d error=%v", affected, affectedErr)
	}
	paused, appErr := helper.App.PauseExamSitting(ctx, managerInvocation, application.PauseExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 2,
		PrivateReason: "Investigating a delivery interruption", IdempotencyKey: "sitting-live-pause",
	})
	if appErr != nil || paused.Sitting == nil || paused.Sitting.State != model.ExamSittingPaused || paused.Sitting.Revision != 3 {
		t.Fatalf("pause live Sitting = %#v, %v", paused, appErr)
	}
	resumed, appErr := helper.App.ResumeExamSitting(ctx, managerInvocation, application.ResumeExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 3,
		PrivateReason: "Delivery interruption resolved", IdempotencyKey: "sitting-live-resume",
	})
	if appErr != nil || resumed.Sitting == nil || resumed.Sitting.State != model.ExamSittingOpen || resumed.Sitting.Revision != 4 {
		t.Fatalf("resume live Sitting = %#v, %v", resumed, appErr)
	}
	liveEnd := live.Sitting.ScheduledEndAt.Add(30 * time.Minute)
	extended, appErr := helper.App.ExtendExamSitting(ctx, managerInvocation, application.ExtendExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 4, ScheduledEndAt: liveEnd,
		PrivateReason: "Compensating for the interruption", IdempotencyKey: "sitting-live-extend",
	})
	if appErr != nil || extended.Sitting == nil || extended.Sitting.Revision != 5 || !extended.Sitting.ScheduledEndAt.Equal(liveEnd) {
		t.Fatalf("extend live Sitting = %#v, %v", extended, appErr)
	}
	closedEarly, appErr := helper.App.CloseExamSitting(ctx, managerInvocation, application.CloseExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 5,
		PrivateReason: "Authorized early close", IdempotencyKey: "sitting-live-close",
	})
	if appErr != nil || closedEarly.Sitting == nil || closedEarly.Sitting.State != model.ExamSittingClosing ||
		closedEarly.Sitting.Revision != 6 || closedEarly.Sitting.ReasonCode != model.ExamSittingReasonManagerClosed {
		t.Fatalf("close live Sitting = %#v, %v", closedEarly, appErr)
	}
	closedReplay, appErr := helper.App.CloseExamSitting(ctx, managerInvocation, application.CloseExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: live.Sitting.ID, ExpectedRevision: 5,
		PrivateReason: "Authorized early close", IdempotencyKey: "sitting-live-close",
	})
	if appErr != nil || closedReplay.Sitting == nil || closedReplay.Sitting.Revision != closedEarly.Sitting.Revision ||
		!closedReplay.Sitting.ClosingAt.Time.Equal(closedEarly.Sitting.ClosingAt.Time) {
		t.Fatalf("close live replay = %#v, %v", closedReplay, appErr)
	}

	if _, err := persistence.AcademicUnitMember().End(ctx, managerMembership.ID.String(), managerMembership.Revision, model.GetMillis()-100); err != nil {
		t.Fatal(err)
	}
	if _, appErr := helper.App.GetExamSitting(ctx, managerInvocation, application.GetExamSittingQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("get after membership revocation error = %v", appErr)
	}
	if appErr := helper.App.AuthorizeWebSocketSubscription(ctx, *managerPrincipal, model.RequestMetadata{},
		model.ActionExamSittingView, model.Resource{Type: model.ResourceExamSitting, ID: scheduled.Sitting.ID.String()}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("subscription after membership revocation error = %v", appErr)
	}
	if _, err := persistence.AcademicUnitMember().Save(ctx, &model.AcademicUnitMember{
		AcademicUnitID: unit.ID, UserID: manager.ID, StartsAt: model.TimeFromMillis(model.GetMillis() - 50),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().End(ctx, managerBinding.ID.String(), model.GetMillis()-25); err != nil {
		t.Fatal(err)
	}
	if _, appErr := helper.App.GetExamSitting(ctx, managerInvocation, application.GetExamSittingQuery{
		ExamID: created.Exam.ID, SittingID: scheduled.Sitting.ID,
	}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("get after permission revocation error = %v", appErr)
	}
	if appErr := helper.App.AuthorizeWebSocketSubscription(ctx, *managerPrincipal, model.RequestMetadata{},
		model.ActionExamSittingView, model.Resource{Type: model.ResourceExamSitting, ID: scheduled.Sitting.ID.String()}); !application.Is(appErr, "resource.not_found") {
		t.Fatalf("subscription after permission revocation error = %v", appErr)
	}

	overrideUser, appErr := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "sitting-override", Email: "sitting-override@example.edu", DisplayName: "Sitting Override",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	overrideLogin := loginIntegrationUser(t, helper.Handler(), overrideUser.Username, password, model.SessionClientCLI, "sitting-override-cli")
	overridePrincipal, appErr := helper.App.AuthenticateAccess(ctx, overrideLogin.Tokens.AccessToken)
	if appErr != nil {
		t.Fatal(appErr)
	}
	overrideInvocation := application.NewInvocation(*overridePrincipal, model.RequestMetadata{RequestID: "exam-sitting-override-integration"})
	overrideRole, err := persistence.Role().Save(ctx, &model.Role{
		Name: "exam-sitting-override", DisplayName: "Exam Sitting Override",
		Permissions: []string{
			string(model.ActionExamViewOverride), string(model.ActionExamSittingCreateOverride),
			string(model.ActionExamSittingViewOverride), string(model.ActionExamSittingManageOverride),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(ctx, &model.RoleBinding{
		UserID: overrideUser.ID, RoleID: overrideRole.ID, ScopeType: model.RoleScopeInstitution,
		ScopeID: institution.ID.String(), StartsAt: model.TimeFromMillis(model.GetMillis() - 1_000),
	}); err != nil {
		t.Fatal(err)
	}

	overrideSchedule := application.ScheduleExamSittingCommand{
		ExamID: created.Exam.ID, ExamRevisionID: published.ID, ClassID: class.ID,
		ScheduledStartAt: now.Add(6 * time.Hour).Truncate(time.Millisecond),
		ScheduledEndAt:   now.Add(8 * time.Hour).Truncate(time.Millisecond),
		IdempotencyKey:   "sitting-override-schedule",
	}
	overrideScheduled, appErr := helper.App.ScheduleExamSitting(ctx, overrideInvocation, overrideSchedule)
	if appErr != nil || overrideScheduled.Sitting == nil {
		t.Fatalf("override schedule = %#v, %v", overrideScheduled, appErr)
	}
	if _, appErr := helper.App.GetExamSitting(ctx, overrideInvocation, application.GetExamSittingQuery{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID,
	}); appErr != nil {
		t.Fatalf("override get error = %v", appErr)
	}
	overridePage, appErr := helper.App.ListExamSittings(ctx, overrideInvocation, application.ListExamSittingsQuery{
		ExamID: created.Exam.ID, Limit: 50,
	})
	if appErr != nil || len(overridePage.Items) != 3 {
		t.Fatalf("override list = %#v, %v", overridePage, appErr)
	}
	overrideEndAt := overrideSchedule.ScheduledEndAt.Add(30 * time.Minute)
	overrideUpdated, appErr := helper.App.UpdateExamSittingSchedule(ctx, overrideInvocation, application.UpdateExamSittingScheduleCommand{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID, ExpectedRevision: 1,
		ScheduledEndAt: &overrideEndAt, IdempotencyKey: "sitting-override-update",
	})
	if appErr != nil || overrideUpdated.Sitting == nil || overrideUpdated.Sitting.Revision != 2 {
		t.Fatalf("override update = %#v, %v", overrideUpdated, appErr)
	}
	if _, appErr := helper.App.CancelExamSitting(ctx, overrideInvocation, application.CancelExamSittingCommand{
		ExamID: created.Exam.ID, SittingID: overrideScheduled.Sitting.ID, ExpectedRevision: 2,
		PrivateReason: "Administrator canceled the delivery", IdempotencyKey: "sitting-override-cancel",
	}); appErr != nil {
		t.Fatalf("override cancel error = %v", appErr)
	}

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
		if err != nil {
			t.Fatal(err)
		}
		for _, delivery := range deliveries {
			if delivery.TargetUserID == target && delivery.State == model.MailDeliveryAccepted && len(delivery.EncryptedPayload) == 0 {
				if len(primary.Mailer.Deliveries())+len(secondary.Mailer.Deliveries()) == 0 {
					t.Fatal("accepted Sitting delivery was not observed by either server.New mail adapter")
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Sitting %s mail did not converge across two nodes: %#v; primary logs=%s; secondary logs=%s",
				templateKey, deliveries, primary.Logs.String(), secondary.Logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertExamSittingOverrideAudit(t *testing.T, ctx context.Context, persistence store.Store, actorID model.UserID,
	action model.Action, resource model.Resource, unitID model.AcademicUnitID,
) {
	t.Helper()
	events, err := persistence.Audit().List(ctx, store.AuditListOptions{
		ActorId: actorID.String(), Action: string(action), Resource: &resource, Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Status == model.AuditStatusSuccess && event.ScopeType == model.RoleScopeAcademicUnit && event.ScopeID == unitID.String() {
			return
		}
	}
	t.Fatalf("no successful %s audit for %s in Academic Unit %s: %#v", action, resource.ID, unitID, events)
}
