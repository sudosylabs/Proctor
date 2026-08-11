//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestBootstrapAndRoleAdministrationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
	)
	handler := helper.Handler()
	password := "correct horse battery staple"

	status := performJSONRequest(
		handler, http.MethodGet, "/api/v1/bootstrap", nil, "",
	)
	if status.Code != http.StatusOK ||
		status.Body.String() != "{\"initialized\":false}\n" {
		t.Fatalf("pristine bootstrap status = %d: %s", status.Code, status.Body.String())
	}
	bootstrap := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
			"institution": map[string]any{
				"name": "northbridge", "display_name": "Northbridge University",
			},
			"administrator": map[string]any{
				"username": "system-owner", "email": "system-owner@example.edu",
				"display_name": "System Owner",
			},
			"password": password,
		},
		"",
	)
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	var created model.InstallationBootstrapResult
	var createdWire struct {
		Institution *struct {
			ID string `json:"id"`
		} `json:"institution"`
		Administrator *wireUserProfileResponse `json:"administrator"`
		Role          *struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
			BuiltIn     bool     `json:"built_in"`
		} `json:"role"`
		RoleBinding *struct {
			ID        string              `json:"id"`
			ScopeType model.RoleScopeType `json:"scope_type"`
		} `json:"role_binding"`
	}
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &createdWire); err != nil {
		t.Fatal(err)
	}
	created.Institution = &model.Institution{ID: model.InstitutionID(createdWire.Institution.ID)}
	created.Administrator = createdWire.Administrator.model()
	created.Role = &model.Role{ID: model.RoleID(createdWire.Role.ID), Name: createdWire.Role.Name, Permissions: createdWire.Role.Permissions, BuiltIn: createdWire.Role.BuiltIn}
	created.RoleBinding = &model.RoleBinding{ID: model.RoleBindingID(createdWire.RoleBinding.ID), ScopeType: createdWire.RoleBinding.ScopeType}
	if created.Role == nil ||
		created.Role.Name != model.SystemAdministratorRoleName ||
		!created.Role.BuiltIn ||
		created.RoleBinding == nil ||
		created.RoleBinding.ScopeType != model.RoleScopeInstitution {
		t.Fatalf("bootstrap result = %#v", created)
	}
	if len(created.Role.Permissions) != len(model.AllActions()) {
		t.Fatalf("system administrator permissions = %#v", created.Role.Permissions)
	}
	if second := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
			"institution":   map[string]any{"name": "second", "display_name": "Second"},
			"administrator": map[string]any{"username": "second", "email": "second@example.edu"},
			"password":      password,
		},
		"",
	); second.Code != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d: %s", second.Code, second.Body.String())
	}
	status = performJSONRequest(handler, http.MethodGet, "/api/v1/bootstrap", nil, "")
	if status.Body.String() != "{\"initialized\":true}\n" ||
		containsAny(status.Body.String(), created.Administrator.ID.String(), created.Institution.ID.String()) {
		t.Fatalf("initialized bootstrap status leaked identifiers: %s", status.Body.String())
	}

	administratorLogin := loginIntegrationUser(
		t, handler, created.Administrator.Username, password,
		model.SessionClientCLI, "administrator-cli",
	)
	authorizedRoleListRequestID := "authorized-role-list-test"
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil)
	listRequest.Header.Set(
		"Authorization",
		"Bearer "+administratorLogin.Tokens.AccessToken,
	)
	listRequest.Header.Set("X-Request-ID", authorizedRoleListRequestID)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf(
			"authorized role list status = %d: %s",
			listResponse.Code,
			listResponse.Body.String(),
		)
	}
	rejectServerOwnedRoleFields := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/roles",
		map[string]any{
			"name": "class-observer", "display_name": "Class Observer",
			"permissions": []string{string(model.ActionClassView)},
			"built_in":    true,
		},
		administratorLogin.Tokens.AccessToken,
	)
	if rejectServerOwnedRoleFields.Code != http.StatusBadRequest {
		t.Fatalf(
			"server-owned role field status = %d: %s",
			rejectServerOwnedRoleFields.Code,
			rejectServerOwnedRoleFields.Body.String(),
		)
	}
	createRole := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/roles",
		map[string]any{
			"name": "class-observer", "display_name": "Class Observer",
			"permissions": []string{string(model.ActionClassView)},
		},
		administratorLogin.Tokens.AccessToken,
	)
	if createRole.Code != http.StatusCreated {
		t.Fatalf("create role status = %d: %s", createRole.Code, createRole.Body.String())
	}
	var customRole model.Role
	if err := json.Unmarshal(createRole.Body.Bytes(), &customRole); err != nil {
		t.Fatal(err)
	}
	if customRole.BuiltIn {
		t.Fatal("client created a built-in role")
	}
	unknownPermission := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/roles",
		map[string]any{
			"name": "unknown-permission", "display_name": "Unknown Permission",
			"permissions": []string{"future.unknown"},
		},
		administratorLogin.Tokens.AccessToken,
	)
	if unknownPermission.Code != http.StatusBadRequest {
		t.Fatalf(
			"unknown permission status = %d: %s",
			unknownPermission.Code,
			unknownPermission.Body.String(),
		)
	}
	renamed := "Class Observer Updated"
	patchRole := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/roles/"+customRole.ID.String(),
		map[string]any{"display_name": renamed},
		administratorLogin.Tokens.AccessToken,
	)
	if patchRole.Code != http.StatusOK {
		t.Fatalf("patch role status = %d: %s", patchRole.Code, patchRole.Body.String())
	}
	protected := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/roles/"+created.Role.ID.String(),
		map[string]any{"display_name": "Unsafe"},
		administratorLogin.Tokens.AccessToken,
	)
	if protected.Code != http.StatusConflict {
		t.Fatalf("patch built-in role status = %d: %s", protected.Code, protected.Body.String())
	}

	secondAdministrator, appErr := helper.App.CreateLocalUser(
		context.Background(),
		&model.User{
			Username: "second-administrator", Email: "second-administrator@example.edu",
			DisplayName: "Second Administrator",
		},
		password,
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
	unprivilegedLogin := loginIntegrationUser(
		t, handler, secondAdministrator.Username, password,
		model.SessionClientCLI, "unprivileged-cli",
	)
	malformedRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/roles",
		strings.NewReader("{"),
	)
	malformedRequest.Header.Set("Content-Type", "application/json")
	malformedRequest.Header.Set(
		"Authorization",
		"Bearer "+unprivilegedLogin.Tokens.AccessToken,
	)
	malformedResponse := httptest.NewRecorder()
	handler.ServeHTTP(malformedResponse, malformedRequest)
	// Malformed bodies are rejected at the transport decode boundary before
	// the application-owned authorization boundary runs.
	if malformedResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"malformed role create status = %d: %s",
			malformedResponse.Code,
			malformedResponse.Body.String(),
		)
	}
	createBinding := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/role-bindings",
		map[string]any{
			"user_id": secondAdministrator.ID.String(), "role_id": customRole.ID.String(),
			"scope_type": model.RoleScopeInstitution, "scope_id": created.Institution.ID.String(),
			"start_at": model.GetMillis(),
		},
		administratorLogin.Tokens.AccessToken,
	)
	if createBinding.Code != http.StatusCreated {
		t.Fatalf("create binding status = %d: %s", createBinding.Code, createBinding.Body.String())
	}
	var customBinding model.RoleBinding
	if err := json.Unmarshal(createBinding.Body.Bytes(), &customBinding); err != nil {
		t.Fatal(err)
	}
	listBindings := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/role-bindings?user_id="+secondAdministrator.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	if listBindings.Code != http.StatusOK {
		t.Fatalf("list bindings status = %d: %s", listBindings.Code, listBindings.Body.String())
	}
	endBinding := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/role-bindings/"+customBinding.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	if endBinding.Code != http.StatusOK {
		t.Fatalf("end binding status = %d: %s", endBinding.Code, endBinding.Body.String())
	}
	archiveRole := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/roles/"+customRole.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	if archiveRole.Code != http.StatusNoContent {
		t.Fatalf("archive role status = %d: %s", archiveRole.Code, archiveRole.Body.String())
	}

	secondAdminBinding := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/role-bindings",
		map[string]any{
			"user_id": secondAdministrator.ID.String(), "role_id": created.Role.ID.String(),
			"scope_type": model.RoleScopeInstitution, "scope_id": created.Institution.ID.String(),
			"start_at": model.GetMillis(),
		},
		administratorLogin.Tokens.AccessToken,
	)
	if secondAdminBinding.Code != http.StatusCreated {
		t.Fatalf(
			"create second administrator binding status = %d: %s",
			secondAdminBinding.Code,
			secondAdminBinding.Body.String(),
		)
	}
	var secondBinding model.RoleBinding
	if err := json.Unmarshal(secondAdminBinding.Body.Bytes(), &secondBinding); err != nil {
		t.Fatal(err)
	}
	removeFirst := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/role-bindings/"+created.RoleBinding.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	if removeFirst.Code != http.StatusOK {
		t.Fatalf("remove first administrator status = %d: %s", removeFirst.Code, removeFirst.Body.String())
	}
	if lostImmediately := performJSONRequest(
		handler, http.MethodGet, "/api/v1/roles", nil,
		administratorLogin.Tokens.AccessToken,
	); lostImmediately.Code != http.StatusForbidden {
		t.Fatalf(
			"ended administrator authorization status = %d: %s",
			lostImmediately.Code,
			lostImmediately.Body.String(),
		)
	}
	secondLogin := loginIntegrationUser(
		t, handler, secondAdministrator.Username, password,
		model.SessionClientCLI, "second-administrator-cli",
	)
	removeLast := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/role-bindings/"+secondBinding.ID.String(),
		nil,
		secondLogin.Tokens.AccessToken,
	)
	if removeLast.Code != http.StatusConflict {
		t.Fatalf("remove last administrator status = %d: %s", removeLast.Code, removeLast.Body.String())
	}

	events, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	var successes, failures, requestDecisionAudits int
	for _, event := range events {
		if event.Action != string(model.ActionRoleManage) {
			continue
		}
		if event.RequestID == authorizedRoleListRequestID {
			requestDecisionAudits++
		}
		switch event.Status {
		case model.AuditStatusSuccess:
			successes++
		case model.AuditStatusFail:
			failures++
		}
	}
	if successes == 0 || failures == 0 {
		t.Fatalf(
			"role administration audit statuses successes=%d failures=%d",
			successes,
			failures,
		)
	}
	if requestDecisionAudits != 1 {
		t.Fatalf(
			"application use case wrote %d authorization decisions for one request",
			requestDecisionAudits,
		)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
