// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCreateOwnsAuthorizationAuditPersistenceAndEffects(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	got, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{
		AcademicUnitID: fixture.unitID, Title: "  Algorithms  ", InstructionsMarkdown: "Use **Go**.",
		IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.Exam.ID.IsZero() || got.Exam.CreatorUserID != fixture.userID || got.Exam.OwnerUserID != fixture.userID || got.Draft.Title != "Algorithms" || got.ManagerCount != 1 {
		t.Fatalf("view = %#v", got)
	}
	if fixture.authorizer.action != model.ActionExamCreate || fixture.authorizer.resource != (model.Resource{Type: model.ResourceAcademicUnit, ID: fixture.unitID.String()}) {
		t.Fatalf("authorization = %s %#v", fixture.authorizer.action, fixture.authorizer.resource)
	}
	wantOrder := []string{"membership", "authorize", "audit.begin", "store.create", "effect.created"}
	if !reflect.DeepEqual(*fixture.order, wantOrder) {
		t.Fatalf("order = %v, want %v", *fixture.order, wantOrder)
	}
	if fixture.persistence.creation.Exam.ID != fixture.persistence.creation.Draft.ExamID || fixture.persistence.creation.Manager.UserID != fixture.userID {
		t.Fatalf("atomic aggregate = %#v", fixture.persistence.creation)
	}
	if fixture.persistence.creation.Draft.Policy != model.DefaultExamPolicySet() {
		t.Fatalf("policy = %#v", fixture.persistence.creation.Draft.Policy)
	}
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationCreate, "test-key",
		fmt.Sprintf(`{"academic_unit_id":%q,"title":"  Algorithms  ","instructions_markdown":"Use **Go**."}`, fixture.unitID))
}

func TestCreateUsesExplicitOverrideWithoutMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamCreateOverride {
		t.Fatalf("action = %s, want override", fixture.authorizer.action)
	}
}

func TestCreateRequiresIdempotencyBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "idempotency.key_required" {
		t.Fatalf("error = %v, want idempotency.key_required", err)
	}
	if len(*fixture.order) != 0 {
		t.Fatalf("side effects before required idempotency validation: %v", *fixture.order)
	}
}

func TestCreateRejectsInvalidDraftBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "   ", IdempotencyKey: "test-key"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.invalid" {
		t.Fatalf("error = %v", err)
	}
	if len(*fixture.order) != 0 {
		t.Fatalf("invalid draft caused effects: %v", *fixture.order)
	}
}

func TestCreateReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	command := "test-key"
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: command})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.idempotency == nil || fixture.persistence.idempotency.Operation != "exam.create.v1" || fixture.effects.calls != 0 {
		t.Fatalf("idempotency/effects = %#v/%d", fixture.persistence.idempotency, fixture.effects.calls)
	}
}

func TestCreateFailureCompletesAuditAsFailed(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.persistence.err = store.NewErrConflict("exam", "exams_pkey", errors.New("duplicate"))
	_, err := fixture.service.Create(context.Background(), fixture.call, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: "test-key"})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.conflict" {
		t.Fatalf("error = %v", err)
	}
	if fixture.auditor.failedCode != "exam.conflict" {
		t.Fatalf("failed audit code = %q", fixture.auditor.failedCode)
	}
}

func TestEditDraftTextOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Distributed Systems  "
	instructions := "Use **Go** and submit all files."
	command := "test-key"
	view, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1,
		Title: &title, InstructionsMarkdown: &instructions, IdempotencyKey: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.Title != "Distributed Systems" || view.Draft.InstructionsMarkdown != instructions || view.Draft.Revision != 2 {
		t.Fatalf("view = %#v", view)
	}
	if fixture.authorizer.action != model.ActionExamManage || fixture.persistence.textUpdate == nil || fixture.persistence.idempotency == nil || fixture.persistence.idempotency.Operation != "exam.draft.text.edit.v1" {
		t.Fatalf("authorization/update = %s / %#v", fixture.authorizer.action, fixture.persistence.textUpdate)
	}
	if fixture.auditor.value["title"] != nil || fixture.auditor.value["instructions_markdown"] != nil ||
		fixture.auditor.scopeType != model.RoleScopeAcademicUnit || fixture.auditor.scopeID != fixture.unitID.String() || fixture.effects.updatedRevision != 2 {
		t.Fatalf("unsafe audit/scope/effect = %#v / %s:%s / %d", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID, fixture.effects.updatedRevision)
	}
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationEditDraftText, command,
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"title":"Distributed Systems","instructions_markdown":"Use **Go** and submit all files."}`, fixture.examID))
	want := []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}
	if !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestEditDraftTextNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Test  "
	_, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.draft.no_changes" {
		t.Fatalf("error = %v, want exam.draft.no_changes", err)
	}
	if want := []string{"store.access", "membership", "authorize", "store.get"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestEditDraftTextRejectsArchivedAndStaleDraftsWithoutPublishing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		archived         bool
		expectedRevision int64
		want             string
	}{
		{name: "archived", archived: true, expectedRevision: 1, want: "exam.archived"},
		{name: "stale", expectedRevision: 2, want: "exam.draft.revision_conflict"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.persistence.archived = test.archived
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			title := "Changed"
			_, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: test.expectedRevision, Title: &title,
				IdempotencyKey: "test-key",
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.want {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
			if fixture.auditor.value == nil || fixture.persistence.textUpdate == nil || fixture.effects.updatedRevision != 0 || fixture.auditor.failedCode != test.want {
				t.Fatalf("rejected update effects: audit=%#v store=%#v effect=%d failed=%s", fixture.auditor.value, fixture.persistence.textUpdate, fixture.effects.updatedRevision, fixture.auditor.failedCode)
			}
		})
	}
}

func TestEditDraftTextArchivedNoOpStillRejectsAsArchived(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.archived = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Test  "
	_, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.archived" {
		t.Fatalf("error = %v, want exam.archived", err)
	}
	if fixture.auditor.value == nil || fixture.persistence.textUpdate == nil || fixture.effects.updatedRevision != 0 || fixture.auditor.failedCode != "exam.archived" {
		t.Fatalf("archived no-op effects: audit=%#v store=%#v effect=%d failed=%s", fixture.auditor.value, fixture.persistence.textUpdate, fixture.effects.updatedRevision, fixture.auditor.failedCode)
	}
}

func TestEditDraftTextReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "Changed"
	if _, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.effects.updatedRevision != 0 {
		t.Fatalf("replay published revision %d", fixture.effects.updatedRevision)
	}
}

func TestEditDraftTextUsesExplicitOverrideWithoutManagerMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	title := "Changed"
	if _, err := fixture.service.EditDraftText(context.Background(), fixture.call, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.authorizer.action != model.ActionExamManageOverride || fixture.persistence.textUpdate == nil || !fixture.persistence.textUpdate.ManagerOverride {
		t.Fatalf("override action/update = %s / %#v", fixture.authorizer.action, fixture.persistence.textUpdate)
	}
	if want := []string{"store.access", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestConfigureDraftFocusLossOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	focus := model.FocusLossPolicy{Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 100, Window: 4 * time.Hour, Outcome: model.IntegrityOutcomeFlagAndSuspend}
	command := "test-key"
	view, err := fixture.service.ConfigureDraftFocusLoss(context.Background(), fixture.call, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, FocusLoss: focus, IdempotencyKey: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.Policy.FocusLoss != focus || view.Draft.Policy.ConnectionLoss.Outcome != model.IntegrityOutcomeFlagAndSuspend || view.Draft.Revision != 2 {
		t.Fatalf("view = %#v", view)
	}
	if fixture.authorizer.action != model.ActionExamManage || fixture.persistence.focusLossUpdate == nil || fixture.persistence.focusLossUpdate.FocusLoss != focus || fixture.persistence.idempotency == nil || fixture.persistence.idempotency.Operation != "exam.draft.focus_loss.configure.v1" {
		t.Fatalf("authorization/update = %s / %#v", fixture.authorizer.action, fixture.persistence.focusLossUpdate)
	}
	if len(fixture.auditor.value) != 3 || fixture.auditor.value["exam_id"] != fixture.examID.String() || fixture.auditor.value["expected_draft_revision"] != int64(1) || fixture.auditor.value["draft_revision"] != int64(2) || fixture.effects.updatedRevision != 2 {
		t.Fatalf("unsafe audit/effect = %#v / %d", fixture.auditor.value, fixture.effects.updatedRevision)
	}
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationConfigureDraftFocusLoss, command,
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":100,"window_milliseconds":14400000,"outcome":"flag_and_suspend"}`, fixture.examID))
	want := []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_focus_loss", "effect.updated"}
	if !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestConfigureDraftFocusLossNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	_, err := fixture.service.ConfigureDraftFocusLoss(context.Background(), fixture.call, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, FocusLoss: model.DefaultExamPolicySet().FocusLoss, IdempotencyKey: "test-key",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.draft.no_changes" {
		t.Fatalf("error = %v, want exam.draft.no_changes", err)
	}
	if want := []string{"store.access", "membership", "authorize", "store.get"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestConfigureDraftFocusLossArchivedNoOpAndStaleEditsReachAuditedStoreGuard(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		archived bool
		revision int64
		want     string
	}{
		{name: "archived no-op", archived: true, revision: 1, want: "exam.archived"},
		{name: "stale no-op", revision: 2, want: "exam.draft.revision_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = true
			fixture.persistence.archived = test.archived
			fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			_, err := fixture.service.ConfigureDraftFocusLoss(context.Background(), fixture.call, ConfigureDraftFocusLossCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: test.revision, FocusLoss: model.DefaultExamPolicySet().FocusLoss, IdempotencyKey: "test-key",
			})
			var fault *Fault
			if !errors.As(err, &fault) || fault.Code != test.want {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
			if fixture.persistence.focusLossUpdate == nil || fixture.auditor.failedCode != test.want || fixture.effects.updatedRevision != 0 {
				t.Fatalf("guard effects: update=%#v failed=%s effect=%d", fixture.persistence.focusLossUpdate, fixture.auditor.failedCode, fixture.effects.updatedRevision)
			}
		})
	}
}

func TestConfigureDraftFocusLossReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.archived = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	focus := model.FocusLossPolicy{Enabled: false, MinimumDuration: time.Second, IncidentCount: 2, Window: time.Minute, Outcome: model.IntegrityOutcomeFlag}
	if _, err := fixture.service.ConfigureDraftFocusLoss(context.Background(), fixture.call, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, FocusLoss: focus, IdempotencyKey: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.persistence.focusLossUpdate == nil || fixture.effects.updatedRevision != 0 {
		t.Fatalf("replay update/effect = %#v/%d", fixture.persistence.focusLossUpdate, fixture.effects.updatedRevision)
	}
}

func TestConfigureDraftExecutionProfileOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	profile := model.ExecutionProfile{Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkAllowlist}
	command := "test-key"
	view, err := fixture.service.ConfigureDraftExecutionProfile(context.Background(), fixture.call, ConfigureDraftExecutionProfileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Profile: profile, IdempotencyKey: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.ExecutionProfile != profile || view.Draft.Revision != 2 {
		t.Fatalf("view = %#v", view)
	}
	update := fixture.persistence.executionProfileUpdate
	if fixture.authorizer.action != model.ActionExamManage || update == nil || update.Profile != profile || fixture.persistence.idempotency == nil || fixture.persistence.idempotency.Operation != "exam.draft.execution_profile.configure.v1" {
		t.Fatalf("authorization/update = %s / %#v", fixture.authorizer.action, update)
	}
	if len(fixture.auditor.value) != 4 || fixture.auditor.value["exam_id"] != fixture.examID.String() || fixture.auditor.value["expected_draft_revision"] != int64(1) || fixture.auditor.value["draft_revision"] != int64(2) || fixture.auditor.value["execution_enabled"] != true || fixture.effects.updatedRevision != 2 {
		t.Fatalf("unsafe audit/effect = %#v / %d", fixture.auditor.value, fixture.effects.updatedRevision)
	}
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationConfigureExecutionProfile, command,
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"profile":{"Enabled":true,"Image":"golang-1.24","Network":"allowlist"}}`, fixture.examID))
	want := []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_execution_profile", "effect.updated"}
	if !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestConfigureDraftExecutionProfileNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	_, err := fixture.service.ConfigureDraftExecutionProfile(context.Background(), fixture.call, ConfigureDraftExecutionProfileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Profile: model.DefaultExecutionProfile(), IdempotencyKey: "test-key",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.draft.no_changes" {
		t.Fatalf("error = %v, want exam.draft.no_changes", err)
	}
	if want := []string{"store.access", "membership", "authorize", "store.get"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestConfigureDraftExecutionProfileRejectsUnsupportedFreshChoice(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.profiles.supported = false
	profile := model.ExecutionProfile{Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkNone}
	_, err := fixture.service.ConfigureDraftExecutionProfile(context.Background(), fixture.call, ConfigureDraftExecutionProfileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Profile: profile, IdempotencyKey: "test-key",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.invalid" {
		t.Fatalf("error = %v, want exam.invalid", err)
	}
	if fixture.outcomes.calls != 2 || fixture.profiles.calls != 1 || fixture.profiles.profile != profile ||
		fixture.persistence.executionProfileUpdate != nil || fixture.auditor.failedCode != "exam.invalid" {
		t.Fatalf("outcomes/catalog/update/audit = %d/%d/%#v/%q", fixture.outcomes.calls, fixture.profiles.calls,
			fixture.persistence.executionProfileUpdate, fixture.auditor.failedCode)
	}
}

func TestConfigureDraftExecutionProfileReplaysWhenCatalogBecomesUnavailable(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.outcomes.found = true
	fixture.profiles.err = errors.New("catalog offline")
	_, err := fixture.service.ConfigureDraftExecutionProfile(context.Background(), fixture.call, ConfigureDraftExecutionProfileCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1,
		Profile:        model.ExecutionProfile{Enabled: true, Image: "golang-1.24", Network: model.ExecutionNetworkNone},
		IdempotencyKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.outcomes.calls != 1 || fixture.profiles.calls != 0 || fixture.persistence.executionProfileUpdate == nil ||
		fixture.effects.updatedRevision != 0 || fixture.auditor.failedCode != "" {
		t.Fatalf("outcomes/catalog/update/effect/audit = %d/%d/%#v/%d/%q", fixture.outcomes.calls, fixture.profiles.calls,
			fixture.persistence.executionProfileUpdate, fixture.effects.updatedRevision, fixture.auditor.failedCode)
	}
}

func TestAuthorizeViewSelectsCurrentManagerOrExplicitOverride(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		manager   bool
		member    bool
		want      model.Action
		authorize error
	}{
		{name: "current manager", manager: true, member: true, want: model.ActionExamView},
		{name: "non-manager override", want: model.ActionExamViewOverride},
		{name: "revoked membership override", manager: true, want: model.ActionExamViewOverride},
		{name: "denied non-manager", want: model.ActionExamViewOverride, authorize: errors.New("denied")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = test.manager
			if test.member {
				fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			}
			fixture.authorizer.err = test.authorize
			err := fixture.service.AuthorizeView(context.Background(), fixture.call, fixture.examID)
			if !errors.Is(err, test.authorize) {
				t.Fatalf("error = %v, want %v", err, test.authorize)
			}
			if fixture.authorizer.action != test.want || fixture.authorizer.resource != (model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}) {
				t.Fatalf("authorization = %s %#v", fixture.authorizer.action, fixture.authorizer.resource)
			}
		})
	}
}

func TestGetSelectsManagerOrOverrideAuthorization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		manager bool
		action  model.Action
	}{{"manager", true, model.ActionExamView}, {"override", false, model.ActionExamViewOverride}} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = test.manager
			if test.manager {
				fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			}
			view, err := fixture.service.Get(context.Background(), fixture.call, fixture.examID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Exam.ID != fixture.examID || fixture.authorizer.action != test.action || fixture.authorizer.resource.Type != model.ResourceExam {
				t.Fatalf("view/auth = %#v / %s %#v", view, fixture.authorizer.action, fixture.authorizer.resource)
			}
			want := []string{"store.access", "authorize", "store.get"}
			if test.manager {
				want = []string{"store.access", "membership", "authorize", "store.get"}
			}
			if !reflect.DeepEqual(*fixture.order, want) {
				t.Fatalf("order = %v, want %v", *fixture.order, want)
			}
		})
	}
}

func TestGetRequiresOverrideWhenManagerMembershipWasRevoked(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	denied := errors.New("override denied")
	fixture.authorizer.err = denied
	if _, err := fixture.service.Get(context.Background(), fixture.call, fixture.examID); !errors.Is(err, denied) {
		t.Fatalf("error = %v, want override denial", err)
	}
	if fixture.authorizer.action != model.ActionExamViewOverride {
		t.Fatalf("action = %s, want override after membership revocation", fixture.authorizer.action)
	}
	if want := []string{"store.access", "membership", "authorize"}; !reflect.DeepEqual(*fixture.order, want) {
		t.Fatalf("order = %v, want %v", *fixture.order, want)
	}
}

func TestCallClonesCredentialScopes(t *testing.T) {
	t.Parallel()
	principal := testPrincipal(model.NewUserID())
	principal.CredentialType = model.CredentialPersonalAccessToken
	principal.SessionID = ""
	principal.CredentialScopes = []string{string(model.ActionExamCreate)}
	principal.AuthenticationStrength = ""
	principal.AuthenticatedAt = time.Time{}
	principal.ClientType = model.SessionClientCLI
	call := NewCall(principal, model.RequestMetadata{RequestID: "request"})
	principal.CredentialScopes[0] = string(model.ActionAuditView)
	got := call.Principal()
	if got.CredentialScopes[0] != string(model.ActionExamCreate) {
		t.Fatalf("call principal was aliased: %#v", got)
	}
}

type authoringFixture struct {
	service     *Authoring
	call        Call
	unitID      model.AcademicUnitID
	examID      model.ExamID
	userID      model.UserID
	order       *[]string
	authorizer  *authorizerFake
	memberships *membershipsFake
	users       *usersFake
	mail        *managerMailPreparerFake
	auditor     *auditorFake
	outcomes    *commandOutcomesFake
	profiles    *executionProfileCatalogFake
	persistence *authoringStoreFake
	effects     *effectsFake
}

func newAuthoringFixture(t *testing.T) authoringFixture {
	t.Helper()
	order := []string{}
	unitID, examID, userID := model.NewAcademicUnitID(), model.NewExamID(), model.NewUserID()
	authorizer := &authorizerFake{order: &order}
	memberships := &membershipsFake{order: &order}
	users := &usersFake{order: &order, user: activeTestUser(userID)}
	mail := &managerMailPreparerFake{order: &order}
	auditor := &auditorFake{order: &order}
	outcomes := &commandOutcomesFake{}
	profiles := &executionProfileCatalogFake{supported: true}
	persistence := &authoringStoreFake{order: &order, examID: examID, unitID: unitID, actorID: userID}
	effects := &effectsFake{order: &order}
	service, err := NewAuthoring(persistence, memberships, users, mail, authorizer, auditor, outcomes, profiles, effects, effects, func() time.Time {
		return time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
	}, func() model.ExamID { return examID })
	if err != nil {
		t.Fatal(err)
	}
	return authoringFixture{service: service, call: NewCall(testPrincipal(userID), model.RequestMetadata{}), unitID: unitID, examID: examID, userID: userID, order: &order, authorizer: authorizer, memberships: memberships, users: users, mail: mail, auditor: auditor, outcomes: outcomes, profiles: profiles, persistence: persistence, effects: effects}
}

type commandOutcomesFake struct {
	found bool
	err   error
	calls int
}

func (fake *commandOutcomesFake) Has(context.Context, *store.CommandIdempotency) (bool, error) {
	fake.calls++
	return fake.found, fake.err
}

type executionProfileCatalogFake struct {
	supported bool
	err       error
	calls     int
	profile   model.ExecutionProfile
}

func (fake *executionProfileCatalogFake) Supports(_ context.Context, profile model.ExecutionProfile) (bool, error) {
	fake.calls++
	fake.profile = profile
	return fake.supported, fake.err
}

func testPrincipal(userID model.UserID) model.Principal {
	now := time.Now().UTC()
	return model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: now}
}

type authorizerFake struct {
	order          *[]string
	action         model.Action
	resource       model.Resource
	err            error
	listVisibility store.ExamListVisibility
}

func (f *authorizerFake) AuthorizeList(_ context.Context, _ Call, _ model.AcademicUnitID) (store.ExamListVisibility, error) {
	*f.order = append(*f.order, "authorize.list")
	return f.listVisibility, f.err
}

func (f *authorizerFake) Authorize(_ context.Context, _ Call, action model.Action, resource model.Resource) error {
	*f.order = append(*f.order, "authorize")
	f.action, f.resource = action, resource
	return f.err
}

type membershipsFake struct {
	order       *[]string
	items       []*model.AcademicUnitMember
	itemsByUser map[string][]*model.AcademicUnitMember
	err         error
}

func (f *membershipsFake) ListActiveByUser(_ context.Context, userID string, _ int64) ([]*model.AcademicUnitMember, error) {
	*f.order = append(*f.order, "membership")
	if items, ok := f.itemsByUser[userID]; ok {
		return items, f.err
	}
	return f.items, f.err
}

type usersFake struct {
	order *[]string
	user  *model.User
	users map[string]*model.User
	err   error
}

func (f *usersFake) Get(_ context.Context, id string) (*model.User, error) {
	*f.order = append(*f.order, "user.get")
	if user, ok := f.users[id]; ok {
		return user, f.err
	}
	return f.user, f.err
}

type managerMailPreparerFake struct {
	order    *[]string
	requests []ManagerMailPreparation
	err      error
}

func (f *managerMailPreparerFake) PrepareManagerMail(request ManagerMailPreparation) (*store.ExamManagerMail, error) {
	*f.order = append(*f.order, "mail.prepare")
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	occurrence := &model.MailOccurrence{ID: request.OccurrenceID, Kind: model.MailOccurrenceExamManagement,
		TemplateKey: request.TemplateKey, ActorUserID: request.Recipient.ID, CreatedAt: request.ActionAt}
	delivery := &model.MailDelivery{TargetUserID: request.Recipient.ID, TemplateKey: request.TemplateKey}
	return &store.ExamManagerMail{Occurrence: occurrence, Delivery: delivery, Job: &model.Job{}}, nil
}

type auditorFake struct {
	order      *[]string
	failedCode string
	value      map[string]any
	scopeType  model.RoleScopeType
	scopeID    string
	err        error
}

func (f *auditorFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, scopeType model.RoleScopeType, scopeID, _ string, value map[string]any, _ map[string]any) (string, error) {
	*f.order = append(*f.order, "audit.begin")
	f.value = value
	f.scopeType, f.scopeID = scopeType, scopeID
	return model.NewId(), f.err
}
func (f *auditorFake) Fail(_ context.Context, _ string, code string) error {
	*f.order = append(*f.order, "audit.fail")
	f.failedCode = code
	return f.err
}

type authoringStoreFake struct {
	order                  *[]string
	examID                 model.ExamID
	unitID                 model.AcademicUnitID
	actorID                model.UserID
	actorIsManager         bool
	archived               bool
	replayed               bool
	creation               *store.ExamAuthoringCreation
	textUpdate             *store.ExamDraftTextUpdate
	focusLossUpdate        *store.ExamDraftFocusLossUpdate
	executionProfileUpdate *store.ExamDraftExecutionProfileUpdate
	archive                *store.ExamArchive
	listOptions            store.ExamListOptions
	summaries              []store.ExamSummary
	managerSummaries       []store.ExamManagerSummary
	managerListOptions     store.ExamManagerListOptions
	managerMutation        *store.ExamManagerMutation
	idempotency            *store.CommandIdempotency
	err                    error
	managerErr             error
}

func (f *authoringStoreFake) ListManagers(_ context.Context, options store.ExamManagerListOptions) ([]store.ExamManagerSummary, error) {
	f.managerListOptions = options
	return append([]store.ExamManagerSummary(nil), f.managerSummaries...), f.err
}
func (f *authoringStoreFake) AddManager(_ context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return f.managerResult(input, command, true, false)
}
func (f *authoringStoreFake) RemoveManager(_ context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return f.managerResult(input, command, false, false)
}
func (f *authoringStoreFake) TransferOwner(_ context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return f.managerResult(input, command, true, true)
}
func (f *authoringStoreFake) managerResult(input *store.ExamManagerMutation, command *store.CommandIdempotency, present, transfer bool) (*store.ExamManagerCommandResult, error) {
	f.managerMutation, f.idempotency = input, command
	if f.managerErr != nil {
		return nil, f.managerErr
	}
	if f.err != nil {
		return nil, f.err
	}
	exam, _ := model.NewExam(input.ExamID, f.unitID, f.actorID, model.TimeFromMillis(input.ChangedAt).Add(-time.Minute))
	if transfer {
		exam.OwnerUserID = input.TargetUserID
	}
	exam.Revision = input.ExpectedRevision + 1
	manager, _ := model.NewExamManager(input.ExamID, input.TargetUserID, input.ActorUserID, model.TimeFromMillis(input.ChangedAt))
	if !present && manager == nil {
		return nil, errors.New("manager result")
	}
	return &store.ExamManagerCommandResult{Exam: exam, Manager: manager, Replayed: f.replayed}, nil
}

func (f *authoringStoreFake) List(_ context.Context, options store.ExamListOptions) ([]store.ExamSummary, error) {
	*f.order = append(*f.order, "store.list")
	f.listOptions = options
	return append([]store.ExamSummary(nil), f.summaries...), f.err
}
func (f *authoringStoreFake) Archive(_ context.Context, input *store.ExamArchive, command *store.CommandIdempotency) (*store.ExamArchiveCommandResult, error) {
	*f.order = append(*f.order, "store.archive")
	f.archive, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	exam, _ := model.NewExam(input.ExamID, f.unitID, f.actorID, model.TimeFromMillis(input.ArchivedAt).Add(-time.Minute))
	if f.replayed {
		_ = exam.Archive(model.TimeFromMillis(input.ArchivedAt))
		return &store.ExamArchiveCommandResult{Value: exam, Replayed: true}, nil
	}
	if !f.actorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if f.archived {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if input.ExpectedRevision != 1 {
		return nil, store.NewErrConflict("exam", "exam_revision", nil)
	}
	_ = exam.Archive(model.TimeFromMillis(input.ArchivedAt))
	return &store.ExamArchiveCommandResult{Value: exam}, nil
}

func (f *authoringStoreFake) Create(_ context.Context, input *store.ExamAuthoringCreation, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.create")
	f.creation, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshotFromCreation(input, true), Replayed: f.replayed}, nil
}
func (f *authoringStoreFake) UpdateDraftText(_ context.Context, input *store.ExamDraftTextUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_text")
	f.textUpdate, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	snapshot, err := f.Get(context.Background(), input.ExamID, input.ActorUserID)
	*f.order = (*f.order)[:len(*f.order)-1]
	if err != nil {
		return nil, err
	}
	if f.replayed {
		return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: true}, nil
	}
	if !snapshot.ActorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if snapshot.Exam.IsArchived() {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if snapshot.Draft.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	if _, err := snapshot.Draft.ApplyTextPatch(input.Title, input.InstructionsMarkdown, model.TimeFromMillis(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: f.replayed}, nil
}
func (f *authoringStoreFake) UpdateDraftFocusLoss(_ context.Context, input *store.ExamDraftFocusLossUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_focus_loss")
	f.focusLossUpdate, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	snapshot, err := f.Get(context.Background(), input.ExamID, input.ActorUserID)
	*f.order = (*f.order)[:len(*f.order)-1]
	if err != nil {
		return nil, err
	}
	if f.replayed {
		return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: true}, nil
	}
	if !snapshot.ActorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if snapshot.Exam.IsArchived() {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if snapshot.Draft.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	if _, err := snapshot.Draft.ApplyFocusLossPolicy(input.FocusLoss, model.TimeFromMillis(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot}, nil
}
func (f *authoringStoreFake) UpdateDraftExecutionProfile(_ context.Context, input *store.ExamDraftExecutionProfileUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_execution_profile")
	f.executionProfileUpdate, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	snapshot, err := f.Get(context.Background(), input.ExamID, input.ActorUserID)
	*f.order = (*f.order)[:len(*f.order)-1]
	if err != nil {
		return nil, err
	}
	if f.replayed {
		return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: true}, nil
	}
	if !snapshot.ActorIsManager && !input.ManagerOverride {
		return nil, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if snapshot.Exam.IsArchived() {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if snapshot.Draft.Revision != input.ExpectedRevision {
		return nil, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	if _, err := snapshot.Draft.ApplyExecutionProfile(input.Profile, model.TimeFromMillis(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot}, nil
}
func (f *authoringStoreFake) UpdateDraftBrowserPolicy(_ context.Context, input *store.ExamDraftBrowserPolicyUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_browser_policy")
	f.idempotency = command
	if f.err != nil {
		return nil, f.err
	}
	snapshot, err := f.Get(context.Background(), input.ExamID, input.ActorUserID)
	*f.order = (*f.order)[:len(*f.order)-1]
	if err != nil {
		return nil, err
	}
	if f.replayed {
		return &store.ExamAuthoringCommandResult{Value: snapshot, Replayed: true}, nil
	}
	if _, err = snapshot.Draft.ApplyBrowserPolicy(input.Policy, model.TimeFromMillis(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot}, nil
}
func (f *authoringStoreFake) Access(_ context.Context, examID model.ExamID, _ model.UserID) (*store.ExamAccessSnapshot, error) {
	*f.order = append(*f.order, "store.access")
	if f.err != nil {
		return nil, f.err
	}
	exam, _ := model.NewExam(examID, f.unitID, f.actorID, time.Now().UTC())
	if f.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(time.Now().UTC())
	}
	return &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: f.actorIsManager}, nil
}
func (f *authoringStoreFake) Get(_ context.Context, examID model.ExamID, _ model.UserID) (*store.ExamAuthoringSnapshot, error) {
	*f.order = append(*f.order, "store.get")
	if f.err != nil {
		return nil, f.err
	}
	at := time.Now().UTC()
	exam, _ := model.NewExam(examID, f.unitID, f.actorID, at)
	if f.archived {
		exam.ArchivedAt = model.OptionalTimeFrom(at)
	}
	draft, _ := model.NewExamDraft(examID, "Test", "", model.DefaultExamPolicySet(), at)
	return &store.ExamAuthoringSnapshot{Exam: exam, Draft: draft, OwnerUserID: f.actorID, ManagerCount: 1, ActorIsManager: f.actorIsManager}, nil
}
func (f *authoringStoreFake) Resolve(context.Context, model.ExamID) (*model.Exam, error) {
	return nil, nil
}
func snapshotFromCreation(input *store.ExamAuthoringCreation, actor bool) *store.ExamAuthoringSnapshot {
	return &store.ExamAuthoringSnapshot{Exam: input.Exam, Draft: input.Draft, OwnerUserID: input.Exam.OwnerUserID, ManagerCount: 1, ActorIsManager: actor}
}

type effectsFake struct {
	order            *[]string
	calls            int
	updatedRevision  int64
	archivedRevision int64
	err              error
}

func (f *effectsFake) Archived(_ context.Context, _ model.ExamID, revision int64, _ time.Time) error {
	*f.order = append(*f.order, "effect.archived")
	f.archivedRevision = revision
	return f.err
}
func (f *effectsFake) ManagerChanged(context.Context, model.ExamID, model.UserID, bool, int64, time.Time) error {
	return f.err
}
func (f *effectsFake) OwnerTransferred(context.Context, model.ExamID, model.UserID, int64, time.Time) error {
	return f.err
}

func (f *effectsFake) Created(context.Context, model.ExamID) error {
	*f.order = append(*f.order, "effect.created")
	f.calls++
	return f.err
}
func (f *effectsFake) DraftUpdated(_ context.Context, _ model.ExamID, revision int64) error {
	*f.order = append(*f.order, "effect.updated")
	f.updatedRevision = revision
	return f.err
}
func (f *effectsFake) Report(context.Context, string, error) {}
