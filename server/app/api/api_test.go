// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestRoutesHaveExplicitAuthenticationPolicy(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	routes := helper.Server.API().Routes()
	if len(routes) != 66 {
		t.Fatalf("route count = %d, want 66", len(routes))
	}
	expected := map[string]api.AuthRequirement{
		http.MethodGet + " /health/live":                                                                      api.AuthPublic,
		http.MethodGet + " /health/ready":                                                                     api.AuthPublic,
		http.MethodGet + " /api/v1/system/version":                                                            api.AuthPublic,
		http.MethodPost + " /api/v1/auth/login":                                                               api.AuthPublic,
		http.MethodPost + " /api/v1/auth/refresh":                                                             api.AuthRefreshCredentialRequired,
		http.MethodPost + " /api/v1/auth/logout":                                                              api.AuthSessionRequired,
		http.MethodGet + " /api/v1/users/me":                                                                  api.AuthSessionRequired,
		http.MethodGet + " /api/v1/users/me/sessions":                                                         api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/sessions/revoke":                                                 api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/sessions/revoke-all":                                             api.AuthSessionRequired,
		http.MethodGet + " /api/v1/audits":                                                                    api.AuthSessionRequired,
		http.MethodGet + " /api/v1/bootstrap":                                                                 api.AuthPublic,
		http.MethodPost + " /api/v1/bootstrap":                                                                api.AuthPublic,
		http.MethodGet + " /api/v1/roles":                                                                     api.AuthSessionRequired,
		http.MethodPost + " /api/v1/roles":                                                                    api.AuthSessionRequired,
		http.MethodGet + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                    api.AuthSessionRequired,
		http.MethodPatch + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                  api.AuthSessionRequired,
		http.MethodDelete + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                 api.AuthSessionRequired,
		http.MethodGet + " /api/v1/role-bindings":                                                             api.AuthSessionRequired,
		http.MethodPost + " /api/v1/role-bindings":                                                            api.AuthSessionRequired,
		http.MethodDelete + " /api/v1/role-bindings/{role_binding_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}": api.AuthSessionRequired,
	}
	for _, route := range routes {
		if route.Method == "" || route.Path == "" {
			t.Errorf("route is incomplete: %#v", route)
		}
		key := route.Method + " " + route.Path
		want, exists := expected[key]
		if !exists {
			want = api.AuthSessionRequired
		}
		if route.Auth != want {
			t.Errorf("route %s auth = %q, want %q", key, route.Auth, want)
		}
		if exists {
			delete(expected, key)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing routes = %#v", expected)
	}
	routes[0].Path = "/mutated"
	if helper.Server.API().Routes()[0].Path == "/mutated" {
		t.Fatal("Routes exposed mutable internal state")
	}
}

func TestHealthVersionAndCommonHeaders(t *testing.T) {
	t.Parallel()

	buildInfo := api.BuildInfo{
		Version: "test-version", Commit: "test-commit", BuildTime: "test-time", GoVersion: "test-go",
	}
	helper := testlib.Setup(t, testlib.WithServerOptions(app.WithBuildInfo(buildInfo)))

	liveness := performRequest(helper.Server.Handler(), http.MethodGet, "/health/live", "desktop-client-1")
	if liveness.Code != http.StatusOK {
		t.Fatalf("liveness status = %d: %s", liveness.Code, liveness.Body.String())
	}
	if liveness.Header().Get(api.RequestIDHeader) != "desktop-client-1" {
		t.Errorf("request ID = %q", liveness.Header().Get(api.RequestIDHeader))
	}
	if liveness.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers were not applied")
	}

	readiness := performRequest(helper.Server.Handler(), http.MethodGet, "/health/ready", "")
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", readiness.Code)
	}
	helper.Server.Health().SetReady(true)
	readiness = performRequest(helper.Server.Handler(), http.MethodGet, "/health/ready", "")
	if readiness.Code != http.StatusOK {
		t.Fatalf("ready status = %d", readiness.Code)
	}

	version := performRequest(helper.Server.Handler(), http.MethodGet, "/api/v1/system/version", "")
	var got api.BuildInfo
	if err := json.Unmarshal(version.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != buildInfo {
		t.Fatalf("build info = %#v, want %#v", got, buildInfo)
	}
}

func TestRoutingFailuresUseProblemDetails(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	tests := []struct {
		method string
		path   string
		status int
		code   string
		allow  string
	}{
		{http.MethodGet, "/missing", http.StatusNotFound, "not_found", ""},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed, "method_not_allowed", http.MethodGet},
	}
	for _, test := range tests {
		response := performRequest(helper.Server.Handler(), test.method, test.path, "")
		if response.Code != test.status {
			t.Errorf("%s %s status = %d", test.method, test.path, response.Code)
		}
		if response.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("content type = %q", response.Header().Get("Content-Type"))
		}
		if response.Header().Get("Allow") != test.allow {
			t.Errorf("Allow = %q", response.Header().Get("Allow"))
		}
		var problem api.Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Error(err)
		} else if problem.Code != test.code || problem.RequestID == "" {
			t.Errorf("problem = %#v", problem)
		}
	}
}

func TestAuthenticationBoundaryRejectsMissingAmbiguousAndURLCredentials(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	tests := []struct {
		name      string
		method    string
		path      string
		configure func(*http.Request)
	}{
		{
			name:   "missing session credential",
			method: http.MethodGet,
			path:   "/api/v1/users/me",
		},
		{
			name:   "missing refresh credential",
			method: http.MethodPost,
			path:   "/api/v1/auth/refresh",
		},
		{
			name:   "missing session-list credential",
			method: http.MethodGet,
			path:   "/api/v1/users/me/sessions",
		},
		{
			name:   "missing privileged credential",
			method: http.MethodGet,
			path:   "/api/v1/audits",
		},
		{
			name:   "missing session-revoke credential",
			method: http.MethodPost,
			path:   "/api/v1/users/me/sessions/revoke",
		},
		{
			name:   "missing revoke-all credential",
			method: http.MethodPost,
			path:   "/api/v1/users/me/sessions/revoke-all",
		},
		{
			name:   "query credential is never accepted",
			method: http.MethodGet,
			path:   "/api/v1/users/me?access_token=secret",
		},
		{
			name:   "duplicate authorization headers",
			method: http.MethodGet,
			path:   "/api/v1/users/me",
			configure: func(request *http.Request) {
				request.Header.Add("Authorization", "Bearer first")
				request.Header.Add("Authorization", "Bearer second")
			},
		},
		{
			name:   "non bearer authorization",
			method: http.MethodGet,
			path:   "/api/v1/users/me",
			configure: func(request *http.Request) {
				request.Header.Set("Authorization", "Basic secret")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.configure != nil {
				test.configure(request)
			}
			response := httptest.NewRecorder()
			helper.Server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var problem api.Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "authentication.required" {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

func TestAuthenticationRequestsUseStrictJSON(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	tests := []string{
		`{"login_id":"user@example.com","password":"password","client_type":"desktop","unknown":true}`,
		`{"login_id":"user@example.com"} {"password":"password"}`,
	}
	for _, body := range tests {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		helper.Server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, response = %s", body, response.Code, response.Body.String())
		}
		var problem api.Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem.Code != "request.invalid" {
			t.Fatalf("problem = %#v", problem)
		}
	}
}

func TestWriteErrorMapsApplicationErrorsWithoutLeakingCauses(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		appErr := model.NewAppError(
			"TestWriteErrorMapsApplicationErrorsWithoutLeakingCauses",
			"invalid_email",
			nil,
			"database detail",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"email": "invalid"}).Wrap(
			errors.New("sensitive internal cause"),
		)
		appErr.Translate(func(string, ...any) string {
			return "The email address is invalid."
		})
		api.WriteError(
			writer,
			request,
			appErr,
		)
	})
	response := performRequest(handler, http.MethodGet, "/users", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatal("internal cause leaked to client")
	}
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "invalid_email" || problem.Fields["email"] != "invalid" {
		t.Fatalf("problem = %#v", problem)
	}
	if problem.Detail != "The email address is invalid." {
		t.Fatalf("problem detail = %q", problem.Detail)
	}
}

func performRequest(handler http.Handler, method, path, requestID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if requestID != "" {
		request.Header.Set(api.RequestIDHeader, requestID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
