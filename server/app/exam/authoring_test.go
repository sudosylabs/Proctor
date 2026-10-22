// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCreateOwnsAuthorizationAuditPersistenceAndEffects(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	got, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{
		AcademicUnitID: fixture.unitID, Title: "  Algorithms  ", InstructionsMarkdown: "Use **Go**.",
		IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.False(t, got.Exam.ID.IsZero())
	require.Equal(t, fixture.userID, got.Exam.CreatorUserID)
	require.Equal(t, fixture.userID, got.Exam.OwnerUserID)
	require.Equal(t, "Algorithms", got.Draft.Title)
	require.Equal(t, 1, got.ManagerCount)
	require.Equal(t, model.ActionExamCreate, fixture.authorizer.action, "authorization")
	require.Equal(t, model.Resource{Type: model.ResourceAcademicUnit, ID: fixture.unitID.String()}, fixture.authorizer.resource, "authorization")
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"membership", "authorize", "audit.begin", "store.create", "effect.created"}, *fixture.order)
	require.Equal(t, fixture.persistence.creation.Draft.ExamID, fixture.persistence.creation.Exam.ID)
	require.Equal(t, fixture.userID, fixture.persistence.creation.Manager.UserID)
	require.True(t, fixture.persistence.creation.Draft.Policy.Equal(model.DefaultExamPolicySet()))
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationCreate, "test-key",
		fmt.Sprintf(`{"academic_unit_id":%q,"title":"  Algorithms  ","instructions_markdown":"Use **Go**."}`, fixture.unitID))
}

func TestCreateUsesExplicitOverrideWithoutMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamCreateOverride, fixture.authorizer.action, "action")
}

func TestCreateRequiresIdempotencyBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "idempotency.key_required", fault.Code())
	require.Empty(t, *fixture.order)
}

func TestCreateRejectsInvalidDraftBeforeResolutionOrAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "   ", IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.invalid", fault.Code())
	require.Empty(t, *fixture.order)
}

func TestCreateReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	command := "test-key"
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: command})
	require.NoError(t, err)
	require.NotNil(t, fixture.persistence.idempotency, "idempotency/effects")
	require.Equal(t, "exam.create", fixture.persistence.idempotency.Operation, "idempotency/effects")
	require.Equal(t, 0, fixture.effects.calls, "idempotency/effects")
}

func TestCreateFailureCompletesAuditAsFailed(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.persistence.err = store.NewErrConflict("exam", "exams_pkey", errors.New("duplicate"))
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.conflict", fault.Code())
	require.Equal(t, "exam.conflict", fixture.auditor.failedCode)
}

func TestCreateFailedAuditCompletionPreservesStoreFailure(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	fixture.persistence.err = store.NewErrConflict("exam", "exams_pkey", errors.New("duplicate"))
	auditErr := errors.New("audit completion unavailable")
	fixture.auditor.failErr = auditErr
	_, err := fixture.service.CreateExam(context.Background(), fixture.invocation, CreateCommand{AcademicUnitID: fixture.unitID, Title: "Networks", IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.conflict", fault.Code())
	require.ErrorIs(t, err, auditErr)
}

func TestEditDraftTextOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Distributed Systems  "
	instructions := "Use **Go** and submit all files."
	command := "test-key"
	view, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1,
		Title: &title, InstructionsMarkdown: &instructions, IdempotencyKey: command,
	})
	require.NoError(t, err)
	require.Equal(t, "Distributed Systems", view.Draft.Title)
	require.Equal(t, instructions, view.Draft.InstructionsMarkdown)
	require.Equal(t, int64(2), view.Draft.Revision)
	require.Equal(t, model.ActionExamManage, fixture.authorizer.action, "authorization/update")
	require.NotNil(t, fixture.persistence.textUpdate, "authorization/update")
	require.NotNil(t, fixture.persistence.idempotency, "authorization/update")
	require.Equal(t, "exam.draft.text.edit", fixture.persistence.idempotency.Operation, "authorization/update")
	require.Nil(t, fixture.auditor.value["title"], "unsafe audit/scope/effect = %#v / %s:%s", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID)
	require.Nil(t, fixture.auditor.value["instructions_markdown"], "unsafe audit/scope/effect = %#v / %s:%s", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID)
	require.Equal(t, model.RoleScopeAcademicUnit, fixture.auditor.scopeType, "unsafe audit/scope/effect = %#v / %s:%s", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID)
	require.Equal(t, fixture.unitID.String(), fixture.auditor.scopeID, "unsafe audit/scope/effect = %#v / %s:%s", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID)
	require.Equal(t, int64(2), fixture.effects.updatedRevision, "unsafe audit/scope/effect = %#v / %s:%s", fixture.auditor.value, fixture.auditor.scopeType, fixture.auditor.scopeID)
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationEditDraftText, command,
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"title":"Distributed Systems","instructions_markdown":"Use **Go** and submit all files."}`, fixture.examID))
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}, *fixture.order)
}

func TestEditDraftTextNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "  Test  "
	_, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.draft.no_changes", fault.Code())
	// A no-op edit is authorized but never audited, persisted, or published.
	require.Nil(t, fixture.auditor.value, "no-op edit was audited")
	require.Nil(t, fixture.persistence.textUpdate, "no-op edit was persisted")
	require.Zero(t, fixture.effects.updatedRevision, "no-op edit was published")
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
			_, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: test.expectedRevision, Title: &title,
				IdempotencyKey: "test-key",
			})
			var fault *apperror.Error
			require.ErrorAs(t, err, &fault)
			require.Equal(t, test.want, fault.Code())
			require.NotNil(t, fixture.auditor.value, "rejected update effects: audit")
			require.NotNil(t, fixture.persistence.textUpdate, "rejected update effects: audit")
			require.Equal(t, int64(0), fixture.effects.updatedRevision, "rejected update effects: audit")
			require.Equal(t, test.want, fixture.auditor.failedCode, "rejected update effects: audit")
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
	_, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.archived", fault.Code())
	require.NotNil(t, fixture.auditor.value, "archived no-op effects: audit")
	require.NotNil(t, fixture.persistence.textUpdate, "archived no-op effects: audit")
	require.Equal(t, int64(0), fixture.effects.updatedRevision, "archived no-op effects: audit")
	require.Equal(t, "exam.archived", fixture.auditor.failedCode, "archived no-op effects: audit")
}

func TestEditDraftTextReplayDoesNotRepublish(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.persistence.replayed = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	title := "Changed"
	_, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), fixture.effects.updatedRevision, "replay published revision")
}

func TestEditDraftTextUsesExplicitOverrideWithoutManagerMembership(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	title := "Changed"
	_, err := fixture.service.EditExamDraftText(context.Background(), fixture.invocation, EditDraftTextCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Title: &title,
		IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamManageOverride, fixture.authorizer.action, "override action/update")
	require.NotNil(t, fixture.persistence.textUpdate, "override action/update")
	require.True(t, fixture.persistence.textUpdate.ManagerOverride, "override action/update")
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect. No membership is consulted for an override.
	require.Equal(t, []string{"store.access", "authorize", "store.get", "audit.begin", "store.update_text", "effect.updated"}, *fixture.order)
}

func TestConfigureDraftFocusLossOwnsAuthorizationAuditPersistenceAndSafeEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	focus := model.FocusLossPolicy{Enabled: false, MinimumDuration: 500 * time.Millisecond, IncidentCount: 100, Window: 4 * time.Hour, Outcome: model.IntegrityOutcomeFlagAndSuspend}
	command := "test-key"
	view, err := fixture.service.ConfigureExamDraftFocusLoss(context.Background(), fixture.invocation, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Enabled: focus.Enabled, MinimumDuration: focus.MinimumDuration, IncidentCount: focus.IncidentCount, Window: focus.Window, Outcome: focus.Outcome, IdempotencyKey: command,
	})
	require.NoError(t, err)
	require.Equal(t, focus, view.Draft.Policy.FocusLoss)
	require.Equal(t, model.IntegrityOutcomeFlagAndSuspend, view.Draft.Policy.ConnectionLoss.Outcome)
	require.Equal(t, int64(2), view.Draft.Revision)
	require.Equal(t, model.ActionExamManage, fixture.authorizer.action, "authorization/update")
	require.NotNil(t, fixture.persistence.focusLossUpdate, "authorization/update")
	require.Equal(t, focus, fixture.persistence.focusLossUpdate.FocusLoss, "authorization/update")
	require.NotNil(t, fixture.persistence.idempotency, "authorization/update")
	require.Equal(t, "exam.draft.focus_loss.configure", fixture.persistence.idempotency.Operation, "authorization/update")
	require.Len(t, fixture.auditor.value, 3, "unsafe audit/effect")
	require.Equal(t, fixture.examID.String(), fixture.auditor.value["exam_id"], "unsafe audit/effect")
	require.Equal(t, int64(1), fixture.auditor.value["expected_draft_revision"], "unsafe audit/effect")
	require.Equal(t, int64(2), fixture.auditor.value["draft_revision"], "unsafe audit/effect")
	require.Equal(t, int64(2), fixture.effects.updatedRevision, "unsafe audit/effect")
	assertStoreIdempotency(t, fixture.persistence.idempotency, fixture.userID, idempotencyOperationConfigureDraftFocusLoss, command,
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":1,"enabled":false,"minimum_duration_milliseconds":500,"incident_count":100,"window_milliseconds":14400000,"outcome":"flag_and_suspend"}`, fixture.examID))
	// Contract (guardrails 8 and 14): authorization precedes the audit, the audit
	// begins before the durable write, and the write commits before its effect.
	require.Equal(t, []string{"store.access", "membership", "authorize", "store.get", "audit.begin", "store.update_focus_loss", "effect.updated"}, *fixture.order)
}

func TestConfigureDraftFocusLossNoChangeSkipsAuditPersistenceAndEffect(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
	_, err := fixture.service.ConfigureExamDraftFocusLoss(context.Background(), fixture.invocation, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Enabled: model.DefaultExamPolicySet().FocusLoss.Enabled, MinimumDuration: model.DefaultExamPolicySet().FocusLoss.MinimumDuration, IncidentCount: model.DefaultExamPolicySet().FocusLoss.IncidentCount, Window: model.DefaultExamPolicySet().FocusLoss.Window, Outcome: model.DefaultExamPolicySet().FocusLoss.Outcome, IdempotencyKey: "test-key",
	})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.draft.no_changes", fault.Code())
	// A no-op edit is authorized but never audited, persisted, or published.
	require.Nil(t, fixture.auditor.value, "no-op edit was audited")
	require.Nil(t, fixture.persistence.focusLossUpdate, "no-op edit was persisted")
	require.Zero(t, fixture.effects.updatedRevision, "no-op edit was published")
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
			_, err := fixture.service.ConfigureExamDraftFocusLoss(context.Background(), fixture.invocation, ConfigureDraftFocusLossCommand{
				ExamID: fixture.examID, ExpectedDraftRevision: test.revision, Enabled: model.DefaultExamPolicySet().FocusLoss.Enabled, MinimumDuration: model.DefaultExamPolicySet().FocusLoss.MinimumDuration, IncidentCount: model.DefaultExamPolicySet().FocusLoss.IncidentCount, Window: model.DefaultExamPolicySet().FocusLoss.Window, Outcome: model.DefaultExamPolicySet().FocusLoss.Outcome, IdempotencyKey: "test-key",
			})
			var fault *apperror.Error
			require.ErrorAs(t, err, &fault)
			require.Equal(t, test.want, fault.Code())
			require.NotNil(t, fixture.persistence.focusLossUpdate, "guard effects: update")
			require.Equal(t, test.want, fixture.auditor.failedCode, "guard effects: update")
			require.Equal(t, int64(0), fixture.effects.updatedRevision, "guard effects: update")
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
	_, err := fixture.service.ConfigureExamDraftFocusLoss(context.Background(), fixture.invocation, ConfigureDraftFocusLossCommand{
		ExamID: fixture.examID, ExpectedDraftRevision: 1, Enabled: focus.Enabled, MinimumDuration: focus.MinimumDuration, IncidentCount: focus.IncidentCount, Window: focus.Window, Outcome: focus.Outcome, IdempotencyKey: "test-key",
	})
	require.NoError(t, err)
	require.NotNil(t, fixture.persistence.focusLossUpdate, "replay update/effect")
	require.Equal(t, int64(0), fixture.effects.updatedRevision, "replay update/effect")
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
			err := fixture.service.AuthorizeExamView(context.Background(), fixture.invocation, fixture.examID)
			require.ErrorIs(t, err, test.authorize)
			require.Equal(t, test.want, fixture.authorizer.action, "authorization")
			require.Equal(t, model.Resource{Type: model.ResourceExam, ID: fixture.examID.String()}, fixture.authorizer.resource, "authorization")
		})
	}
}

func TestGetSelectsManagerOrOverrideAuthorization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		manager bool
		action  model.Action
		order   []string
	}{
		{"manager", true, model.ActionExamView, []string{"store.access", "membership", "authorize", "store.get"}},
		{"override", false, model.ActionExamViewOverride, []string{"store.access", "authorize", "store.get"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newAuthoringFixture(t)
			fixture.persistence.actorIsManager = test.manager
			if test.manager {
				fixture.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: fixture.unitID, UserID: fixture.userID}}
			}
			view, err := fixture.service.GetExam(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID})
			require.NoError(t, err)
			require.Equal(t, fixture.examID, view.Exam.ID, "view/auth = %#v / %s %#v", view, fixture.authorizer.action, fixture.authorizer.resource)
			require.Equal(t, test.action, fixture.authorizer.action, "view/auth = %#v / %s %#v", view, fixture.authorizer.action, fixture.authorizer.resource)
			require.Equal(t, model.ResourceExam, fixture.authorizer.resource.Type, "view/auth = %#v / %s %#v", view, fixture.authorizer.action, fixture.authorizer.resource)
			// Contract: authorization precedes reading the Exam. Only a Manager's
			// current membership is consulted.
			require.Equal(t, test.order, *fixture.order)
		})
	}
}

func TestGetRequiresOverrideWhenManagerMembershipWasRevoked(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	denied := errors.New("override denied")
	fixture.authorizer.err = denied
	_, err := fixture.service.GetExam(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID})
	require.ErrorIs(t, err, denied)
	require.Equal(t, model.ActionExamViewOverride, fixture.authorizer.action, "action")
	require.Equal(t, []string{"store.access", "membership", "authorize"}, *fixture.order)
}

func TestGetStopsWhenCurrentManagerMembershipCannotBeRead(t *testing.T) {
	t.Parallel()
	fixture := newAuthoringFixture(t)
	fixture.persistence.actorIsManager = true
	failure := errors.New("membership unavailable")
	fixture.memberships.err = failure
	_, err := fixture.service.GetExam(context.Background(), fixture.invocation, GetQuery{ExamID: fixture.examID})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.unavailable", fault.Code())
	require.ErrorIs(t, err, failure)
	require.Empty(t, fixture.authorizer.action, "authorized without current membership")
	require.Equal(t, []string{"store.access", "membership"}, *fixture.order)
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
	invocation := invocation.New(principal, model.RequestMetadata{RequestID: "request"})
	principal.CredentialScopes[0] = string(model.ActionAuditView)
	got := invocation.Principal()
	require.Equal(t, string(model.ActionExamCreate), got.CredentialScopes[0])
}

type authoringFixture struct {
	service     *Authoring
	invocation  invocation.Invocation
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
	persistence := &authoringStoreFake{order: &order, examID: examID, unitID: unitID, actorID: userID}
	effects := &effectsFake{order: &order}
	service, err := NewAuthoring(AuthoringDependencies{
		Persistence:    persistence,
		Memberships:    memberships,
		Users:          users,
		Mail:           mail,
		Authorizer:     authorizer,
		Auditor:        auditor,
		Outcomes:       outcomes,
		Effects:        effects,
		EffectFailures: effects,
		Now: func() time.Time {
			return time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC)
		},
		NewID:             func() model.ExamID { return examID },
		InstitutionOrigin: "https://institution.example",
	})
	require.NoError(t, err)
	return authoringFixture{service: service, invocation: invocation.New(testPrincipal(userID), model.RequestMetadata{}), unitID: unitID, examID: examID, userID: userID, order: &order, authorizer: authorizer, memberships: memberships, users: users, mail: mail, auditor: auditor, outcomes: outcomes, persistence: persistence, effects: effects}
}

type commandOutcomesFake struct {
	found bool
	err   error
	calls int
}

func (f *commandOutcomesFake) Has(context.Context, *store.CommandIdempotency) (bool, error) {
	f.calls++
	return f.found, f.err
}

func testPrincipal(userID model.UserID) model.Principal {
	now := time.Now().UTC()
	return model.Principal{UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: now}
}

type authorizerFake struct {
	order          *[]string
	action         model.Action
	resource       model.Resource
	err            error
	listVisibility store.ExamListVisibility
}

func (f *authorizerFake) AuthorizeList(_ context.Context, _ invocation.Invocation, _ model.AcademicUnitID) (store.ExamListVisibility, error) {
	*f.order = append(*f.order, "authorize.list")
	return f.listVisibility, f.err
}

func (f *authorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, resource model.Resource) error {
	*f.order = append(*f.order, "authorize")
	f.action, f.resource = action, resource
	return f.err
}

type membershipsFake struct {
	order       *[]string
	items       []*model.AcademicUnitMember
	itemsByUser map[string][]*model.AcademicUnitMember
	lookup      func(string, time.Time) ([]*model.AcademicUnitMember, error)
	err         error
}

func (f *membershipsFake) ListActiveByUser(_ context.Context, userID model.UserID, at time.Time) ([]*model.AcademicUnitMember, error) {
	*f.order = append(*f.order, "membership")
	if f.lookup != nil {
		return f.lookup(userID.String(), at)
	}
	if items, ok := f.itemsByUser[userID.String()]; ok {
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

func (f *usersFake) Get(_ context.Context, id model.UserID) (*model.User, error) {
	*f.order = append(*f.order, "user.get")
	if user, ok := f.users[id.String()]; ok {
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
	failErr    error
}

func (f *auditorFake) BeginAtScope(_ context.Context, _ invocation.Invocation, _ model.Action, _ model.Resource, scopeType model.RoleScopeType, scopeID, _ string, value, _ map[string]any) (model.AuditEventID, error) {
	*f.order = append(*f.order, "audit.begin")
	f.value = value
	f.scopeType, f.scopeID = scopeType, scopeID
	return model.NewAuditEventID(), f.err
}
func (f *auditorFake) Fail(_ context.Context, _ model.AuditEventID, code string) error {
	*f.order = append(*f.order, "audit.fail")
	f.failedCode = code
	if f.failErr != nil {
		return f.failErr
	}
	return f.err
}

type authoringStoreFake struct {
	order              *[]string
	examID             model.ExamID
	unitID             model.AcademicUnitID
	actorID            model.UserID
	actorIsManager     bool
	archived           bool
	replayed           bool
	creation           *store.ExamAuthoringCreation
	textUpdate         *store.ExamDraftTextUpdate
	focusLossUpdate    *store.ExamDraftFocusLossUpdate
	nativePolicyUpdate *store.ExamDraftNativePolicyUpdate
	archive            *store.ExamArchive
	listOptions        store.ExamListOptions
	summaries          []store.ExamSummary
	managerSummaries   []store.ExamManagerSummary
	managerListOptions store.ExamManagerListOptions
	managerMutation    *store.ExamManagerMutation
	idempotency        *store.CommandIdempotency
	err                error
	managerErr         error
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
	exam, _ := model.NewExam(input.ExamID, f.unitID, f.actorID, model.TimeUTC(input.ChangedAt).Add(-time.Minute))
	if transfer {
		exam.OwnerUserID = input.TargetUserID
	}
	exam.Revision = input.ExpectedRevision + 1
	manager, _ := model.NewExamManager(input.ExamID, input.TargetUserID, input.ActorUserID, model.TimeUTC(input.ChangedAt))
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
	exam, _ := model.NewExam(input.ExamID, f.unitID, f.actorID, model.TimeUTC(input.ArchivedAt).Add(-time.Minute))
	if f.replayed {
		_ = exam.Archive(model.TimeUTC(input.ArchivedAt))
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
	_ = exam.Archive(model.TimeUTC(input.ArchivedAt))
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
	if _, err := snapshot.Draft.ApplyTextPatch(input.Title, input.InstructionsMarkdown, model.TimeUTC(input.UpdatedAt)); err != nil {
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
	if _, err := snapshot.Draft.ApplyFocusLossPolicy(input.FocusLoss, model.TimeUTC(input.UpdatedAt)); err != nil {
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
	if _, err = snapshot.Draft.ApplyBrowserPolicy(input.Policy, model.TimeUTC(input.UpdatedAt)); err != nil {
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

func (f *authoringStoreFake) UpdateDraftNativePolicy(_ context.Context, input *store.ExamDraftNativePolicyUpdate, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	*f.order = append(*f.order, "store.update_native_policy")
	f.nativePolicyUpdate, f.idempotency = input, command
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
	if _, err := snapshot.Draft.ApplyNativePolicy(input.NativePolicy, model.TimeUTC(input.UpdatedAt)); err != nil {
		return nil, err
	}
	return &store.ExamAuthoringCommandResult{Value: snapshot}, nil
}
