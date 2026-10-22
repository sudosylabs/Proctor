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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestRoutingKernelRejectsInvalidCatalogs(t *testing.T) {
	t.Parallel()

	validOperation := func(operationRequest) (operationResult, error) {
		return noContentResult(), nil
	}
	tests := []struct {
		name      string
		resources []apiResource
		wantError string
	}{
		{
			name: "negative route body limit",
			resources: []apiResource{
				newResource("invalid", routeDefinition{
					method: http.MethodPost, path: apiPath(literal("resources")), auth: AuthPublic,
					maxBodyBytes: -1, operation: validOperation,
				}),
			},
			wantError: "maximum body size",
		},
		{
			name: "idempotent protocol requires principal",
			resources: []apiResource{
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
			resources: []apiResource{
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
			resources: []apiResource{
				newResource("first", principalRoute(http.MethodGet, apiPath(canonicalID("first_id")), nil, validOperation)),
				newResource("second", principalRoute(http.MethodGet, apiPath(canonicalID("second_id")), nil, validOperation)),
			},
			wantError: "duplicate route shape",
		},
		{
			name: "unsupported parameter kind",
			resources: []apiResource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(pathParameter{name: "id", kind: parameterKind(99)}), nil, validOperation)),
			},
			wantError: "unsupported parameter kind",
		},
		{
			name: "missing operation",
			resources: []apiResource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(literal("resources")), nil, nil)),
			},
			wantError: "operation is required",
		},
		{
			name: "invalid authentication",
			resources: []apiResource{
				newResource("invalid", routeDefinition{method: http.MethodGet, path: apiPath(literal("resources")), auth: AuthRequirement("unknown"), operation: validOperation}),
			},
			wantError: "authentication requirement is invalid",
		},
		{
			name: "unmapped public error",
			resources: []apiResource{
				newResource("invalid", principalRoute(http.MethodGet, apiPath(literal("resources")), []string{"not.registered"}, validOperation)),
			},
			wantError: "public error code",
		},
		{
			name: "invalid protocol kind",
			resources: []apiResource{
				newResource("invalid", protocolRoute("invalid-protocol", RouteProtocolKind("raw"), AuthPublic, http.MethodGet, apiPath(literal("resources")), nil, func(operationRequest) (protocolResult, error) {
					return protocolResult{}, nil
				})),
			},
			wantError: "protocol operation kind",
		},
		{
			name: "upgrade through bounded protocol operation",
			resources: []apiResource{
				newResource("invalid", protocolRoute("invalid-upgrade", RouteProtocolUpgrade, AuthPublic, http.MethodGet, apiPath(literal("resources")), nil, func(operationRequest) (protocolResult, error) {
					return protocolResult{}, nil
				})),
			},
			wantError: "upgrade requires the dedicated upgrade operation",
		},
		{
			name: "raw upgrade outside reserved websocket operation",
			resources: []apiResource{
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
			require.Error(t, err, "validateResourceCatalog() error")
			require.Contains(t, err.Error(), test.wantError, "validateResourceCatalog() error")
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
	require.NoError(t, validateResourceCatalog("/api/v1", []apiResource{newResource("session-mutations", jsonRoute, streamRoute)}))
}

func TestRoutingKernelAppliesPerRouteBodyLimitsAndProtocolIdempotency(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	authenticator := classRouteAuthenticator{principal: model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
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
	resources := []apiResource{newResource(
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
	require.NoError(t, err)
	httpAPI := &API{authenticator: authenticator, logger: logger, localizer: newTestLocalizer(t), cookies: cookies, recentAuthenticationTTL: time.Minute}
	require.NoError(t, httpAPI.buildRoutingKernel(model.APIURLSuffix, 4, func() error {
		return httpAPI.collectResources(model.APIURLSuffix, resources...)
	}))

	large := httptest.NewRequest(http.MethodPost, "/api/v1/large", strings.NewReader("12345678"))
	large.Header.Set("Authorization", "Bearer test")
	large.Header.Set(idempotencyHeader, "upload-1")
	largeResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(largeResponse, large)
	require.Equal(t, http.StatusCreated, largeResponse.Code, "large upload")
	require.Contains(t, largeResponse.Body.String(), `"bytes":8`, "large upload")
	require.Contains(t, largeResponse.Body.String(), `"idempotency_key":"upload-1"`, "large upload")

	// A declared Content-Length over the limit is refused before the
	// operation runs.
	defaultLimited := httptest.NewRequest(http.MethodPost, "/api/v1/default", strings.NewReader("12345"))
	defaultLimited.Header.Set("Authorization", "Bearer test")
	defaultResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(defaultResponse, defaultLimited)
	require.Equal(t, http.StatusRequestEntityTooLarge, defaultResponse.Code, "default upload")
	require.Contains(t, defaultResponse.Body.String(), `"code":"request.too_large"`, "default upload")
	require.Equal(t, "application/problem+json", defaultResponse.Header().Get("Content-Type"), "default upload")

	// A body of unknown length still stops at the limit while streaming.
	streamed := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/default", io.MultiReader(strings.NewReader("12345")))
	streamed.ContentLength = -1
	streamed.Header.Set("Authorization", "Bearer test")
	streamedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(streamedResponse, streamed)
	require.Equal(t, http.StatusCreated, streamedResponse.Code, "streamed default upload")
	require.Contains(t, streamedResponse.Body.String(), `"read":false`, "streamed default upload")
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
			require.Equal(t, test.want, test.got.auth, "authentication")
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
				[]string{"health.not_ready"},
				func(request operationRequest) (operationResult, error) {
					return problemResult(Problem{
						Type: "https://proctor.sudosylabs.com/problems/health.not_ready", Title: "Service unavailable",
						Status: http.StatusServiceUnavailable, Detail: "The service is not ready to accept requests.",
						Instance: request.request.URL.Path, Code: "health.not_ready", RequestID: RequestID(request.context),
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
						return protocolResult{}, invalidRequestError(errors.New("invalid"))
					}
					return redirectProtocolResult("/destination"), nil
				},
			),
		),
	)

	headers := httptest.NewRecorder()
	httpAPI.ServeHTTP(headers, httptest.NewRequest(http.MethodGet, "/api/v1/headers", nil))
	require.Equal(t, http.StatusOK, headers.Code, "typed headers response = %d %#v", headers.Code, headers.Header())
	require.Equal(t, "preserved", headers.Header().Get("X-Kernel-Test"), "typed headers response = %d %#v", headers.Code, headers.Header())

	unavailable := httptest.NewRecorder()
	httpAPI.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/api/v1/unavailable", nil))
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code, "typed problem response")
	require.Contains(t, unavailable.Body.String(), `"code":"health.not_ready"`, "typed problem response")

	redirect := httptest.NewRecorder()
	httpAPI.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/api/v1/redirect", nil))
	require.Equal(t, http.StatusSeeOther, redirect.Code, "protocol redirect = %d %#v", redirect.Code, redirect.Header())
	require.Equal(t, "/destination", redirect.Header().Get("Location"), "protocol redirect = %d %#v", redirect.Code, redirect.Header())

	failure := httptest.NewRecorder()
	httpAPI.ServeHTTP(failure, httptest.NewRequest(http.MethodGet, "/api/v1/redirect?fail=1", nil))
	require.Equal(t, http.StatusBadRequest, failure.Code, "protocol error")
	require.Contains(t, failure.Body.String(), `"code":"request.invalid"`, "protocol error")
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
				return problemResult(Problem{Status: http.StatusServiceUnavailable, Code: "health.not_ready", Instance: request.request.URL.Path}).withHeaders(cookieHeaders), nil
			}),
			protocolRoute("mismatched-protocol", RouteProtocolRedirect, AuthPublic, http.MethodGet, apiPath(literal("invalid-protocol")), nil, func(operationRequest) (protocolResult, error) {
				return streamingUploadProtocolResult(http.StatusOK, map[string]string{"status": "wrong"}).withHeaders(cookieHeaders), nil
			}),
			protocolRoute("undeclared-protocol-error", RouteProtocolRedirect, AuthPublic, http.MethodGet, apiPath(literal("invalid-protocol-error")), []string{"request.invalid"}, func(operationRequest) (protocolResult, error) {
				return protocolResult{}, errorWithHeaders(apperror.New("resource.not_found"), cookieHeaders)
			}),
		),
	)

	for _, path := range []string{"invalid-ordinary", "undeclared-problem", "invalid-protocol", "invalid-protocol-error"} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/"+path, nil))
		require.Equal(t, http.StatusInternalServerError, response.Code, "%s response", path)
		require.Contains(t, response.Body.String(), `"code":"server.internal"`, "%s response", path)
		require.Empty(t, response.Header().Get("Set-Cookie"), "%s emitted unvalidated effects: %#v", path, response.Header())
		require.Empty(t, response.Header().Get("X-Should-Not-Exist"), "%s emitted unvalidated effects: %#v", path, response.Header())
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
	require.Equal(t, http.StatusOK, response.Code, "bounded binary response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	require.Equal(t, "abc", response.Body.String(), "bounded binary response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	require.Equal(t, "3", response.Header().Get("Content-Length"), "bounded binary response = %d %q %#v", response.Code, response.Body.String(), response.Header())
	routes := httpAPI.Routes()
	require.Len(t, routes, 1)
	require.Equal(t, "bounded-binary-download", routes[0].ProtocolName)
	require.Equal(t, RouteProtocolBinaryDownload, routes[0].ProtocolKind)
}

func TestRoutesReturnsDefensiveDeepCopy(t *testing.T) {
	t.Parallel()

	httpAPI := newCompiledRoutingTestAPI(t, "/api/testing", newResource("copy",
		sessionRoute(
			http.MethodGet,
			apiPath(literal("copy")),
			[]string{"resource.not_found"},
			func(operationRequest) (operationResult, error) { return noContentResult(), nil },
		),
	))
	routes := httpAPI.Routes()
	routes[0].Path = "/mutated"
	routes[0].ErrorCodes[0] = "mutated"

	fresh := httpAPI.Routes()
	require.NotEqual(t, "/mutated", fresh[0].Path)
	require.NotEqual(t, "mutated", fresh[0].ErrorCodes[0])
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
				require.False(t, request.principal.UserID.IsValid(), "public operation received an invented principal")
				return noContentResult(), nil
			},
		}),
	)

	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/system/ping", nil),
	)
	require.Equal(t, http.StatusNoContent, response.Code, "status = %d: %s", response.Code, response.Body.String())
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
		_, exists := want[route.Path]
		require.True(t, exists, "unexpected route path %q", route.Path)
		want[route.Path] = true
	}
	for path, found := range want {
		assert.True(t, found, "route path %q is missing", path)
	}
}
