// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
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
			name: "negative route body limit",
			resources: []resource{
				newResource("invalid", routeDefinition{
					method: http.MethodPost, path: apiPath(literal("resources")), auth: AuthPublic,
					maxBodyBytes: -1, operation: validOperation,
				}),
			},
			wantError: "maximum body size",
		},
		{
			name: "idempotent protocol requires principal",
			resources: []resource{
				newResource("invalid", idempotentProtocolRoute(
					IdempotencyRequired, 1024, "streaming-upload", RouteProtocolStreamingUpload,
					AuthPublic, http.MethodPost, apiPath(literal("resources")), nil,
					func(operationRequest) (protocolResult, error) {
						return streamingUploadProtocolResult(http.StatusCreated, struct{}{}), nil
					},
				)),
			},
			wantError: "idempotency requires a principal route",
		},
		{
			name: "idempotent upgrade is forbidden",
			resources: []resource{
				newResource("invalid", idempotentProtocolRoute(
					IdempotencyRequired, 1024, "invalid-upgrade", RouteProtocolUpgrade,
					AuthPrincipalRequired, http.MethodPost, apiPath(literal("resources")), nil,
					func(operationRequest) (protocolResult, error) { return protocolResult{}, nil },
				)),
			},
			wantError: "upgrade requires the dedicated upgrade operation",
		},
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
		{
			name: "invalid protocol kind",
			resources: []resource{
				newResource("invalid", protocolRoute("invalid-protocol", RouteProtocolKind("raw"), AuthPublic, http.MethodGet, apiPath(literal("resources")), nil, func(operationRequest) (protocolResult, error) {
					return protocolResult{}, nil
				})),
			},
			wantError: "protocol operation kind",
		},
		{
			name: "upgrade through bounded protocol operation",
			resources: []resource{
				newResource("invalid", protocolRoute("invalid-upgrade", RouteProtocolUpgrade, AuthPublic, http.MethodGet, apiPath(literal("resources")), nil, func(operationRequest) (protocolResult, error) {
					return protocolResult{}, nil
				})),
			},
			wantError: "upgrade requires the dedicated upgrade operation",
		},
		{
			name: "raw upgrade outside reserved websocket operation",
			resources: []resource{
				newResource("invalid", upgradeRoute("another-upgrade", AuthSessionRequired, http.MethodGet, apiPath(literal("resources")), nil, func(http.ResponseWriter, operationRequest) error {
					return nil
				})),
			},
			wantError: "is not the reserved WebSocket upgrade",
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

func TestRoutingKernelAllowsIdempotentSessionJSONAndStreamingRoutes(t *testing.T) {
	t.Parallel()
	jsonRoute := sessionRoute(http.MethodPost, apiPath(literal("json")), nil,
		func(operationRequest) (operationResult, error) { return noContentResult(), nil })
	jsonRoute.idempotency = IdempotencyRequired
	streamRoute := idempotentProtocolRoute(IdempotencyRequired, 1024, "session-upload", RouteProtocolStreamingUpload,
		AuthSessionRequired, http.MethodPost, apiPath(literal("upload")), nil,
		func(operationRequest) (protocolResult, error) {
			return streamingUploadProtocolResult(http.StatusCreated, struct{}{}), nil
		})
	if err := validateResourceCatalog("/api/v1", []resource{newResource("session-mutations", jsonRoute, streamRoute)}); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingKernelAppliesPerRouteBodyLimitsAndProtocolIdempotency(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	authenticator := classRouteAuthenticator{principal: model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI,
		AuthenticatedAt:        time.Now(),
	}}
	readBody := func(request operationRequest) (protocolResult, error) {
		body, err := io.ReadAll(request.request.Body)
		if err != nil {
			return streamingUploadProtocolResult(http.StatusCreated, map[string]bool{"read": false}), nil
		}
		return streamingUploadProtocolResult(http.StatusCreated, map[string]any{
			"read": true, "bytes": len(body), "idempotency_key": request.idempotencyKey,
		}), nil
	}
	resources := []resource{newResource(
		"uploads",
		idempotentProtocolRoute(
			IdempotencyRequired, 8, "large-streaming-upload", RouteProtocolStreamingUpload,
			AuthPrincipalRequired, http.MethodPost, apiPath(literal("large")), nil, readBody,
		),
		protocolRoute(
			"default-streaming-upload", RouteProtocolStreamingUpload,
			AuthPrincipalRequired, http.MethodPost, apiPath(literal("default")), nil, readBody,
		),
	)}
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := &API{authenticator: authenticator, logger: logger, localizer: newTestLocalizer(t), cookies: cookies, recentAuthenticationTTL: time.Minute}
	if err := httpAPI.buildRoutingKernel(model.APIURLSuffix, 4, func() error {
		return httpAPI.collectResources(model.APIURLSuffix, resources...)
	}); err != nil {
		t.Fatal(err)
	}

	large := httptest.NewRequest(http.MethodPost, "/api/v1/large", strings.NewReader("12345678"))
	large.Header.Set("Authorization", "Bearer test")
	large.Header.Set(idempotencyHeader, "upload-1")
	largeResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(largeResponse, large)
	if largeResponse.Code != http.StatusCreated || !strings.Contains(largeResponse.Body.String(), `"bytes":8`) || !strings.Contains(largeResponse.Body.String(), `"idempotency_key":"upload-1"`) {
		t.Fatalf("large upload = %d %s", largeResponse.Code, largeResponse.Body.String())
	}

	defaultLimited := httptest.NewRequest(http.MethodPost, "/api/v1/default", strings.NewReader("12345"))
	defaultLimited.Header.Set("Authorization", "Bearer test")
	defaultResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(defaultResponse, defaultLimited)
	if defaultResponse.Code != http.StatusCreated || !strings.Contains(defaultResponse.Body.String(), `"read":false`) {
		t.Fatalf("default upload = %d %s", defaultResponse.Code, defaultResponse.Body.String())
	}
}

func TestRoutingKernelExplicitAuthenticationConstructors(t *testing.T) {
	t.Parallel()

	operation := func(operationRequest) (operationResult, error) {
		return noContentResult(), nil
	}
	tests := []struct {
		name string
		got  routeDefinition
		want AuthRequirement
	}{
		{"public", publicRoute(http.MethodGet, apiPath(literal("public")), nil, operation), AuthPublic},
		{"principal", principalRoute(http.MethodGet, apiPath(literal("principal")), nil, operation), AuthPrincipalRequired},
		{"session", sessionRoute(http.MethodGet, apiPath(literal("session")), nil, operation), AuthSessionRequired},
		{"strong session", strongSessionRoute(http.MethodGet, apiPath(literal("strong")), nil, operation), AuthStrongSessionRequired},
		{"recent session", recentSessionRoute(http.MethodGet, apiPath(literal("recent")), nil, operation), AuthRecentSessionRequired},
		{"strong recent session", strongRecentSessionRoute(http.MethodGet, apiPath(literal("strong-recent")), nil, operation), AuthStrongRecentSessionRequired},
		{"refresh credential", refreshCredentialRoute(http.MethodGet, apiPath(literal("refresh")), nil, operation), AuthRefreshCredentialRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got.auth != test.want {
				t.Fatalf("authentication = %q, want %q", test.got.auth, test.want)
			}
		})
	}
}

func TestRoutingKernelAppliesTypedHeadersProblemsAndNamedProtocolOperations(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource(
			"kernel-results",
			publicRoute(
				http.MethodGet,
				apiPath(literal("headers")),
				nil,
				func(operationRequest) (operationResult, error) {
					return jsonResult(http.StatusOK, healthResponse{Status: "ok"}).withHeaders(http.Header{
						"X-Kernel-Test": []string{"preserved"},
					}), nil
				},
			),
			publicRoute(
				http.MethodGet,
				apiPath(literal("unavailable")),
				[]string{"not_ready"},
				func(request operationRequest) (operationResult, error) {
					return problemResult(Problem{
						Type: "https://proctor.sudosylabs.com/problems/not-ready", Title: "Service unavailable",
						Status: http.StatusServiceUnavailable, Detail: "The service is not ready to accept requests.",
						Instance: request.request.URL.Path, Code: "not_ready", RequestID: RequestID(request.context),
					}), nil
				},
			),
			protocolRoute(
				"external-authentication-redirect",
				RouteProtocolRedirect,
				AuthPublic,
				http.MethodGet,
				apiPath(literal("redirect")),
				[]string{"request.invalid"},
				func(request operationRequest) (protocolResult, error) {
					if request.request.URL.Query().Get("fail") != "" {
						return protocolResult{}, invalidRequestError("redirect", errors.New("invalid"))
					}
					return redirectProtocolResult("/destination"), nil
				},
			),
		),
	)

	headers := httptest.NewRecorder()
	httpAPI.ServeHTTP(headers, httptest.NewRequest(http.MethodGet, "/api/v1/headers", nil))
	if headers.Code != http.StatusOK || headers.Header().Get("X-Kernel-Test") != "preserved" {
		t.Fatalf("typed headers response = %d %#v", headers.Code, headers.Header())
	}

	unavailable := httptest.NewRecorder()
	httpAPI.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/api/v1/unavailable", nil))
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("typed problem response = %d %s", unavailable.Code, unavailable.Body.String())
	}

	redirect := httptest.NewRecorder()
	httpAPI.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/api/v1/redirect", nil))
	if redirect.Code != http.StatusSeeOther || redirect.Header().Get("Location") != "/destination" {
		t.Fatalf("protocol redirect = %d %#v", redirect.Code, redirect.Header())
	}

	failure := httptest.NewRecorder()
	httpAPI.ServeHTTP(failure, httptest.NewRequest(http.MethodGet, "/api/v1/redirect?fail=1", nil))
	if failure.Code != http.StatusBadRequest || !strings.Contains(failure.Body.String(), `"code":"request.invalid"`) {
		t.Fatalf("protocol error = %d %s", failure.Code, failure.Body.String())
	}
}

func TestRoutingKernelValidatesResultsBeforeEmittingResponseEffects(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookieHeaders := http.Header{"Set-Cookie": {"unsafe=value"}, "X-Should-Not-Exist": {"unsafe"}}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource(
			"invalid-results",
			publicRoute(http.MethodGet, apiPath(literal("invalid-ordinary")), nil, func(operationRequest) (operationResult, error) {
				return operationResult{status: http.StatusInternalServerError, headers: cookieHeaders}, nil
			}),
			publicRoute(http.MethodGet, apiPath(literal("undeclared-problem")), nil, func(request operationRequest) (operationResult, error) {
				return problemResult(Problem{Status: http.StatusServiceUnavailable, Code: "not_ready", Instance: request.request.URL.Path}).withHeaders(cookieHeaders), nil
			}),
			protocolRoute("mismatched-protocol", RouteProtocolRedirect, AuthPublic, http.MethodGet, apiPath(literal("invalid-protocol")), nil, func(operationRequest) (protocolResult, error) {
				return streamingUploadProtocolResult(http.StatusOK, map[string]string{"status": "wrong"}).withHeaders(cookieHeaders), nil
			}),
			protocolRoute("undeclared-protocol-error", RouteProtocolRedirect, AuthPublic, http.MethodGet, apiPath(literal("invalid-protocol-error")), []string{"request.invalid"}, func(operationRequest) (protocolResult, error) {
				return protocolResult{}, errorWithHeaders(application.NewError("resource.not_found"), cookieHeaders)
			}),
		),
	)

	for _, path := range []string{"invalid-ordinary", "undeclared-problem", "invalid-protocol", "invalid-protocol-error"} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/"+path, nil))
		if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal"`) {
			t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Set-Cookie") != "" || response.Header().Get("X-Should-Not-Exist") != "" {
			t.Fatalf("%s emitted unvalidated effects: %#v", path, response.Header())
		}
	}
}

func TestRoutingKernelBoundsBinaryProtocolAndPublishesProtocolManifest(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		newResource(
			"binary",
			protocolRoute("bounded-binary-download", RouteProtocolBinaryDownload, AuthPublic, http.MethodGet, apiPath(literal("binary")), nil, func(operationRequest) (protocolResult, error) {
				return binaryDownloadProtocolResult(io.NopCloser(strings.NewReader("abcdef")), 3).withHeaders(http.Header{"Content-Type": {"application/octet-stream"}}), nil
			}),
		),
	)

	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/binary", nil))
	if response.Code != http.StatusOK || response.Body.String() != "abc" || response.Header().Get("Content-Length") != "3" {
		t.Fatalf("bounded binary response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	}
	routes := httpAPI.Routes()
	if len(routes) != 1 || routes[0].ProtocolName != "bounded-binary-download" || routes[0].ProtocolKind != RouteProtocolBinaryDownload {
		t.Fatalf("protocol manifest = %#v", routes)
	}
}

func TestRoutesReturnsDefensiveDeepCopy(t *testing.T) {
	t.Parallel()

	httpAPI := newCompiledRoutingTestAPI(t, "/api/testing", newResource("copy",
		sessionRoute(
			http.MethodGet,
			apiPath(literal("copy")),
			[]string{"authentication.required"},
			func(operationRequest) (operationResult, error) { return noContentResult(), nil },
		),
	))
	routes := httpAPI.Routes()
	routes[0].Path = "/mutated"
	routes[0].ErrorCodes[0] = "mutated"

	fresh := httpAPI.Routes()
	if fresh[0].Path == "/mutated" || fresh[0].ErrorCodes[0] == "mutated" {
		t.Fatalf("Routes() exposed mutable catalog state: %#v", fresh[0])
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
			publicRoute(
				http.MethodGet,
				rootPath(literal("health"), literal("live")),
				nil,
				operation,
			),
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
		"/health/live": false,
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
