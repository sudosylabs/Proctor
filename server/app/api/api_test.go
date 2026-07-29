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
	if len(routes) != 3 {
		t.Fatalf("route count = %d, want 3", len(routes))
	}
	for _, route := range routes {
		if route.Method == "" || route.Path == "" {
			t.Errorf("route is incomplete: %#v", route)
		}
		if route.Auth != api.AuthPublic {
			t.Errorf("route %s %s auth = %q", route.Method, route.Path, route.Auth)
		}
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
