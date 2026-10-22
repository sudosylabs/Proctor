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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
				roleID, err := request.params.RequireRoleID()
				if err != nil {
					return operationResult{}, err
				}
				gotRoleID = roleID.String()
				return noContentResult(), nil
			},
		),
	))

	valid := httptest.NewRequest(http.MethodGet, "/api/testing/roles/"+modelIDForRouteTest, nil)
	validResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(validResponse, valid)
	require.Equal(t, http.StatusNoContent, validResponse.Code, "valid route status/id")
	require.Equal(t, modelIDForRouteTest, gotRoleID, "valid route status/id")

	for _, path := range []string{
		"/api/v1/roles/" + modelIDForRouteTest,
		"/api/testing/roles/not-an-id",
		"/api/testing/roles/3UUUid3i7bexfcbzmo6s5w4zqo",
	} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, response.Code, "%s status", path)
	}
}

func TestRouteMetadataRetainsVersionRegexAndPolicyContract(t *testing.T) {
	t.Parallel()

	httpAPI := newCompiledRoutingTestAPI(t, "/api/testing", newResource("role",
		sessionRoute(
			http.MethodDelete,
			apiPath(literal("roles"), canonicalID("role_id")),
			[]string{"resource.not_found"},
			func(operationRequest) (operationResult, error) { return noContentResult(), nil },
		),
	))
	routes := httpAPI.Routes()
	want := "/api/testing/roles/{role_id:" + canonicalIDRoutePattern() + "}"
	require.Len(t, routes, 1, "routes")
	require.Equal(t, want, routes[0].Path, "routes")
	require.Equal(t, AuthSessionRequired, routes[0].Auth, "routes")
	policy := newRouteErrorPolicy(routes[0].ErrorCodes)
	for _, code := range routeErrorCodes(AuthSessionRequired, http.MethodDelete, []string{"resource.not_found"}) {
		_, exists := policy[code]
		require.True(t, exists, "routes = %#v, missing protected-route error %q", routes, code)
	}
}

func TestPrincipalAssuranceRequirements(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_000_000)
	recent := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		ClientType:             model.SessionClientWeb,
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now.Add(-time.Minute),
	}
	strongOld := recent
	strongOld.AuthenticationStrength = model.AuthenticationMultiFactor
	strongOld.AuthenticatedAt = now.Add(-time.Hour)
	strongOld.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Hour))
	strongRecent := strongOld
	strongRecent.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Minute))
	require.NoError(t, requirePrincipalAssurance(
		recent, AuthRecentSessionRequired, now, 15*time.Minute,
	))
	require.Equal(t, "authentication.strong_required", applicationErrorCode(requirePrincipalAssurance(
		recent, AuthStrongSessionRequired, now, 15*time.Minute,
	)), "single-factor strong requirement error")
	require.Equal(t, "authentication.reauthentication_required", applicationErrorCode(requirePrincipalAssurance(
		strongOld, AuthRecentSessionRequired, now, 15*time.Minute,
	)), "old recent requirement error")
	require.NoError(t, requirePrincipalAssurance(
		strongRecent,
		AuthStrongRecentSessionRequired,
		now,
		15*time.Minute,
	))
}

func newRoutingTestAPI(_ string) *API {
	return &API{
		catalog:                 newRouteCatalogBuilder(),
		recentAuthenticationTTL: 15 * time.Minute,
	}
}

func newCompiledRoutingTestAPI(t *testing.T, apiURLSuffix string, resources ...apiResource) *API {
	t.Helper()
	logger, _ := newTestLogger(t)
	httpAPI := &API{
		authenticator:           classRouteAuthenticator{},
		logger:                  logger,
		localizer:               newTestLocalizer(t),
		recentAuthenticationTTL: 15 * time.Minute,
	}
	require.NoError(t, httpAPI.buildRoutingKernel(apiURLSuffix, 1<<20, func() error {
		return httpAPI.collectResources(apiURLSuffix, resources...)
	}))
	return httpAPI
}

const modelIDForRouteTest = "3uuuid3i7bexfcbzmo6s5w4zqo"
