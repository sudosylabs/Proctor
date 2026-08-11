// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoutingKernelRejectsInvalidCatalogs(t *testing.T) {
	t.Parallel()

	validOperation := func(operationRequest) (operationResult, error) {
		return noContentResult(), nil
	}
	tests := []struct {
		name      string
		resources []resource
		wantError string
	}{
		{
			name: "duplicate normalized route",
			resources: []resource{
				newResource("first", principalRoute(http.MethodGet, apiPath(canonicalID("first_id")), nil, validOperation)),
				newResource("second", principalRoute(http.MethodGet, apiPath(canonicalID("second_id")), nil, validOperation)),
			},
			wantError: "duplicate route shape",
		},
		{
			name: "unsupported parameter kind",
			resources: []resource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(pathParameter{name: "id", kind: parameterKind(99)}), nil, validOperation)),
			},
			wantError: "unsupported parameter kind",
		},
		{
			name: "missing operation",
			resources: []resource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(literal("resources")), nil, nil)),
			},
			wantError: "operation is required",
		},
		{
			name: "invalid authentication",
			resources: []resource{
				newResource("invalid", routeDefinition{method: http.MethodGet, path: apiPath(literal("resources")), auth: AuthRequirement("unknown"), operation: validOperation}),
			},
			wantError: "authentication requirement is invalid",
		},
		{
			name: "unmapped public error",
			resources: []resource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(literal("resources")), []string{"not.registered"}, validOperation)),
			},
			wantError: "public error code",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateResourceCatalog(model.APIURLSuffix, test.resources)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateResourceCatalog() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestPublicRoutingKernelOperationDoesNotRequirePrincipal(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource("system", routeDefinition{
			method: http.MethodGet,
			path:   apiPath(literal("system"), literal("ping")),
			auth:   AuthPublic,
			operation: func(request operationRequest) (operationResult, error) {
				if request.principal.UserID.IsValid() {
					t.Fatal("public operation received an invented principal")
				}
				return noContentResult(), nil
			},
		}),
	)

	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/system/ping", nil),
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestRoutingKernelPathVocabularyPreservesV1IdentifierPatterns(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	operation := func(operationRequest) (operationResult, error) {
		return noContentResult(), nil
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource(
			"identifiers",
			routeDefinition{
				method: http.MethodGet,
				path:   apiPath(literal("users"), canonicalID("user_id")),
				auth:   AuthPublic, operation: operation,
			},
			routeDefinition{
				method: http.MethodGet,
				path:   apiPath(literal("auth"), literal("providers"), providerID("provider_id")),
				auth:   AuthPublic, operation: operation,
			},
		),
	)

	want := map[string]bool{
		"/api/v1/users/{user_id:" + canonicalIDRoutePattern() + "}":             false,
		"/api/v1/auth/providers/{provider_id:" + providerIDRoutePattern() + "}": false,
	}
	for _, route := range httpAPI.Routes() {
		if _, exists := want[route.Path]; !exists {
			t.Fatalf("unexpected route path %q", route.Path)
		}
		want[route.Path] = true
	}
	for path, found := range want {
		if !found {
			t.Errorf("route path %q is missing", path)
		}
	}
}

func TestCompiledRoutingKernelRejectsLateLegacyRegistration(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource("system", routeDefinition{
			method: http.MethodGet,
			path:   apiPath(literal("system"), literal("ping")),
			auth:   AuthPublic,
			operation: func(operationRequest) (operationResult, error) {
				return noContentResult(), nil
			},
		}),
	)
	if err := httpAPI.registerLegacyRoute(
		nil,
		"/late",
		http.MethodGet,
		httpAPI.APIHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})),
	); err == nil {
		t.Fatal("compiled route catalog accepted late registration")
	}
}
