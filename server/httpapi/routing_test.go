// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoutingKernelCentralizesAPIVersionRegexAndParams(t *testing.T) {
	t.Parallel()

	var gotRoleID string
	httpAPI := newCompiledRoutingTestAPI(t, "/api/testing", newResource("role",
		publicRoute(
			http.MethodGet,
			apiPath(literal("roles"), canonicalID("role_id")),
			nil,
			func(request operationRequest) (operationResult, error) {
				roleID, err := request.params.RequireRoleId()
				if err != nil {
					return operationResult{}, err
				}
				gotRoleID = roleID
				return noContentResult(), nil
			},
		),
	))

	valid := httptest.NewRequest(http.MethodGet, "/api/testing/roles/"+modelIDForRouteTest, nil)
	validResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusNoContent || gotRoleID != modelIDForRouteTest {
		t.Fatalf("valid route status/id = %d/%q", validResponse.Code, gotRoleID)
	}

	for _, path := range []string{
		"/api/v1/roles/" + modelIDForRouteTest,
		"/api/testing/roles/not-an-id",
		"/api/testing/roles/3UUUid3i7bexfcbzmo6s5w4zqo",
	} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.Code)
		}
	}
}

func TestRouteMetadataRetainsVersionRegexAndPolicyContract(t *testing.T) {
	t.Parallel()

	httpAPI := newCompiledRoutingTestAPI(t, "/api/testing", newResource("role",
		sessionRoute(
			http.MethodDelete,
			apiPath(literal("roles"), canonicalID("role_id")),
			[]string{"authentication.required"},
			func(operationRequest) (operationResult, error) { return noContentResult(), nil },
		),
	))
	routes := httpAPI.Routes()
	want := "/api/testing/roles/{role_id:" + canonicalIDRoutePattern() + "}"
	if len(routes) != 1 || routes[0].Path != want || routes[0].Auth != AuthSessionRequired {
		t.Fatalf("routes = %#v, want path %q with session policy", routes, want)
	}
	policy := newRouteErrorPolicy(routes[0].ErrorCodes)
	for _, code := range routeErrorCodes(AuthSessionRequired, []string{"authentication.required"}) {
		if _, exists := policy[code]; !exists {
			t.Fatalf("routes = %#v, missing protected-route error %q", routes, code)
		}
	}
}

func TestPrincipalAssuranceRequirements(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_000_000)
	recent := model.Principal{
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now.Add(-time.Minute),
	}
	strongOld := model.Principal{
		AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt:        now.Add(-time.Hour),
		MFACompletedAt:         model.OptionalTimeFrom(now.Add(-time.Hour)),
	}
	strongRecent := model.Principal{
		AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt:        now.Add(-time.Hour),
		MFACompletedAt:         model.OptionalTimeFrom(now.Add(-time.Minute)),
	}
	if appErr := requirePrincipalAssurance(
		recent, AuthRecentSessionRequired, now, 15*time.Minute,
	); appErr != nil {
		t.Fatalf("recent session rejected: %v", appErr)
	}
	if appErr := requirePrincipalAssurance(
		recent, AuthStrongSessionRequired, now, 15*time.Minute,
	); applicationErrorCode(appErr) != "authentication.strong_required" {
		t.Fatalf("single-factor strong requirement error = %v", appErr)
	}
	if appErr := requirePrincipalAssurance(
		strongOld, AuthRecentSessionRequired, now, 15*time.Minute,
	); applicationErrorCode(appErr) != "authentication.reauthentication_required" {
		t.Fatalf("old recent requirement error = %v", appErr)
	}
	if appErr := requirePrincipalAssurance(
		strongRecent,
		AuthStrongRecentSessionRequired,
		now,
		15*time.Minute,
	); appErr != nil {
		t.Fatalf("strong recent session rejected: %v", appErr)
	}
}

func newRoutingTestAPI(_ string) *API {
	return &API{
		catalog:                 newRouteCatalogBuilder(),
		recentAuthenticationTTL: 15 * time.Minute,
	}
}

func newCompiledRoutingTestAPI(t *testing.T, apiURLSuffix string, resources ...resource) *API {
	t.Helper()
	logger, _ := newTestLogger(t)
	httpAPI := &API{
		authenticator:           classRouteAuthenticator{},
		logger:                  logger,
		localizer:               newTestLocalizer(t),
		recentAuthenticationTTL: 15 * time.Minute,
	}
	if err := httpAPI.buildRoutingKernel(apiURLSuffix, 1<<20, func() error {
		return httpAPI.collectResources(apiURLSuffix, resources...)
	}); err != nil {
		t.Fatal(err)
	}
	return httpAPI
}

const modelIDForRouteTest = "3uuuid3i7bexfcbzmo6s5w4zqo"
