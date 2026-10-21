// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorizationRegistryIsClosedAndResourceTyped(t *testing.T) {
	definition, ok := DefinitionForAction(ActionClassMembersView)
	require.True(t, ok, "class-members definition = %#v", definition)
	require.Equal(t, ResourceClass, definition.ResourceType, "class-members definition = %#v", definition)
	require.True(t, definition.InheritInstitutionScope, "class-members definition = %#v", definition)
	require.True(t, definition.InheritAcademicUnitScopes, "class-members definition = %#v", definition)
	_, ok = DefinitionForAction(Action("future.permission"))
	require.False(t, ok, "unknown action was registered")
	require.NoError(t, (Resource{Type: ResourceClass, ID: NewID()}).Validate(), "valid class resource was rejected")
	userDefinition, ok := DefinitionForAction(ActionUserView)
	require.True(t, ok, "user-view definition = %#v", userDefinition)
	require.Equal(t, ResourceUser, userDefinition.ResourceType, "user-view definition = %#v", userDefinition)
	require.True(t, userDefinition.InheritInstitutionScope, "user-view definition = %#v", userDefinition)
	require.True(t, userDefinition.InheritAcademicUnitScopes, "user-view definition = %#v", userDefinition)
	require.NoError(t, (Resource{Type: ResourceUser, ID: NewID()}).Validate(), "valid user resource was rejected")
	examDefinition, ok := DefinitionForAction(ActionExamView)
	require.True(t, ok, "exam-view definition = %#v", examDefinition)
	require.Equal(t, ResourceExam, examDefinition.ResourceType, "exam-view definition = %#v", examDefinition)
	require.True(t, examDefinition.InheritInstitutionScope, "exam-view definition = %#v", examDefinition)
	require.True(t, examDefinition.InheritAcademicUnitScopes, "exam-view definition = %#v", examDefinition)
	require.NoError(t, (Resource{Type: ResourceExam, ID: NewID()}).Validate())
	sittingView, ok := DefinitionForAction(ActionExamSittingView)
	require.True(t, ok, "exam-sitting view definition = %#v", sittingView)
	require.Equal(t, ResourceExamSitting, sittingView.ResourceType, "exam-sitting view definition = %#v", sittingView)
	require.True(t, sittingView.InheritInstitutionScope, "exam-sitting view definition = %#v", sittingView)
	require.True(t, sittingView.InheritAcademicUnitScopes, "exam-sitting view definition = %#v", sittingView)
	sittingCreate, ok := DefinitionForAction(ActionExamSittingCreate)
	require.True(t, ok, "exam-sitting create definition = %#v", sittingCreate)
	require.Equal(t, ResourceExam, sittingCreate.ResourceType, "exam-sitting create definition = %#v", sittingCreate)
	require.True(t, sittingCreate.InheritInstitutionScope, "exam-sitting create definition = %#v", sittingCreate)
	require.True(t, sittingCreate.InheritAcademicUnitScopes, "exam-sitting create definition = %#v", sittingCreate)
	require.NoError(t, (Resource{Type: ResourceExamSitting, ID: NewID()}).Validate())
	require.Error(t, (Resource{Type: ResourceType("future"), ID: NewID()}).Validate(), "unimplemented resource type was accepted")
}

func TestActionDefinitionDistinguishesConcreteResourcesFromRoleScopes(t *testing.T) {
	t.Parallel()

	examManage, ok := DefinitionForAction(ActionExamManage)
	require.True(t, ok, "exam.manage is not registered")
	require.False(t, examManage.AcceptsResource(ResourceAcademicUnit), "exam.manage accepted an Academic Unit as its concrete resource")
	require.True(t, examManage.SupportsRoleScope(RoleScopeInstitution), "exam.manage Role scopes = institution")
	require.True(t, examManage.SupportsRoleScope(RoleScopeAcademicUnit), "exam.manage Role scopes = institution")
	require.False(t, examManage.SupportsRoleScope(RoleScopeClass), "exam.manage Role scopes = institution")
	require.False(t, examManage.SupportsRoleScope(RoleScopeType("future")), "exam.manage accepted an unknown Role scope")
}

func TestJobActionsAreInstitutionScopedAndKnown(t *testing.T) {
	for _, action := range []Action{ActionJobView, ActionJobManage} {
		definition, ok := DefinitionForAction(action)
		require.True(t, ok, "job action %q definition = %#v", action, definition)
		require.Equal(t, ResourceInstitution, definition.ResourceType, "job action %q definition = %#v", action, definition)
		require.True(t, definition.InheritInstitutionScope, "job action %q definition = %#v", action, definition)
		require.False(t, definition.InheritAcademicUnitScopes, "job action %q definition = %#v", action, definition)
		require.True(t, IsKnownAction(string(action)), "job action %q is not known", action)
	}
}

func TestGranularAcademicAndOnboardingActionsAreClosedAndResourceTyped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action       Action
		resourceType ResourceType
		patAllowed   bool
	}{
		{ActionAcademicUnitView, ResourceAcademicUnit, true},
		{ActionAcademicUnitManage, ResourceAcademicUnit, true},
		{ActionAcademicUnitMembersView, ResourceAcademicUnit, true},
		{ActionAcademicUnitMembersManage, ResourceAcademicUnit, true},
		{ActionAcademicPeriodView, ResourceAcademicPeriod, true},
		{ActionAcademicPeriodManage, ResourceAcademicPeriod, true},
		{ActionProgrammeView, ResourceProgramme, true},
		{ActionProgrammeManage, ResourceProgramme, true},
		{ActionProgrammeLevelView, ResourceProgrammeLevel, true},
		{ActionProgrammeLevelManage, ResourceProgrammeLevel, true},
		{ActionClassView, ResourceClass, true},
		{ActionClassManage, ResourceClass, true},
		{ActionClassMembersView, ResourceClass, true},
		{ActionClassMembersManage, ResourceClass, true},
		{ActionAcademicProgressionManage, ResourceClass, true},
		{ActionAccessPolicyView, ResourceInstitution, false},
		{ActionAccessPolicyManage, ResourceInstitution, false},
		{ActionInvitationView, ResourceInstitution, true},
		{ActionInvitationCreate, ResourceInstitution, true},
		{ActionInvitationManage, ResourceInstitution, true},
		{ActionOnboardingBatchView, ResourceInstitution, true},
		{ActionOnboardingBatchManage, ResourceInstitution, true},
		{ActionExternalIdentityManage, ResourceUser, false},
		{ActionRoleView, ResourceInstitution, false},
		{ActionRoleManage, ResourceInstitution, false},
		{ActionRoleBindingView, ResourceInstitution, false},
		{ActionRoleBindingManage, ResourceInstitution, false},
		{ActionAcademicAuditView, ResourceAcademicUnit, true},
	}

	all := AllActions()
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			definition, ok := DefinitionForAction(test.action)
			require.True(t, ok, "action %q is not registered", test.action)
			require.Equal(t, test.resourceType, definition.ResourceType, "action %q resource type", test.action)
			require.True(t, IsGrantableAction(string(test.action)), "action %q is not role grantable", test.action)
			require.Equal(t, test.patAllowed, IsPersonalAccessTokenAction(string(test.action)), "action %q PAT eligibility", test.action)
			require.Contains(t, all, string(test.action), "action")
		})
	}
	for _, action := range []Action{ActionOnboardingBatchView, ActionOnboardingBatchManage} {
		definition, _ := DefinitionForAction(action)
		require.True(t, definition.AcceptsResource(ResourceClass), "action %q does not accept exact Class scope", action)
	}
}

func TestExamSittingParticipationActionIsRecognizedButNotRoleGrantable(t *testing.T) {
	t.Parallel()

	definition, ok := DefinitionForAction(ActionExamSittingParticipate)
	require.True(t, ok, "participation definition = %#v", definition)
	require.Equal(t, ResourceExamSitting, definition.ResourceType, "participation definition = %#v", definition)
	require.True(t, definition.RelationshipOnly, "participation definition = %#v", definition)
	require.False(t, IsGrantableAction(string(ActionExamSittingParticipate)), "relationship-only participation action became role grantable")
	for _, action := range AllActions() {
		require.NotEqual(t, string(ActionExamSittingParticipate), action, "relationship-only participation action entered the system administrator role")
	}
}

func TestBrowserActivityHasNoGrantableOverride(t *testing.T) {
	obsolete := Action("exam.attempt.browser_activity.view.override")
	_, known := DefinitionForAction(obsolete)
	require.False(t, known, "obsolete Browser Activity override remained live")
	require.NotContains(t, AllActions(), string(obsolete), "obsolete Browser Activity override remained live")
	definition, known := DefinitionForAction(ActionExamAttemptBrowserActivityView)
	require.True(t, known, "dedicated Browser Activity permission is missing")
	require.Equal(t, ResourceExamSitting, definition.ResourceType, "dedicated Browser Activity permission is missing")
	require.Contains(t, AllActions(), string(ActionExamAttemptBrowserActivityView), "dedicated Browser Activity permission is missing")
}

func TestDesktopCompatibilityPolicyManagementIsSystemAdministratorOnly(t *testing.T) {
	t.Parallel()

	definition, ok := DefinitionForAction(ActionDesktopCompatibilityPolicyManage)
	require.True(t, ok, "desktop compatibility policy definition = %#v", definition)
	require.Equal(t, ResourceInstitution, definition.ResourceType, "desktop compatibility policy definition = %#v", definition)
	require.True(t, definition.InheritInstitutionScope, "desktop compatibility policy definition = %#v", definition)
	require.True(t, definition.SystemAdministratorOnly, "desktop compatibility policy definition = %#v", definition)
	require.False(t, IsGrantableAction(string(ActionDesktopCompatibilityPolicyManage)), "system-administrator-only action became grantable to reusable Roles")
	require.False(t, IsPersonalAccessTokenAction(string(ActionDesktopCompatibilityPolicyManage)), "system-administrator-only action became available to Personal Access Tokens")
	require.Contains(t, AllActions(), string(ActionDesktopCompatibilityPolicyManage), "system-administrator-only action is missing from the protected Role")
}

func TestSubmissionActionsAreResourceTypedAndGrantable(t *testing.T) {
	t.Parallel()

	for _, action := range []Action{ActionSubmissionView, ActionSubmissionViewOverride,
		ActionSubmissionReview, ActionSubmissionReviewOverride,
		ActionSubmissionRelease, ActionSubmissionReleaseOverride} {
		definition, ok := DefinitionForAction(action)
		require.True(t, ok, "Submission action %q definition = %#v", action, definition)
		require.Equal(t, ResourceSubmission, definition.ResourceType, "Submission action %q definition = %#v", action, definition)
		require.True(t, definition.InheritInstitutionScope, "Submission action %q definition = %#v", action, definition)
		require.True(t, definition.InheritAcademicUnitScopes, "Submission action %q definition = %#v", action, definition)
		require.False(t, definition.RelationshipOnly, "Submission action %q definition = %#v", action, definition)
		require.True(t, IsGrantableAction(string(action)), "Submission action %q is not role grantable", action)
	}
	require.NoError(t, (Resource{Type: ResourceSubmission, ID: NewID()}).Validate())
}

func TestUserAuditResourceKeepsItsAcademicScopeSeparate(t *testing.T) {
	event := &AuditEvent{
		ActorID: NewUserID(), SessionID: SessionID(NewID()), Action: string(ActionUserView),
		Resource:  Resource{Type: ResourceUser, ID: NewID()},
		ScopeType: RoleScopeInstitution, ScopeID: NewID(),
		Status: AuditStatusSuccess, NodeID: "node-1",
	}
	event.PrepareCreate(NewAuditEventID(), NowUTC())
	require.NoError(t, event.Validate())
}

func TestAuditEventValidationCloningAndBounds(t *testing.T) {
	event := &AuditEvent{
		ActorID: NewUserID(), SessionID: SessionID(NewID()), Action: string(ActionAuditView),
		Resource:  Resource{Type: ResourceInstitution, ID: NewID()},
		ScopeType: RoleScopeInstitution, ScopeID: NewID(),
		Status: AuditStatusSuccess, NodeID: "node-1",
		IPAddress: "fe80::1%en0", UserAgent: strings.Repeat("a", 600),
		Parameters: []byte(`{"safe":true}`),
	}
	event.PrepareCreate(NewAuditEventID(), NowUTC())
	require.NoError(t, event.Validate())
	require.Len(t, event.UserAgent, 512, "bounded user agent length")
	cloned := event.Clone()
	cloned.Parameters[2] = 'X'
	require.Equal(t, `{"safe":true}`, string(event.Parameters), "Clone exposed audit JSON storage")

	invalid := event.Clone()
	invalid.IPAddress = "not-an-address"
	var validation *ValidationError
	require.ErrorAs(t, invalid.Validate(), &validation)
	require.Equal(t, "model.audit_event.is_valid.ip_address.app_error", validation.Code)
	_, err := EncodeAuditData(strings.Repeat("x", AuditJSONMaxBytes))
	require.Error(t, err, "oversized encoded audit data was accepted")
}
