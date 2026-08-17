// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"slices"
	"strings"
	"testing"
)

func TestAuthorizationRegistryIsClosedAndResourceTyped(t *testing.T) {
	definition, ok := DefinitionForAction(ActionClassMembersView)
	if !ok || definition.ResourceType != ResourceClass ||
		!definition.InheritInstitutionScope ||
		!definition.InheritAcademicUnitScopes {
		t.Fatalf("class-members definition = %#v, %v", definition, ok)
	}
	if _, ok := DefinitionForAction(Action("future.permission")); ok {
		t.Fatal("unknown action was registered")
	}
	if err := (Resource{Type: ResourceClass, ID: NewId()}).Validate(); err != nil {
		t.Fatal("valid class resource was rejected")
	}
	userDefinition, ok := DefinitionForAction(ActionUserView)
	if !ok || userDefinition.ResourceType != ResourceUser ||
		!userDefinition.InheritInstitutionScope ||
		!userDefinition.InheritAcademicUnitScopes {
		t.Fatalf("user-view definition = %#v, %v", userDefinition, ok)
	}
	if err := (Resource{Type: ResourceUser, ID: NewId()}).Validate(); err != nil {
		t.Fatal("valid user resource was rejected")
	}
	examDefinition, ok := DefinitionForAction(ActionExamView)
	if !ok || examDefinition.ResourceType != ResourceExam || !examDefinition.InheritInstitutionScope || !examDefinition.InheritAcademicUnitScopes {
		t.Fatalf("exam-view definition = %#v, %v", examDefinition, ok)
	}
	if err := (Resource{Type: ResourceExam, ID: NewId()}).Validate(); err != nil {
		t.Fatalf("valid exam resource was rejected: %v", err)
	}
	sittingView, ok := DefinitionForAction(ActionExamSittingView)
	if !ok || sittingView.ResourceType != ResourceExamSitting || !sittingView.InheritInstitutionScope || !sittingView.InheritAcademicUnitScopes {
		t.Fatalf("exam-sitting view definition = %#v, %v", sittingView, ok)
	}
	sittingCreate, ok := DefinitionForAction(ActionExamSittingCreate)
	if !ok || sittingCreate.ResourceType != ResourceExam || !sittingCreate.InheritInstitutionScope || !sittingCreate.InheritAcademicUnitScopes {
		t.Fatalf("exam-sitting create definition = %#v, %v", sittingCreate, ok)
	}
	if err := (Resource{Type: ResourceExamSitting, ID: NewId()}).Validate(); err != nil {
		t.Fatalf("valid Exam Sitting resource was rejected: %v", err)
	}
	if err := (Resource{Type: ResourceType("future"), ID: NewId()}).Validate(); err == nil {
		t.Fatal("unimplemented resource type was accepted")
	}
}

func TestJobActionsAreInstitutionScopedAndKnown(t *testing.T) {
	for _, action := range []Action{ActionJobView, ActionJobManage} {
		definition, ok := DefinitionForAction(action)
		if !ok || definition.ResourceType != ResourceInstitution ||
			!definition.InheritInstitutionScope || definition.InheritAcademicUnitScopes {
			t.Fatalf("job action %q definition = %#v, %v", action, definition, ok)
		}
		if !IsKnownAction(string(action)) {
			t.Fatalf("job action %q is not known", action)
		}
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
		definition, ok := DefinitionForAction(test.action)
		if !ok {
			t.Fatalf("action %q is not registered", test.action)
		}
		if definition.ResourceType != test.resourceType {
			t.Fatalf("action %q resource type = %q, want %q", test.action, definition.ResourceType, test.resourceType)
		}
		if !IsGrantableAction(string(test.action)) {
			t.Fatalf("action %q is not role grantable", test.action)
		}
		if got := IsPersonalAccessTokenAction(string(test.action)); got != test.patAllowed {
			t.Fatalf("action %q PAT eligibility = %v, want %v", test.action, got, test.patAllowed)
		}
		if !slices.Contains(all, string(test.action)) {
			t.Fatalf("action %q missing from AllActions", test.action)
		}
	}
}

func TestExamSittingParticipationActionIsRecognizedButNotRoleGrantable(t *testing.T) {
	t.Parallel()

	definition, ok := DefinitionForAction(ActionExamSittingParticipate)
	if !ok || definition.ResourceType != ResourceExamSitting || !definition.RelationshipOnly {
		t.Fatalf("participation definition = %#v, %v", definition, ok)
	}
	if IsGrantableAction(string(ActionExamSittingParticipate)) {
		t.Fatal("relationship-only participation action became role grantable")
	}
	for _, action := range AllActions() {
		if action == string(ActionExamSittingParticipate) {
			t.Fatal("relationship-only participation action entered the system administrator role")
		}
	}
}

func TestSubmissionActionsAreResourceTypedAndGrantable(t *testing.T) {
	t.Parallel()

	for _, action := range []Action{ActionSubmissionView, ActionSubmissionViewOverride,
		ActionSubmissionReview, ActionSubmissionReviewOverride,
		ActionSubmissionRelease, ActionSubmissionReleaseOverride} {
		definition, ok := DefinitionForAction(action)
		if !ok || definition.ResourceType != ResourceSubmission || !definition.InheritInstitutionScope ||
			!definition.InheritAcademicUnitScopes || definition.RelationshipOnly {
			t.Fatalf("Submission action %q definition = %#v, %v", action, definition, ok)
		}
		if !IsGrantableAction(string(action)) {
			t.Fatalf("Submission action %q is not role grantable", action)
		}
	}
	if err := (Resource{Type: ResourceSubmission, ID: NewId()}).Validate(); err != nil {
		t.Fatalf("valid Submission resource was rejected: %v", err)
	}
}

func TestUserAuditResourceKeepsItsAcademicScopeSeparate(t *testing.T) {
	event := &AuditEvent{
		ActorID: NewUserID(), SessionID: SessionID(NewId()), Action: string(ActionUserView),
		Resource:  Resource{Type: ResourceUser, ID: NewId()},
		ScopeType: RoleScopeInstitution, ScopeID: NewId(),
		Status: AuditStatusSuccess, NodeID: "node-1",
	}
	event.PrepareCreate(NewAuditEventID(), NowUTC())
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditEventValidationCloningAndBounds(t *testing.T) {
	event := &AuditEvent{
		ActorID: NewUserID(), SessionID: SessionID(NewId()), Action: string(ActionAuditView),
		Resource:  Resource{Type: ResourceInstitution, ID: NewId()},
		ScopeType: RoleScopeInstitution, ScopeID: NewId(),
		Status: AuditStatusSuccess, NodeID: "node-1",
		IPAddress: "fe80::1%en0", UserAgent: strings.Repeat("a", 600),
		Parameters: []byte(`{"safe":true}`),
	}
	event.PrepareCreate(NewAuditEventID(), NowUTC())
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(event.UserAgent) != 512 {
		t.Fatalf("bounded user agent length = %d", len(event.UserAgent))
	}
	cloned := event.Clone()
	cloned.Parameters[2] = 'X'
	if string(event.Parameters) != `{"safe":true}` {
		t.Fatal("Clone exposed audit JSON storage")
	}

	invalid := event.Clone()
	invalid.IPAddress = "not-an-address"
	if err := invalid.Validate(); err == nil ||
		err.(*ValidationError).Code != "model.audit_event.is_valid.ip_address.app_error" {
		t.Fatalf("invalid IP error = %v", err)
	}
	if _, err := EncodeAuditData(strings.Repeat("x", AuditJSONMaxBytes)); err == nil {
		t.Fatal("oversized encoded audit data was accepted")
	}
}
