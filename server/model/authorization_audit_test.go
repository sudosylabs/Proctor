// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
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
	if (Resource{Type: ResourceClass, Id: NewId()}).IsValid() == false {
		t.Fatal("valid class resource was rejected")
	}
	userDefinition, ok := DefinitionForAction(ActionUserView)
	if !ok || userDefinition.ResourceType != ResourceUser ||
		!userDefinition.InheritInstitutionScope ||
		userDefinition.InheritAcademicUnitScopes {
		t.Fatalf("user-view definition = %#v, %v", userDefinition, ok)
	}
	if !(Resource{Type: ResourceUser, Id: NewId()}).IsValid() {
		t.Fatal("valid user resource was rejected")
	}
	if (Resource{Type: ResourceType("exam"), Id: NewId()}).IsValid() {
		t.Fatal("unimplemented resource type was accepted")
	}
}

func TestUserAuditResourceKeepsItsAcademicScopeSeparate(t *testing.T) {
	event := &AuditEvent{
		ActorId: NewId(), SessionId: NewId(), Action: string(ActionUserView),
		Resource:  Resource{Type: ResourceUser, Id: NewId()},
		ScopeType: RoleScopeInstitution, ScopeId: NewId(),
		Status: AuditStatusSuccess, NodeId: "node-1",
	}
	event.PreSave()
	if appErr := event.IsValid(); appErr != nil {
		t.Fatal(appErr)
	}
}

func TestAuditEventValidationCloningAndBounds(t *testing.T) {
	event := &AuditEvent{
		ActorId: NewId(), SessionId: NewId(), Action: string(ActionAuditView),
		Resource:  Resource{Type: ResourceInstitution, Id: NewId()},
		ScopeType: RoleScopeInstitution, ScopeId: NewId(),
		Status: AuditStatusSuccess, NodeId: "node-1",
		IPAddress: "fe80::1%en0", UserAgent: strings.Repeat("a", 600),
		Parameters: []byte(`{"safe":true}`),
	}
	event.PreSave()
	if appErr := event.IsValid(); appErr != nil {
		t.Fatal(appErr)
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
	if appErr := invalid.IsValid(); appErr == nil ||
		appErr.(*ValidationError).Code != "model.audit_event.is_valid.ip_address.app_error" {
		t.Fatalf("invalid IP error = %v", appErr)
	}
	if _, appErr := EncodeAuditData(strings.Repeat("x", AuditJSONMaxBytes)); appErr == nil {
		t.Fatal("oversized encoded audit data was accepted")
	}
}
