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
	if err := (Resource{Type: ResourceClass, ID: NewId()}).Validate(); err != nil {
		t.Fatal("valid class resource was rejected")
	}
	userDefinition, ok := DefinitionForAction(ActionUserView)
	if !ok || userDefinition.ResourceType != ResourceUser ||
		!userDefinition.InheritInstitutionScope ||
		userDefinition.InheritAcademicUnitScopes {
		t.Fatalf("user-view definition = %#v, %v", userDefinition, ok)
	}
	if err := (Resource{Type: ResourceUser, ID: NewId()}).Validate(); err != nil {
		t.Fatal("valid user resource was rejected")
	}
	if err := (Resource{Type: ResourceType("exam"), ID: NewId()}).Validate(); err == nil {
		t.Fatal("unimplemented resource type was accepted")
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
