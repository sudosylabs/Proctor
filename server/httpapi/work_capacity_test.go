// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	application "github.com/sudosylabs/proctor/server/app"
)

func TestLoginCapacityRefusalIsRetryableAndDoesNotIssueCredentials(t *testing.T) {
	logger, _ := newTestLogger(t)
	failure := application.NewError("service.busy").Wrap(errors.New("private capacity detail"))
	fake := &authenticationEntryHTTPApplication{loginError: failure}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, authenticationResource(fake, browserCookies{}))
	request := newJSONRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"login_id":"student","password":"example-password","client_type":"web"}`))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("capacity refusal = %d, Retry-After %q", response.Code, response.Header().Get("Retry-After"))
	}
	if fake.loginCalls != 1 || len(response.Result().Cookies()) != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("capacity refusal retried internally, issued cookies, or was cacheable")
	}
	if !strings.Contains(response.Body.String(), `"code":"service.busy"`) || strings.Contains(response.Body.String(), "private capacity detail") {
		t.Fatalf("unsafe capacity response: %s", response.Body.String())
	}
	if applicationErrorRequiresLogging(failure) {
		t.Fatal("capacity refusals must use bounded counters rather than per-request error logging")
	}
	if !applicationErrorRequiresLogging(application.NewError("authentication.internal")) {
		t.Fatal("unexpected failures lost their operator diagnostics")
	}
}

func TestOnlyExpensiveWorkRoutesDeclareCapacityRefusal(t *testing.T) {
	expected := map[string]bool{
		"POST /api/v1/auth/login":                                                            true,
		"POST /api/v1/auth/reauthenticate/password":                                          true,
		"POST /api/v1/auth/register":                                                         true,
		"POST /api/v1/auth/password-reset/complete":                                          true,
		"POST /api/v1/bootstrap":                                                             true,
		"PUT /api/v1/authentication-methods/password":                                        true,
		"POST /api/v1/auth/browser/invitations/accept":                                       true,
		"POST /api/v1/invitations/student-class/accept":                                      true,
		"POST /api/v1/invitations/teacher-academic-unit/accept":                              true,
		"POST /api/v1/auth/desktop/authorizations/authenticate/password":                     true,
		"GET /api/v1/users/{user_id}/profile-picture":                                        true,
		"PUT /api/v1/users/{user_id}/profile-picture":                                        true,
		"POST /api/v1/exams/{exam_id}/draft/resources":                                       true,
		"PUT /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}/content":             true,
		"POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/correction-resource-stages": true,
	}
	api, err := New(validHTTPOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range api.Routes() {
		key := route.Method + " " + normalizeRuntimeRoutePath(route.Path)
		if slices.Contains(route.ErrorCodes, "service.busy") != expected[key] {
			t.Errorf("%s has incorrect capacity error declaration", key)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatalf("capacity routes missing from the production catalog: %v", expected)
	}
}
