// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func TestBaseRoutesCentralizeAPIVersionRegexAndParams(t *testing.T) {
	t.Parallel()

	httpAPI := newRoutingTestAPI("/api/testing")
	var gotRoleID string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		params, ok := RequestParams(request.Context())
		if !ok {
			t.Error("request parameters were not attached")
		} else {
			roleID, appErr := params.RequireRoleId()
			if appErr != nil {
				t.Errorf("RequireRoleId returned %v", appErr)
			}
			gotRoleID = roleID
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	if err := httpAPI.Register(
		httpAPI.BaseRoutes.Role,
		"",
		http.MethodGet,
		httpAPI.APIHandler(handler),
	); err != nil {
		t.Fatal(err)
	}

	valid := httptest.NewRequest(
		http.MethodGet,
		"/api/testing/roles/"+modelIDForRouteTest,
		nil,
	)
	validResponse := httptest.NewRecorder()
	httpAPI.router.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusNoContent || gotRoleID != modelIDForRouteTest {
		t.Fatalf("valid route status/id = %d/%q", validResponse.Code, gotRoleID)
	}

	for _, path := range []string{
		"/api/v1/roles/" + modelIDForRouteTest,
		"/api/testing/roles/not-an-id",
		"/api/testing/roles/3UUUid3i7bexfcbzmo6s5w4zqo",
	} {
		response := httptest.NewRecorder()
		httpAPI.router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.Code)
		}
	}
}

func TestRegisterRejectsMissingPolicyDuplicatePatternAndForeignBase(t *testing.T) {
	t.Parallel()

	httpAPI := newRoutingTestAPI("/api/testing")
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	first := httpAPI.subrouter(
		httpAPI.BaseRoutes.APIRoot,
		"/resources/{id:[a-z]+}",
	)
	second := httpAPI.subrouter(
		httpAPI.BaseRoutes.APIRoot,
		"/resources/{other:[a-z]+}",
	)
	if err := httpAPI.Register(
		first,
		"",
		http.MethodGet,
		httpAPI.APIHandler(handler),
	); err != nil {
		t.Fatal(err)
	}
	if err := httpAPI.Register(
		second,
		"",
		http.MethodGet,
		httpAPI.APIHandler(handler),
	); err == nil {
		t.Fatal("duplicate route shape was accepted")
	}
	if err := httpAPI.Register(
		httpAPI.BaseRoutes.APIRoot,
		"/resources",
		http.MethodPost,
		&Handler{handler: handler},
	); err == nil {
		t.Fatal("route without authentication policy was accepted")
	}
	if err := httpAPI.Register(
		mux.NewRouter(),
		"/resources",
		http.MethodGet,
		httpAPI.APIHandler(handler),
	); err == nil {
		t.Fatal("foreign base router was accepted")
	}
}

func TestRouteMetadataRetainsVersionAndRegexContract(t *testing.T) {
	t.Parallel()

	httpAPI := newRoutingTestAPI("/api/testing")
	if err := httpAPI.Register(
		httpAPI.BaseRoutes.Role,
		"",
		http.MethodDelete,
		httpAPI.APISessionRequired(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		),
	); err != nil {
		t.Fatal(err)
	}
	routes := httpAPI.Routes()
	want := "/api/testing/roles/{role_id:" + canonicalIDRoutePattern() + "}"
	if len(routes) != 1 || routes[0].Path != want {
		t.Fatalf("routes = %#v, want path %q", routes, want)
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

func newRoutingTestAPI(apiURLSuffix string) *API {
	httpAPI := &API{
		routeKeys:               make(map[string]struct{}),
		prefixes:                make(map[*mux.Router]string),
		recentAuthenticationTTL: 15 * time.Minute,
	}
	httpAPI.initializeBaseRoutes(apiURLSuffix)
	return httpAPI
}

const modelIDForRouteTest = "3uuuid3i7bexfcbzmo6s5w4zqo"
