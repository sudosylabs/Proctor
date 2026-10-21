//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestBootstrapAndRoleAdministrationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
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
	require.Equal(t, http.StatusOK, status.Code, "pristine bootstrap status")
	require.Equal(t, "{\"initialized\":false}\n", status.Body.String(), "pristine bootstrap status")
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
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
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
	require.NoError(t, json.Unmarshal(bootstrap.Body.Bytes(), &createdWire))
	created.Institution = &model.Institution{ID: model.InstitutionID(createdWire.Institution.ID)}
	created.Administrator = createdWire.Administrator.model()
	created.Role = &model.Role{ID: model.RoleID(createdWire.Role.ID), Name: createdWire.Role.Name, Permissions: createdWire.Role.Permissions, BuiltIn: createdWire.Role.BuiltIn}
	created.RoleBinding = &model.RoleBinding{ID: model.RoleBindingID(createdWire.RoleBinding.ID), ScopeType: createdWire.RoleBinding.ScopeType}
	require.NotNil(t, created.Role)
	require.Equal(t, model.SystemAdministratorRoleName, created.Role.Name)
	require.True(t, created.Role.BuiltIn)
	require.NotNil(t, created.RoleBinding)
	require.Equal(t, model.RoleScopeInstitution, created.RoleBinding.ScopeType)
	require.NotNil(t, createdWire.AccessPolicy)
	require.Equal(t, int64(1), createdWire.AccessPolicy.Revision)
	require.True(t, createdWire.AccessPolicy.LocalLoginEnabled)
	require.False(t, createdWire.AccessPolicy.PublicRegistrationEnabled)
	require.True(t, createdWire.AccessPolicy.InvitationAdmissionEnabled)
	require.True(t, createdWire.AccessPolicy.InvitationLocalCredentialEnabled)
	require.True(t, createdWire.AccessPolicy.DesktopAuthorizationEnabled)
	require.Len(t, created.Role.Permissions, len(model.AllActions()))
	replay := performJSONRequest(
		handler, http.MethodPost, "/api/v1/bootstrap", bootstrapRequest, "",
	)
	require.Equal(t, http.StatusCreated, replay.Code, "exact bootstrap replay = %d: %s, want retained %d", replay.Code, replay.Body.String(), bootstrap.Code)
	require.Equal(t, bootstrap.Body.String(), replay.Body.String(), "exact bootstrap replay = %d: %s, want retained %d", replay.Code, replay.Body.String(), bootstrap.Code)
	settings, err := persistence.UserSettings().Get(context.Background(), created.Administrator.ID)
	require.NoError(t, err)
	require.Equal(t, model.UserSettingsInitialSource, settings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
	require.Equal(t, created.Administrator.ID, settings.UserID)
	require.True(t, settings.Revision.IsValid())
	second := performJSONRequest(
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
	)
	require.Equal(t, http.StatusConflict, second.Code, "second bootstrap status = %d: %s", second.Code, second.Body.String())
	status = performJSONRequest(handler, http.MethodGet, "/api/v1/bootstrap", nil, "")
	require.Equal(t, "{\"initialized\":true}\n", status.Body.String())
	require.False(t, containsAny(status.Body.String(), created.Administrator.ID.String(), created.Institution.ID.String()))

	administratorLogin := loginIntegrationUser(
		t, handler, created.Administrator.Username, password,
		model.SessionClientCLI, "administrator-cli",
	)
	strengthenRoleAdministratorSession(t, handler, administratorLogin.Tokens.AccessToken)
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil)
	listRequest.Header.Set(
		"Authorization",
		"Bearer "+administratorLogin.Tokens.AccessToken,
	)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	authorizedRoleListRequestID := listResponse.Header().Get("X-Request-ID")
	require.Equal(t, http.StatusOK, listResponse.Code, "authorized role list status = %d: %s", listResponse.Code, listResponse.Body.String())
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
	require.Equal(t, http.StatusBadRequest, rejectServerOwnedRoleFields.Code, "server-owned role field status = %d: %s", rejectServerOwnedRoleFields.Code, rejectServerOwnedRoleFields.Body.String())
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
	require.Equal(t, http.StatusCreated, createRole.Code, "create role status = %d: %s", createRole.Code, createRole.Body.String())
	var customRole model.Role
	require.NoError(t, json.Unmarshal(createRole.Body.Bytes(), &customRole))
	require.False(t, customRole.BuiltIn, "client created a built-in role")
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
	require.Equal(t, http.StatusBadRequest, unknownPermission.Code, "unknown permission status = %d: %s", unknownPermission.Code, unknownPermission.Body.String())
	renamed := "Class Observer Updated"
	patchRole := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/roles/"+customRole.ID.String(),
		map[string]any{"display_name": renamed},
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, patchRole.Code, "patch role status = %d: %s", patchRole.Code, patchRole.Body.String())
	protected := performJSONRequest(
		handler,
		http.MethodPatch,
		"/api/v1/roles/"+created.Role.ID.String(),
		map[string]any{"display_name": "Unsafe"},
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusConflict, protected.Code, "patch built-in role status = %d: %s", protected.Code, protected.Body.String())

	secondAdministrator, appErr := helper.App.CreateLocalUser(
		context.Background(),
		app.CreateLocalUserCommand{User: &model.User{
			Username: "second-administrator", Email: "second-administrator@example.edu",
			DisplayName: "Second Administrator",
		}, Password: password},
	)
	require.NoError(t, appErr)
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
	require.Equal(t, http.StatusBadRequest, malformedResponse.Code, "malformed role create status = %d: %s", malformedResponse.Code, malformedResponse.Body.String())
	createBinding := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/role-bindings",
		map[string]any{
			"user_id": secondAdministrator.ID.String(), "role_id": customRole.ID.String(),
			"scope_type": model.RoleScopeInstitution, "scope_id": created.Institution.ID.String(),
			"start_at": integrationWireTime(model.GetMillis()),
		},
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusCreated, createBinding.Code, "create binding status = %d: %s; logs=%s", createBinding.Code, createBinding.Body.String(), helper.Logs.String())
	var customBinding model.RoleBinding
	require.NoError(t, json.Unmarshal(createBinding.Body.Bytes(), &customBinding))
	listBindings := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/role-bindings?user_id="+secondAdministrator.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, listBindings.Code, "list bindings status = %d: %s", listBindings.Code, listBindings.Body.String())
	endBinding := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/role-bindings/"+customBinding.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, endBinding.Code, "end binding status = %d: %s", endBinding.Code, endBinding.Body.String())
	archiveRole := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/roles/"+customRole.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, archiveRole.Code, "archive role status = %d: %s", archiveRole.Code, archiveRole.Body.String())

	secondAdminBinding := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/role-bindings",
		map[string]any{
			"user_id": secondAdministrator.ID.String(), "role_id": created.Role.ID.String(),
			"scope_type": model.RoleScopeInstitution, "scope_id": created.Institution.ID.String(),
			"start_at": integrationWireTime(model.GetMillis()),
		},
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusCreated, secondAdminBinding.Code, "create second administrator binding status = %d: %s", secondAdminBinding.Code, secondAdminBinding.Body.String())
	var secondBinding model.RoleBinding
	require.NoError(t, json.Unmarshal(secondAdminBinding.Body.Bytes(), &secondBinding))
	removeFirst := performJSONRequest(
		handler,
		http.MethodDelete,
		"/api/v1/role-bindings/"+created.RoleBinding.ID.String(),
		nil,
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, removeFirst.Code, "remove first administrator status = %d: %s", removeFirst.Code, removeFirst.Body.String())
	lostImmediately := performJSONRequest(
		handler, http.MethodGet, "/api/v1/roles", nil,
		administratorLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusForbidden, lostImmediately.Code, "ended administrator authorization status = %d: %s", lostImmediately.Code, lostImmediately.Body.String())
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
	require.Equal(t, http.StatusConflict, removeLast.Code, "remove last administrator status = %d: %s", removeLast.Code, removeLast.Body.String())

	events, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	require.NoError(t, err)
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
	require.NotEqual(t, 0, roleSuccesses, "role administration audit statuses role_successes")
	require.NotEqual(t, 0, bindingSuccesses, "role administration audit statuses role_successes")
	require.NotEqual(t, 0, bindingFailures, "role administration audit statuses role_successes")
	require.Equal(t, 1, requestDecisionAudits, "application use case wrote")
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
	require.Equal(t, http.StatusCreated, setupResponse.Code, "MFA setup status = %d: %s", setupResponse.Code, setupResponse.Body.String())
	var setup struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(setupResponse.Body.Bytes(), &setup))
	activation := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/me/mfa/activate",
		map[string]any{"code": integrationTOTP(t, setup.Secret, time.Now().UTC().Unix()/30)},
		accessToken,
	)
	require.Equal(t, http.StatusOK, activation.Code, "MFA activation status = %d: %s", activation.Code, activation.Body.String())
	var recovery struct {
		Codes []string `json:"recovery_codes"`
	}
	require.NoError(t, json.Unmarshal(activation.Body.Bytes(), &recovery))
	require.NotEmpty(t, recovery.Codes, "MFA activation returned no recovery codes")
	rechallenge := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/users/me/mfa/challenge",
		map[string]any{"code": recovery.Codes[0]},
		accessToken,
	)
	require.Equal(t, http.StatusOK, rechallenge.Code, "MFA challenge status = %d: %s", rechallenge.Code, rechallenge.Body.String())
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

func (s unknownBootstrapOutcomeInstallation) ResetAdministratorMFA(ctx context.Context, input *store.AdministratorMFAReset) (*store.AdministratorMFAResetResult, error) {
	return s.delegate.ResetAdministratorMFA(ctx, input)
}

func TestBootstrapUnknownCommitOutcomeReconcilesThroughRealGraph(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
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
	require.Equal(t, http.StatusCreated, response.Code, "bootstrap response=%d %s calls", response.Code, response.Body.String())
	require.GreaterOrEqual(t, uncertain.calls.Load(), int32(2), "bootstrap response=%d %s calls", response.Code, response.Body.String())
	var result struct {
		AccessPolicy *struct {
			Revision int64 `json:"revision"`
		} `json:"access_policy"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.NotNil(t, result.AccessPolicy)
	require.Equal(t, int64(1), result.AccessPolicy.Revision)
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
