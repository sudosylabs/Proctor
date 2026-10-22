// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type systemHTTPHealth struct {
	live  bool
	ready bool
}

func (h systemHTTPHealth) Live() bool  { return h.live }
func (h systemHTTPHealth) Ready() bool { return h.ready }

func TestSystemResourceReturnsTypedUnhealthyProblemThroughKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, systemResource(systemHTTPHealth{}, BuildInfo{Version: "test"}))
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code, "liveness status = %d: %s", response.Code, response.Body.String())
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "health.not_live", problem.Code)
	require.Equal(t, "The process is not healthy.", problem.Detail)
}

func TestSystemResourceReturnsHealthyStatusAndVersionThroughKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResource(systemHTTPHealth{live: true, ready: true}, BuildInfo{Version: "test-version"}),
	)
	for path, body := range map[string]string{
		"/health/live":           `"status":"ok"`,
		"/health/ready":          `"status":"ok"`,
		"/api/v1/system/version": `"version":"test-version"`,
	} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code, "%s response = %d %s", path, response.Code, response.Body.String())
		require.True(t, bytes.Contains(response.Body.Bytes(), []byte(body)), "%s response = %d %s", path, response.Code, response.Body.String())
	}
}

func TestBootstrapResourceStrictDecodeAndDeclaredFailureThroughKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	bootstrap := &recordingBootstrap{bootstrapErr: apperror.New("installation.unavailable")}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, bootstrapResource(bootstrap))

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, newJSONRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(`{"unknown":true}`)))
	require.Equal(t, http.StatusBadRequest, invalid.Code, "strict bootstrap decode = %d %s", invalid.Code, invalid.Body.String())
	require.True(t, bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"request.invalid"`)), "strict bootstrap decode = %d %s", invalid.Code, invalid.Body.String())

	failure := httptest.NewRecorder()
	httpAPI.ServeHTTP(failure, newJSONRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(`{"institution":{"name":"northbridge","display_name":"Northbridge"},"administrator":{"username":"admin","email":"admin@example.edu"},"password":"secret","bootstrap_secret":"deployment-secret"}`)))
	require.Equal(t, http.StatusServiceUnavailable, failure.Code, "bootstrap failure = %d %s", failure.Code, failure.Body.String())
	require.True(t, bytes.Contains(failure.Body.Bytes(), []byte(`"code":"installation.unavailable"`)), "bootstrap failure = %d %s", failure.Code, failure.Body.String())
}

func TestBootstrapStatusExposesOnlyInitializedFlag(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		bootstrapResource(&bootstrapHTTPApplication{status: &model.InstallationStatus{Initialized: true}}),
	)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil))
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, true, body["initialized"])
	for _, key := range []string{"institution_id", "administrator_user_id", "state", "role", "password"} {
		_, exists := body[key]
		require.False(t, exists, "status leaked %q: %#v", key, body)
	}
}

func TestBootstrapDeniedDoesNotEchoSecretOrAccountDetail(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	bootstrap := &recordingBootstrap{bootstrapErr: apperror.New("installation.bootstrap_denied")}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, bootstrapResource(bootstrap))
	const secret = "wrong-secret-material-that-must-not-be-echoed"
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, newJSONRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(
		`{"institution":{"name":"northbridge","display_name":"Northbridge"},`+
			`"administrator":{"username":"admin","email":"admin@example.edu"},`+
			`"password":"password","bootstrap_secret":"`+secret+`"}`,
	)))
	require.Equal(t, http.StatusForbidden, response.Code, "response = %d %s", response.Code, response.Body.String())
	require.True(t, bytes.Contains(response.Body.Bytes(), []byte(`"code":"installation.bootstrap_denied"`)), "response = %d %s", response.Code, response.Body.String())
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(secret)), "response = %d %s", response.Code, response.Body.String())
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("admin@example.edu")), "response = %d %s", response.Code, response.Body.String())
}

func TestBootstrapResponseDTOPreservesHistoricalEnvelope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewID()
	adminID := model.NewID()
	roleID := model.NewID()
	bindingID := model.NewID()
	result := &model.InstallationBootstrapResult{
		State: &model.InstallationState{
			InitializedAt: model.TimeFromMillis(100), InstitutionID: model.InstitutionID(institutionID), AdministratorUserID: model.UserID(adminID),
		},
		Institution: &model.Institution{
			ID: model.InstitutionID(institutionID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
			Name: "northbridge", DisplayName: "Northbridge",
		},
		Administrator: &model.User{
			ID: model.UserID(adminID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
			Username: "admin", Email: "admin@example.com",
			Locale: "en", Timezone: "UTC",
		},
		Role: &model.Role{
			ID: model.RoleID(roleID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), Name: model.SystemAdministratorRoleName,
			DisplayName: "System Administrator", BuiltIn: true,
			Permissions: []string{string(model.ActionUserView)},
		},
		RoleBinding: &model.RoleBinding{
			ID: model.RoleBindingID(bindingID), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100), UserID: model.UserID(adminID), RoleID: model.RoleID(roleID),
			ScopeType: model.RoleScopeInstitution, ScopeID: institutionID, StartsAt: model.TimeFromMillis(100),
		},
		AccessPolicy: model.NewInitialAccessPolicy(model.NewAccessPolicyID(), model.TimeFromMillis(100)),
	}
	encoded, err := json.Marshal(installationBootstrapResponseFromModel(nil, result))
	require.NoError(t, err)
	var decoded installationBootstrapResponse
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.State, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.Equal(t, institutionID, decoded.State.InstitutionID, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.NotNil(t, decoded.Administrator, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.Equal(t, adminID, decoded.Administrator.ID, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.NotNil(t, decoded.Role, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.Equal(t, model.SystemAdministratorRoleName, decoded.Role.Name, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.True(t, decoded.Role.BuiltIn, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.NotNil(t, decoded.RoleBinding, "decoded historical envelope = %#v from %s", decoded, encoded)
	require.Equal(t, model.RoleScopeInstitution, decoded.RoleBinding.ScopeType, "decoded historical envelope = %#v from %s", decoded, encoded)
}

func TestBootstrapUsesApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	rec := &recordingBootstrap{
		result: &model.InstallationBootstrapResult{
			State: &model.InstallationState{
				InitializedAt: model.TimeFromMillis(1), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID(),
			},
		},
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, bootstrapResource(rec))
	body, _ := json.Marshal(map[string]any{
		"institution":      map[string]any{"name": "northbridge", "display_name": "Northbridge"},
		"administrator":    map[string]any{"username": "admin", "email": "admin@example.com"},
		"password":         "correct-horse-battery",
		"bootstrap_secret": "deployment-bootstrap-secret",
	})
	request := newJSONRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, "northbridge", rec.command.InstitutionName)
	require.Equal(t, "admin", rec.command.AdministratorUsername)
	require.Equal(t, "correct-horse-battery", rec.command.Password)
	require.Equal(t, "deployment-bootstrap-secret", rec.command.BootstrapSecret)
	require.NotEmpty(t, rec.command.Source)
}

type recordingBootstrap struct {
	command      app.BootstrapInstallationCommand
	result       *model.InstallationBootstrapResult
	bootstrapErr error
}

func (r *recordingBootstrap) GetInstallationStatus(context.Context, app.GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	return &model.InstallationStatus{Initialized: false}, nil
}

func (r *recordingBootstrap) BootstrapInstallation(_ context.Context, _ invocation.Invocation, command app.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	r.command = command
	return r.result, r.bootstrapErr
}
