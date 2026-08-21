// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleHTTPCreateUsesApplicationCommandWithoutPreflight(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	roleID := model.NewId()
	roles := &roleHTTPApplication{result: &model.Role{ID: model.RoleID(roleID), Name: "teacher", DisplayName: "Teacher",
		Permissions: []string{string(model.ActionClassView)},
	}}
	transport := &academicUnitHTTPApplication{principal: principal}
	httpAPI, err := New(Options{
		Logger: logger, Health: academicUnitHTTPHealth{}, Application: transport,
		AcademicUnits: transport, Institutions: transport, Programmes: &programmeHTTPApplication{},
		ProgrammeLevels: &programmeLevelHTTPApplication{}, AcademicPeriods: &academicPeriodHTTPApplication{},
		Classes: &classHTTPApplication{}, Affiliations: &affiliationHTTPApplication{},
		AcademicUnitMembers: &academicUnitMemberHTTPApplication{}, ClassMembers: &classMemberHTTPApplication{},
		UserProfiles: &userProfileHTTPApplication{}, AccountStates: &accountStateHTTPApplication{},
		SessionAdministrations: &sessionAdministrationHTTPApplication{}, Roles: roles, RoleBindings: &roleBindingHTTPApplication{}, AuditListings: &auditListingHTTPApplication{}, Bootstrap: &bootstrapHTTPApplication{},
		BuildInfo: BuildInfo{Version: "test"}, PublicURL: "http://localhost:8065",
		MaxBodyBytes: 1 << 20, RecentAuthenticationTTL: time.Minute, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpAPI.Close() })
	body, _ := json.Marshal(map[string]any{
		"name": "teacher", "display_name": "Teacher",
		"permissions": []string{string(model.ActionClassView)},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/roles", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if roles.createCommand.Name != "teacher" {
		t.Fatalf("command = %#v", roles.createCommand)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != roleID || payload["name"] != "teacher" {
		t.Fatalf("body = %#v", payload)
	}
}

func TestRoleResponsePreservesExistingWireShape(t *testing.T) {
	t.Parallel()
	role := &model.Role{
		ID: model.NewRoleID(), CreatedAt: model.TimeFromMillis(10), UpdatedAt: model.TimeFromMillis(20), Name: "teacher",
		DisplayName: "Teacher", Description: "desc",
		Permissions: []string{string(model.ActionClassView)}, BuiltIn: false,
	}
	want, err := json.Marshal(roleResponse{
		ID: role.ID.String(), CreateAt: 10, UpdateAt: 20, DeleteAt: 0,
		Name: "teacher", DisplayName: "Teacher", Description: "desc",
		Permissions: []string{string(model.ActionClassView)}, BuiltIn: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(roleResponseFromModel(role))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("response = %s, want %s", got, want)
	}
}
