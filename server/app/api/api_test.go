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

	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/testlib"
)

type compatibilityProblem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail"`
	Instance  string            `json:"instance"`
	Code      string            `json:"code"`
	RequestID string            `json:"request_id"`
	Fields    map[string]string `json:"fields"`
}

func TestRoutesHaveExplicitAuthenticationPolicy(t *testing.T) {
	t.Parallel()

	helper := testlib.Setup(t)
	routes := helper.API.Routes()
	if len(routes) != 87 {
		t.Fatalf("route count = %d, want 87", len(routes))
	}
	expected := map[string]api.AuthRequirement{
		http.MethodGet + " /health/live":                                                                                       api.AuthPublic,
		http.MethodGet + " /health/ready":                                                                                      api.AuthPublic,
		http.MethodGet + " /api/v1/system/version":                                                                             api.AuthPublic,
		http.MethodPost + " /api/v1/auth/login":                                                                                api.AuthPublic,
		http.MethodPost + " /api/v1/auth/refresh":                                                                              api.AuthRefreshCredentialRequired,
		http.MethodPost + " /api/v1/auth/logout":                                                                               api.AuthSessionRequired,
		http.MethodPost + " /api/v1/auth/email-verification/complete":                                                          api.AuthPublic,
		http.MethodPost + " /api/v1/auth/email-verification/request":                                                           api.AuthSessionRequired,
		http.MethodPost + " /api/v1/auth/password-reset/complete":                                                              api.AuthPublic,
		http.MethodPost + " /api/v1/auth/password-reset/request":                                                               api.AuthPublic,
		http.MethodGet + " /api/v1/websocket":                                                                                  api.AuthSessionRequired,
		http.MethodGet + " /api/v1/auth/providers":                                                                             api.AuthPublic,
		http.MethodGet + " /api/v1/auth/providers/{provider_id:[a-z0-9][a-z0-9._-]{0,63}}/login":                               api.AuthPublic,
		http.MethodGet + " /api/v1/auth/providers/{provider_id:[a-z0-9][a-z0-9._-]{0,63}}/callback":                            api.AuthPublic,
		http.MethodGet + " /api/v1/users/me":                                                                                   api.AuthPrincipalRequired,
		http.MethodGet + " /api/v1/users/me/sessions":                                                                          api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/sessions/revoke":                                                                  api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/sessions/revoke-all":                                                              api.AuthSessionRequired,
		http.MethodGet + " /api/v1/users/me/mfa":                                                                               api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/mfa/setup":                                                                        api.AuthRecentSessionRequired,
		http.MethodPost + " /api/v1/users/me/mfa/activate":                                                                     api.AuthRecentSessionRequired,
		http.MethodPost + " /api/v1/users/me/mfa/challenge":                                                                    api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/mfa/recovery-codes/regenerate":                                                    api.AuthStrongRecentSessionRequired,
		http.MethodPost + " /api/v1/users/me/mfa/disable":                                                                      api.AuthStrongRecentSessionRequired,
		http.MethodPost + " /api/v1/users/me/tokens":                                                                           api.AuthRecentSessionRequired,
		http.MethodGet + " /api/v1/users/me/tokens":                                                                            api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/tokens/{personal_access_token_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}/disable": api.AuthSessionRequired,
		http.MethodPost + " /api/v1/users/me/tokens/{personal_access_token_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}/enable":  api.AuthRecentSessionRequired,
		http.MethodDelete + " /api/v1/users/me/tokens/{personal_access_token_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":       api.AuthSessionRequired,
		http.MethodGet + " /api/v1/audits":                                                                                     api.AuthPrincipalRequired,
		http.MethodGet + " /api/v1/bootstrap":                                                                                  api.AuthPublic,
		http.MethodPost + " /api/v1/bootstrap":                                                                                 api.AuthPublic,
		http.MethodGet + " /api/v1/roles":                                                                                      api.AuthPrincipalRequired,
		http.MethodPost + " /api/v1/roles":                                                                                     api.AuthPrincipalRequired,
		http.MethodGet + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                                     api.AuthPrincipalRequired,
		http.MethodPatch + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                                   api.AuthPrincipalRequired,
		http.MethodDelete + " /api/v1/roles/{role_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                                  api.AuthPrincipalRequired,
		http.MethodGet + " /api/v1/role-bindings":                                                                              api.AuthPrincipalRequired,
		http.MethodPost + " /api/v1/role-bindings":                                                                             api.AuthPrincipalRequired,
		http.MethodDelete + " /api/v1/role-bindings/{role_binding_id:[ybndrfg8ejkmcpqxot1uwisza345h769]{26}}":                  api.AuthPrincipalRequired,
	}
	for _, route := range routes {
		if route.Method == "" || route.Path == "" {
			t.Errorf("route is incomplete: %#v", route)
		}
		key := route.Method + " " + route.Path
		want, exists := expected[key]
		if !exists {
			want = api.AuthPrincipalRequired
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
	if helper.API.Routes()[0].Path == "/mutated" {
		t.Fatal("Routes exposed mutable internal state")
	}
}

func TestHealthVersionAndCommonHeaders(t *testing.T) {
	t.Parallel()

	buildInfo := api.BuildInfo{
		Version: "test-version", Commit: "test-commit", BuildTime: "test-time", GoVersion: "test-go",
	}
	helper := testlib.Setup(t, testlib.WithBuildInfo(buildInfo))

	liveness := performRequest(helper.Handler(), http.MethodGet, "/health/live", "desktop-client-1")
	if liveness.Code != http.StatusOK {
		t.Fatalf("liveness status = %d: %s", liveness.Code, liveness.Body.String())
	}
	if liveness.Header().Get(api.RequestIDHeader) != "desktop-client-1" {
		t.Errorf("request ID = %q", liveness.Header().Get(api.RequestIDHeader))
	}
	if liveness.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers were not applied")
	}
	if liveness.Header().Get("Content-Type") != "application/json" ||
		liveness.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("liveness headers = %#v", liveness.Header())
	}
	var health map[string]string
	if err := json.Unmarshal(liveness.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" {
		t.Fatalf("liveness response = %#v", health)
	}

	readiness := performRequest(helper.Handler(), http.MethodGet, "/health/ready", "")
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", readiness.Code)
	}
	helper.Health.SetReady(true)
	readiness = performRequest(helper.Handler(), http.MethodGet, "/health/ready", "")
	if readiness.Code != http.StatusOK {
		t.Fatalf("ready status = %d", readiness.Code)
	}

	version := performRequest(helper.Handler(), http.MethodGet, "/api/v1/system/version", "")
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
		method  string
		path    string
		status  int
		code    string
		allow   string
		typeURI string
		title   string
		detail  string
	}{
		{
			http.MethodGet, "/missing", http.StatusNotFound, "not_found", "",
			"https://proctor.sudosylabs.com/problems/not-found",
			"Resource not found", "The requested resource was not found.",
		},
		{
			http.MethodPost, "/health/live", http.StatusMethodNotAllowed,
			"method_not_allowed", http.MethodGet,
			"https://proctor.sudosylabs.com/problems/method-not-allowed",
			"Method not allowed",
			"The request method is not allowed for this resource.",
		},
	}
	for _, test := range tests {
		response := performRequest(
			helper.Handler(),
			test.method,
			test.path,
			"compatibility-request",
		)
		if response.Code != test.status {
			t.Errorf("%s %s status = %d", test.method, test.path, response.Code)
		}
		if response.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("content type = %q", response.Header().Get("Content-Type"))
		}
		if response.Header().Get("Allow") != test.allow {
			t.Errorf("Allow = %q", response.Header().Get("Allow"))
		}
		var problem compatibilityProblem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Error(err)
		} else if problem.Type != test.typeURI ||
			problem.Title != test.title ||
			problem.Status != test.status ||
			problem.Detail != test.detail ||
			problem.Instance != test.path ||
			problem.Code != test.code ||
			problem.RequestID != "compatibility-request" {
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
			request.Header.Set(api.RequestIDHeader, "compatibility-request")
			if test.configure != nil {
				test.configure(request)
			}
			response := httptest.NewRecorder()
			helper.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var problem compatibilityProblem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "authentication.required" {
				t.Fatalf("problem = %#v", problem)
			}
			if problem.Type != "https://proctor.sudosylabs.com/problems/authentication.required" ||
				problem.Title != "Authentication required" ||
				problem.Status != http.StatusUnauthorized ||
				problem.Detail != "Authentication is required." ||
				problem.Instance != request.URL.Path ||
				problem.RequestID != "compatibility-request" {
				t.Fatalf("authentication problem contract = %#v", problem)
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
		helper.Handler().ServeHTTP(response, request)
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
