// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
)

// newJSONRequest keeps body-shape and use-case tests on the JSON protocol.
// Request-policy tests use raw httptest requests to exercise media rejection.
func newJSONRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequestWithContext(context.Background(), method, target, body)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestPublicLoginRejectsUnsafeBrowserRequestsBeforeAuthentication(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		contentType []string
		origin      []string
		site        []string
		accepted    bool
	}{
		{name: "same origin JSON", contentType: []string{"application/json"}, origin: []string{"http://localhost:8065"}, site: []string{"same-origin"}, accepted: true},
		{name: "native JSON", contentType: []string{"application/json"}, accepted: true},
		{name: "UTF-8 JSON", contentType: []string{"application/json; charset=UTF-8"}, accepted: true},
		{name: "cross-site simple request", contentType: []string{"text/plain"}, origin: []string{"https://other.example"}, site: []string{"cross-site"}},
		{name: "cross-site JSON", contentType: []string{"application/json"}, origin: []string{"https://other.example"}},
		{name: "same-site sibling", contentType: []string{"application/json"}, site: []string{"same-site"}},
		{name: "fetch metadata without origin", contentType: []string{"application/json"}, site: []string{"cross-site"}},
		{name: "contradictory headers", contentType: []string{"application/json"}, origin: []string{"https://other.example"}, site: []string{"same-origin"}},
		{name: "opaque origin", contentType: []string{"application/json"}, origin: []string{"null"}},
		{name: "empty origin", contentType: []string{"application/json"}, origin: []string{""}},
		{name: "duplicate origins", contentType: []string{"application/json"}, origin: []string{"http://localhost:8065", "http://localhost:8065"}},
		{name: "origin path", contentType: []string{"application/json"}, origin: []string{"http://localhost:8065/path"}},
		{name: "origin credentials", contentType: []string{"application/json"}, origin: []string{"http://user@localhost:8065"}},
		{name: "origin query", contentType: []string{"application/json"}, origin: []string{"http://localhost:8065?"}},
		{name: "duplicate fetch metadata", contentType: []string{"application/json"}, site: []string{"same-origin", "same-origin"}},
		{name: "unknown fetch metadata", contentType: []string{"application/json"}, site: []string{"unknown"}},
		{name: "text without browser headers", contentType: []string{"text/plain"}},
		{name: "form media type", contentType: []string{"application/x-www-form-urlencoded"}},
		{name: "multipart media type", contentType: []string{"multipart/form-data; boundary=x"}},
		{name: "missing media type"},
		{name: "duplicate media type", contentType: []string{"application/json", "application/json"}},
		{name: "invalid media type", contentType: []string{"application/json,"}},
		{name: "unsupported charset", contentType: []string{"application/json; charset=utf-16"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, _ := newTestLogger(t)
			fake := &authenticationEntryHTTPApplication{loginError: application.NewError("authentication.invalid_credentials")}
			api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, authenticationResource(fake, browserCookies{}))
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/login",
				strings.NewReader(`{"login_id":"student","password":"test-password","client_type":"web"}`))
			request.Host = "other.example"
			request.Header.Set("X-Forwarded-Host", "other.example")
			request.Header.Set("X-Forwarded-Proto", "https")
			for key, values := range map[string][]string{"Content-Type": test.contentType, "Origin": test.origin, "Sec-Fetch-Site": test.site} {
				for _, value := range values {
					request.Header.Add(key, value)
				}
			}
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			wantStatus, wantCalls := http.StatusBadRequest, 0
			if test.accepted {
				wantStatus, wantCalls = http.StatusUnauthorized, 1
			}
			if response.Code != wantStatus || fake.loginCalls != wantCalls || len(response.Header().Values("Set-Cookie")) != 0 {
				t.Fatalf("status/calls/cookies = %d/%d/%d, want %d/%d/0", response.Code, fake.loginCalls,
					len(response.Header().Values("Set-Cookie")), wantStatus, wantCalls)
			}
			if !test.accepted && !strings.Contains(response.Body.String(), `"code":"request.invalid"`) {
				t.Fatalf("request rejection = %s", response.Body.String())
			}
		})
	}
}

func TestBrowserRequestPolicyPreservesProviderGETAndConfiguredOrigin(t *testing.T) {
	t.Parallel()
	policy, err := newBrowserRequestPolicy("https://Proctor.example:443/")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://internal-node/api/v1/auth/login", nil)
	request.Header.Set("Origin", "https://proctor.example")
	if !policy.allows(request) {
		t.Fatal("canonical configured public origin rejected behind an internal proxy address")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		request := httptest.NewRequestWithContext(t.Context(), method, "/api/v1/auth/providers/campus/callback?code=example", nil)
		request.Header.Set("Origin", "https://identity.example")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		if !policy.allows(request) {
			t.Fatalf("safe provider/navigation method %s rejected", method)
		}
	}
}

func TestProductionRoutesDeclareBrowserRequestRejection(t *testing.T) {
	t.Parallel()
	api, err := New(validHTTPOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range api.Routes() {
		if !requiresCSRF(route.Method) {
			continue
		}
		code := "authentication.csrf.invalid"
		if route.Auth == AuthPublic {
			code = "request.invalid"
		}
		if !slices.Contains(route.ErrorCodes, code) {
			t.Errorf("%s %s omits %s", route.Method, route.Path, code)
		}
	}
}
