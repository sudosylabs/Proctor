// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/httpapi"
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

func TestHealthVersionAndCommonHeaders(t *testing.T) {
	t.Parallel()

	buildInfo := httpapi.BuildInfo{
		Version: "test-version", Commit: "test-commit", BuildTime: "test-time", GoVersion: "test-go",
	}
	helper := testlib.Setup(t, testlib.WithBuildInfo(buildInfo))

	liveness := performRequest(helper.Handler(), http.MethodGet, "/health/live", "desktop-client-1")
	require.Equal(t, http.StatusOK, liveness.Code, "liveness status = %d: %s", liveness.Code, liveness.Body.String())
	// A client-supplied request ID never becomes server correlation.
	requestID := liveness.Header().Get(httpapi.RequestIDHeader)
	assert.NotEmpty(t, requestID)
	assert.NotEqual(t, "desktop-client-1", requestID, "client request ID became server correlation")
	assert.Equal(t, "nosniff", liveness.Header().Get("X-Content-Type-Options"), "security headers were not applied")
	require.Equal(t, "application/json", liveness.Header().Get("Content-Type"))
	require.Equal(t, "no-store", liveness.Header().Get("Cache-Control"))
	var health map[string]string
	require.NoError(t, json.Unmarshal(liveness.Body.Bytes(), &health))
	require.Equal(t, "ok", health["status"])

	readiness := performRequest(helper.Handler(), http.MethodGet, "/health/ready", "")
	require.Equal(t, http.StatusServiceUnavailable, readiness.Code, "not-ready status")

	version := performRequest(helper.Handler(), http.MethodGet, "/api/v1/system/version", "")
	var got httpapi.BuildInfo
	require.NoError(t, json.Unmarshal(version.Body.Bytes(), &got))
	require.Equal(t, buildInfo, got, "build info")
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
			http.MethodGet, "/missing", http.StatusNotFound, "route.not_found", "",
			"https://proctor.sudosylabs.com/problems/route.not_found",
			"Resource not found", "The requested resource was not found.",
		},
		{
			http.MethodPost, "/health/live", http.StatusMethodNotAllowed,
			"route.method_not_allowed", http.MethodGet,
			"https://proctor.sudosylabs.com/problems/route.method_not_allowed",
			"Method not allowed",
			"The request method is not allowed for this resource.",
		},
	}
	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			response := performRequest(
				helper.Handler(),
				test.method,
				test.path,
				"compatibility-request",
			)
			assert.Equal(t, test.status, response.Code, "%s %s status", test.method, test.path)
			assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
			assert.Equal(t, test.allow, response.Header().Get("Allow"))
			var problem compatibilityProblem
			if !assert.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem)) {
				return
			}
			assert.Equal(t, test.typeURI, problem.Type)
			assert.Equal(t, test.title, problem.Title)
			assert.Equal(t, test.status, problem.Status)
			assert.Equal(t, test.detail, problem.Detail)
			assert.Equal(t, test.path, problem.Instance)
			assert.Equal(t, test.code, problem.Code)
			assert.NotEmpty(t, problem.RequestID)
			assert.NotEqual(t, "compatibility-request", problem.RequestID, "client request ID became server correlation")
			assert.Equal(t, response.Header().Get(httpapi.RequestIDHeader), problem.RequestID)
		})
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
			request.Header.Set(httpapi.RequestIDHeader, "compatibility-request")
			if test.configure != nil {
				test.configure(request)
			}
			response := httptest.NewRecorder()
			helper.Handler().ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code, "status = %d, body = %s", response.Code, response.Body.String())
			var problem compatibilityProblem
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
			require.Equal(t, "authentication.required", problem.Code)
			require.Equal(t, "https://proctor.sudosylabs.com/problems/authentication.required", problem.Type)
			require.Equal(t, "Authentication required", problem.Title)
			require.Equal(t, http.StatusUnauthorized, problem.Status)
			require.Equal(t, "Authentication is required.", problem.Detail)
			require.Equal(t, request.URL.Path, problem.Instance)
			require.NotEmpty(t, problem.RequestID)
			require.NotEqual(t, "compatibility-request", problem.RequestID)
			require.Equal(t, response.Header().Get(httpapi.RequestIDHeader), problem.RequestID)
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
		require.Equal(t, http.StatusBadRequest, response.Code, "body %q: status = %d, response = %s", body, response.Code, response.Body.String())
		var problem httpapi.Problem
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
		require.Equal(t, "request.invalid", problem.Code)
	}
}

func TestWriteErrorMapsApplicationErrorsWithoutLeakingCauses(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		httpapi.WriteError(
			writer,
			request,
			apperror.New("request.invalid").
				WithField("field", "email").
				Wrap(errors.New("sensitive internal cause")),
		)
	})
	response := performRequest(handler, http.MethodGet, "/users", "")
	require.Equal(t, http.StatusBadRequest, response.Code, "status")
	require.NotContains(t, response.Body.String(), "sensitive", "internal cause leaked to client")
	var problem httpapi.Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "request.invalid", problem.Code)
	require.Equal(t, "email", problem.Fields["field"])
}

func performRequest(handler http.Handler, method, path, requestID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if requestID != "" {
		request.Header.Set(httpapi.RequestIDHeader, requestID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
