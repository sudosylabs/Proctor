//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/config"
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
	encryptionKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.MFA.Enabled = true
			cfg.Authentication.MFA.EncryptionKey = encryptionKey
		}),
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
	bootstrapRequest := map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution": map[string]any{
			"name": "northbridge", "display_name": "Northbridge University",
		},
		"administrator": map[string]any{
			"username": "system-owner", "email": "system-owner@example.edu",
			"display_name": "System Owner",
		},
		"password": password,
	}
	bootstrap := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/bootstrap",
		bootstrapRequest,
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
		AccessPolicy *struct {
			Revision                         int64 `json:"revision"`
			LocalLoginEnabled                bool  `json:"local_login_enabled"`
			PublicRegistrationEnabled        bool  `json:"public_registration_enabled"`
			InvitationAdmissionEnabled       bool  `json:"invitation_admission_enabled"`
			InvitationLocalCredentialEnabled bool  `json:"invitation_local_credential_enabled"`
			DesktopAuthorizationEnabled      bool  `json:"desktop_authorization_enabled"`
		} `json:"access_policy"`
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
	if createdWire.AccessPolicy == nil || createdWire.AccessPolicy.Revision != 1 ||
		!createdWire.AccessPolicy.LocalLoginEnabled || createdWire.AccessPolicy.PublicRegistrationEnabled ||
		!createdWire.AccessPolicy.InvitationAdmissionEnabled ||
		!createdWire.AccessPolicy.InvitationLocalCredentialEnabled ||
		!createdWire.AccessPolicy.DesktopAuthorizationEnabled {
		t.Fatalf("initial access policy = %#v", createdWire.AccessPolicy)
	}
	if len(created.Role.Permissions) != len(model.AllActions()) {
		t.Fatalf("system administrator permissions = %#v", created.Role.Permissions)
	}
	replay := performJSONRequest(
		handler, http.MethodPost, "/api/v1/bootstrap", bootstrapRequest, "",
	)
	if replay.Code != http.StatusCreated || replay.Body.String() != bootstrap.Body.String() {
		t.Fatalf(
			"exact bootstrap replay = %d: %s, want retained %d: %s",
			replay.Code, replay.Body.String(), bootstrap.Code, bootstrap.Body.String(),
		)
	}
	settings, err := persistence.UserSettings().Get(context.Background(), created.Administrator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Source != model.UserSettingsInitialSource ||
		settings.FormatVersion != model.UserSettingsFormatVersion1 ||
		settings.UserID != created.Administrator.ID || !settings.Revision.IsValid() {
		t.Fatalf("bootstrap administrator settings = %#v", settings)
	}
	if second := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/bootstrap",
		map[string]any{
			"bootstrap_secret": testlib.BootstrapSecret,
			"institution":      map[string]any{"name": "second", "display_name": "Second"},
			"administrator":    map[string]any{"username": "second", "email": "second@example.edu"},
			"password":         password,
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
	strengthenRoleAdministratorSession(t, handler, administratorLogin.Tokens.AccessToken)
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
	strengthenRoleAdministratorSession(t, handler, secondLogin.Tokens.AccessToken)
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
		store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var roleSuccesses, bindingSuccesses, bindingFailures, requestDecisionAudits int
	for _, event := range events {
		if event.Action == string(model.ActionRoleView) && event.RequestID == authorizedRoleListRequestID {
			requestDecisionAudits++
		}
		switch event.Action {
		case string(model.ActionRoleManage):
			if event.Status == model.AuditStatusSuccess {
				roleSuccesses++
			}
		case string(model.ActionRoleBindingManage):
			switch event.Status {
			case model.AuditStatusSuccess:
				bindingSuccesses++
			case model.AuditStatusFail:
				bindingFailures++
			}
		}
	}
	if roleSuccesses == 0 || bindingSuccesses == 0 || bindingFailures == 0 {
		t.Fatalf(
			"role administration audit statuses role_successes=%d binding_successes=%d binding_failures=%d",
			roleSuccesses,
			bindingSuccesses,
			bindingFailures,
		)
	}
	if requestDecisionAudits != 1 {
		t.Fatalf(
			"application use case wrote %d authorization decisions for one request",
			requestDecisionAudits,
		)
	}
}

func strengthenRoleAdministratorSession(t *testing.T, handler http.Handler, accessToken string) {
	t.Helper()
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/me/mfa/setup",
		nil,
		accessToken,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("MFA setup status = %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(setupResponse.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	activation := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/me/mfa/activate",
		map[string]any{"code": integrationTOTP(t, setup.Secret, time.Now().UTC().Unix()/30)},
		accessToken,
	)
	if activation.Code != http.StatusOK {
		t.Fatalf("MFA activation status = %d: %s", activation.Code, activation.Body.String())
	}
	var recovery struct {
		Codes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(activation.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if len(recovery.Codes) == 0 {
		t.Fatal("MFA activation returned no recovery codes")
	}
	rechallenge := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/me/mfa/challenge",
		map[string]any{"code": recovery.Codes[0]},
		accessToken,
	)
	if rechallenge.Code != http.StatusOK {
		t.Fatalf("MFA challenge status = %d: %s", rechallenge.Code, rechallenge.Body.String())
	}
}

type unknownBootstrapOutcomeStore struct {
	store.Store
	failNext atomic.Bool
	calls    atomic.Int32
}

func (s *unknownBootstrapOutcomeStore) Installation() store.InstallationStore {
	return unknownBootstrapOutcomeInstallation{delegate: s.Store.Installation(), owner: s}
}

type unknownBootstrapOutcomeInstallation struct {
	delegate store.InstallationStore
	owner    *unknownBootstrapOutcomeStore
}

func (s unknownBootstrapOutcomeInstallation) Get(ctx context.Context) (*model.InstallationState, error) {
	return s.delegate.Get(ctx)
}

func (s unknownBootstrapOutcomeInstallation) Bootstrap(
	ctx context.Context,
	input *store.InstallationBootstrap,
) (*model.InstallationBootstrapResult, error) {
	s.owner.calls.Add(1)
	result, err := s.delegate.Bootstrap(ctx, input)
	if err == nil && s.owner.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("bootstrap commit outcome unknown")
	}
	return result, err
}

func (s unknownBootstrapOutcomeInstallation) ReconcileSystemAdministratorRole(
	ctx context.Context,
	input *store.SystemAdministratorRoleReconciliation,
) (*store.SystemAdministratorRoleReconciliationResult, error) {
	return s.delegate.ReconcileSystemAdministratorRole(ctx, input)
}

func (s unknownBootstrapOutcomeInstallation) RecoverAdministratorAccess(
	ctx context.Context,
	input *store.AdministratorRecovery,
) (*store.AdministratorRecoveryResult, error) {
	return s.delegate.RecoverAdministratorAccess(ctx, input)
}

func (s unknownBootstrapOutcomeInstallation) ReconcileAdministratorRecovery(
	ctx context.Context,
	input *store.AdministratorRecoveryReconciliation,
) (*store.AdministratorRecoveryReconciliationResult, error) {
	return s.delegate.ReconcileAdministratorRecovery(ctx, input)
}

func TestBootstrapUnknownCommitOutcomeReconcilesThroughRealGraph(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	uncertain := &unknownBootstrapOutcomeStore{Store: persistence}
	uncertain.failNext.Store(true)
	helper := testlib.Setup(t, testlib.WithStore(uncertain))

	response := performJSONRequest(helper.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "unknown-outcome", "display_name": "Unknown Outcome"},
		"administrator":    map[string]any{"username": "unknown-owner", "email": "unknown-owner@example.edu"},
		"password":         "correct horse battery staple",
	}, "")
	if response.Code != http.StatusCreated || uncertain.calls.Load() < 2 {
		t.Fatalf("bootstrap response=%d %s calls=%d", response.Code, response.Body.String(), uncertain.calls.Load())
	}
	var result struct {
		AccessPolicy *struct {
			Revision int64 `json:"revision"`
		} `json:"access_policy"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AccessPolicy == nil || result.AccessPolicy.Revision != 1 {
		t.Fatalf("bootstrap result = %#v", result)
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
