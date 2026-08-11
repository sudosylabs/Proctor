// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type systemHTTPHealth struct {
	live  bool
	ready bool
}

func (health systemHTTPHealth) Live() bool  { return health.live }
func (health systemHTTPHealth) Ready() bool { return health.ready }

func TestSystemResourceReturnsTypedUnhealthyProblemThroughKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, systemResource(systemHTTPHealth{}, BuildInfo{Version: "test"}))
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("liveness status = %d: %s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "not_live" || problem.Detail != "The process is not healthy." {
		t.Fatalf("liveness problem = %#v", problem)
	}
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
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(body)) {
			t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestBootstrapResourceStrictDecodeAndDeclaredFailureThroughKernel(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	bootstrap := &recordingBootstrap{bootstrapErr: application.NewError("installation.unavailable")}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, bootstrapResource(bootstrap))

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(`{"unknown":true}`)))
	if invalid.Code != http.StatusBadRequest || !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"request.invalid"`)) {
		t.Fatalf("strict bootstrap decode = %d %s", invalid.Code, invalid.Body.String())
	}

	failure := httptest.NewRecorder()
	httpAPI.ServeHTTP(failure, httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewBufferString(`{"institution":{"name":"northbridge","display_name":"Northbridge"},"administrator":{"username":"admin","email":"admin@example.edu"},"password":"secret"}`)))
	if failure.Code != http.StatusInternalServerError || !bytes.Contains(failure.Body.Bytes(), []byte(`"code":"installation.unavailable"`)) {
		t.Fatalf("bootstrap failure = %d %s", failure.Code, failure.Body.String())
	}
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
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["initialized"] != true {
		t.Fatalf("body = %#v", body)
	}
	for _, key := range []string{"institution_id", "administrator_user_id", "state", "role", "password"} {
		if _, exists := body[key]; exists {
			t.Fatalf("status leaked %q: %#v", key, body)
		}
	}
}

func TestBootstrapResponseDTOPreservesHistoricalEnvelope(t *testing.T) {
	t.Parallel()
	institutionID := model.NewId()
	adminID := model.NewId()
	roleID := model.NewId()
	bindingID := model.NewId()
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
	}
	encoded, err := json.Marshal(installationBootstrapResponseFromModel(result))
	if err != nil {
		t.Fatal(err)
	}
	var decoded installationBootstrapResponse
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.State == nil || decoded.State.InstitutionID != institutionID ||
		decoded.Administrator == nil || decoded.Administrator.ID != adminID ||
		decoded.Role == nil || decoded.Role.Name != model.SystemAdministratorRoleName || !decoded.Role.BuiltIn ||
		decoded.RoleBinding == nil || decoded.RoleBinding.ScopeType != model.RoleScopeInstitution {
		t.Fatalf("decoded historical envelope = %#v from %s", decoded, encoded)
	}
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
		"institution":   map[string]any{"name": "northbridge", "display_name": "Northbridge"},
		"administrator": map[string]any{"username": "admin", "email": "admin@example.com"},
		"password":      "correct-horse-battery",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if rec.command.InstitutionName != "northbridge" || rec.command.AdministratorUsername != "admin" ||
		rec.command.Password != "correct-horse-battery" || rec.command.Source == "" {
		t.Fatalf("command = %#v", rec.command)
	}
}

type recordingBootstrap struct {
	command      application.BootstrapInstallationCommand
	result       *model.InstallationBootstrapResult
	bootstrapErr error
}

func (r *recordingBootstrap) GetInstallationStatus(context.Context, application.GetInstallationStatusQuery) (*model.InstallationStatus, error) {
	return &model.InstallationStatus{Initialized: false}, nil
}

func (r *recordingBootstrap) BootstrapInstallation(_ context.Context, _ application.Invocation, command application.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error) {
	r.command = command
	return r.result, r.bootstrapErr
}
