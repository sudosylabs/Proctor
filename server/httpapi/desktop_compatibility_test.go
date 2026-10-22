// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type desktopCompatibilityHTTPApplication struct {
	query          app.DesktopCompatibilityQuery
	evaluations    int
	evaluation     app.DesktopCompatibilityResult
	evaluationErr  error
	policy         *model.DesktopCompatibilityPolicy
	replaceCommand app.ReplaceDesktopCompatibilityPolicyCommand
}

func (a *desktopCompatibilityHTTPApplication) EvaluateDesktopCompatibility(
	_ context.Context,
	query app.DesktopCompatibilityQuery,
) (app.DesktopCompatibilityResult, error) {
	a.query = query
	a.evaluations++
	return a.evaluation, a.evaluationErr
}

func (a *desktopCompatibilityHTTPApplication) GetDesktopCompatibilityPolicy(
	context.Context,
	invocation.Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	return a.policy.Clone(), nil
}

func (a *desktopCompatibilityHTTPApplication) ReplaceDesktopCompatibilityPolicy(
	_ context.Context,
	_ invocation.Invocation,
	command app.ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	a.replaceCommand = command
	return a.policy.Clone(), nil
}

func TestDesktopCompatibilityRoutesDeclarePublicPingAndProtectedPolicyMutation(t *testing.T) {
	t.Parallel()
	resources := []apiResource{
		systemResourceWithApplication(systemHTTPHealth{}, BuildInfo{}, nil, time.Now),
		desktopCompatibilityPolicyResource(nil),
	}
	byMethodPath := map[string]routeDefinition{}
	for _, resource := range resources {
		for _, route := range resource.routes {
			path, _, err := route.path.compile(model.APIURLSuffix)
			require.NoError(t, err)
			byMethodPath[route.method+" "+path] = route
		}
	}
	require.Equal(t, AuthPublic, byMethodPath["GET /api/v1/system/ping"].auth)
	require.Equal(t, AuthPrincipalRequired, byMethodPath["GET /api/v1/desktop-compatibility-policy"].auth)
	route := byMethodPath["PUT /api/v1/desktop-compatibility-policy"]
	require.Equal(t, AuthStrongRecentSessionRequired, route.auth)
	require.Equal(t, IdempotencyRequired, route.idempotency)
}

func TestSystemPingReturnsRequestSpecificNoStoreCompatibility(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	now := time.Date(2026, time.August, 28, 10, 11, 12, 345_000_000, time.UTC)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: app.DesktopCompatibilityResult{
			Availability:            model.DesktopAvailabilityReady,
			Compatibility:           app.DesktopCompatibilityCompatible,
			Reason:                  "compatible",
			MinimumDesktopRelease:   "1.2.0",
			MaximumDesktopRelease:   "1.4.0",
			MinimumRealtimeProtocol: 1,
			MaximumRealtimeProtocol: 2,
			AdministratorMessage:    "Scheduled maintenance is complete.",
		},
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResourceWithApplication(
			systemHTTPHealth{live: true, ready: true},
			BuildInfo{Version: "server-secret-not-for-ping"},
			applicationFake,
			func() time.Time { return now },
		),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/ping?desktop_release=1.4.0&desktop_build_id=darwin-42&platform=darwin&architecture=arm64&realtime_protocol=2",
		nil,
	)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "darwin-42", applicationFake.query.DesktopBuildID)
	require.Equal(t, 2, applicationFake.query.RealtimeProtocol)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, float64(1), body["schema_version"])
	require.Equal(t, now.Format(time.RFC3339Nano), body["server_time"])
	require.Equal(t, "ready", body["availability"])
	require.Equal(t, "ready", body["reason"])
	require.Equal(t, "compatible", body["compatibility"])
	require.Equal(t, "compatible", body["compatibility_reason"])
	require.Equal(t, "1.2.0", body["minimum_desktop_release"])
	require.Equal(t, "1.4.0", body["maximum_desktop_release"])
	for _, forbidden := range []string{"canonical_origin", "installation", "institution", "provider", "access_policy", "version", "commit", "download"} {
		_, exists := body[forbidden]
		require.False(t, exists, "ping exposed %q: %#v", forbidden, body)
	}
}

func TestSystemPingPublishesMaintenanceAvailabilityAndRetryTime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	retryAt := time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC)
	serverTime := retryAt.Add(-time.Minute)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: app.DesktopCompatibilityResult{
			Availability:         model.DesktopAvailabilityMaintenance,
			RetryAt:              model.OptionalTimeFrom(retryAt),
			Compatibility:        app.DesktopCompatibilityCompatible,
			Reason:               "compatible",
			AdministratorMessage: "Scheduled maintenance is in progress.",
		},
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResourceWithApplication(systemHTTPHealth{live: true, ready: true}, BuildInfo{}, applicationFake, func() time.Time { return serverTime }),
	)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=1",
		nil,
	))
	require.Equal(t, http.StatusOK, response.Code, "maintenance ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "maintenance ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "maintenance", body["availability"])
	require.Equal(t, "maintenance", body["reason"])
	require.Equal(t, retryAt.Format(time.RFC3339Nano), body["retry_at"])
	require.Equal(t, "Scheduled maintenance is in progress.", body["administrator_message"])
}

func TestSystemPingOmitsRetryTimeNotLaterThanItsServerTime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	serverTime := time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: app.DesktopCompatibilityResult{
			Availability:  model.DesktopAvailabilityMaintenance,
			RetryAt:       model.OptionalTimeFrom(serverTime),
			Compatibility: app.DesktopCompatibilityCompatible,
			Reason:        "compatible",
		},
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResourceWithApplication(systemHTTPHealth{live: true, ready: true}, BuildInfo{}, applicationFake, func() time.Time { return serverTime }),
	)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=1",
		nil,
	))
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, serverTime.Format(time.RFC3339Nano), body["server_time"], "server_time")
	retryAt, exists := body["retry_at"]
	require.False(t, exists, "ping exposed non-future retry_at = %#v", retryAt)
}

func TestSystemPingRejectsMalformedSelectorsWithoutEvaluation(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopCompatibilityHTTPApplication{}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResourceWithApplication(systemHTTPHealth{live: true, ready: true}, BuildInfo{}, applicationFake, time.Now),
	)
	for _, target := range []string{
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64",
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=0",
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=1&extra=true",
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_release=1.1.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=1",
	} {
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		require.Equal(t, http.StatusBadRequest, response.Code, "%s", target)
		require.Contains(t, response.Body.String(), `"code":"request.invalid"`, "%s", target)
	}
	require.Equal(t, 0, applicationFake.evaluations, "malformed requests evaluated compatibility")
}

func TestSystemPingFailsClosedInsideSuccessfulResponseWhenUnavailable(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluationErr: apperror.New("desktop_compatibility_policy.unavailable"),
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{},
		systemResourceWithApplication(systemHTTPHealth{live: true}, BuildInfo{}, applicationFake, time.Now),
	)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/ping?desktop_release=1.0.0&desktop_build_id=build&platform=darwin&architecture=arm64&realtime_protocol=1",
		nil,
	))
	require.Equal(t, http.StatusOK, response.Code, "unavailable ping")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "unavailable ping")
	require.Contains(t, response.Body.String(), `"availability":"temporarily_unavailable"`, "unavailable ping")
	require.Contains(t, response.Body.String(), `"compatibility":"server_incompatible"`, "unavailable ping")
	require.NotContains(t, response.Body.String(), "desktop_compatibility_policy.unavailable", "unavailable ping")
}

func TestDesktopCompatibilityPolicyHTTPUsesCompleteReplacementAndNoStore(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt:        time.Now(),
		MFACompletedAt:         model.OptionalTimeFrom(time.Now()),
		ClientType:             model.SessionClientWeb,
	}
	policy := model.NewInitialDesktopCompatibilityPolicy(model.NewInstitutionID(), time.Now())
	applicationFake := &desktopCompatibilityHTTPApplication{policy: policy}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		desktopCompatibilityPolicyResource(applicationFake),
	)
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/desktop-compatibility-policy",
		strings.NewReader(`{"expected_revision":1,"minimum_desktop_release":"1.2.0","revoked_desktop_build_ids":["build-7"],"administrator_message":"Update before the exam.","availability":"ready"}`),
	)
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "desktop-policy-1")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "replace = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "replace = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, int64(1), applicationFake.replaceCommand.ExpectedRevision)
	require.Equal(t, "desktop-policy-1", applicationFake.replaceCommand.IdempotencyKey)
	require.Equal(t, "1.2.0", applicationFake.replaceCommand.Settings.MinimumDesktopRelease)
	require.Len(t, applicationFake.replaceCommand.Settings.RevokedDesktopBuildIDs, 1)
	require.Equal(t, model.DesktopAvailabilityReady, applicationFake.replaceCommand.Settings.Availability)
	require.False(t, applicationFake.replaceCommand.Settings.RetryAt.Valid)

	for _, invalidBody := range []string{
		`{"expected_revision":1,"revoked_desktop_build_ids":[],"administrator_message":""}`,
		`{"expected_revision":1,"minimum_desktop_release":"","administrator_message":""}`,
		`{"expected_revision":1,"minimum_desktop_release":"","revoked_desktop_build_ids":[]}`,
		`{"expected_revision":1,"minimum_desktop_release":"","revoked_desktop_build_ids":[],"administrator_message":"","availability":"ready","retry_at":null}`,
	} {
		invalid := httptest.NewRequest(http.MethodPut, "/api/v1/desktop-compatibility-policy", strings.NewReader(invalidBody))
		invalid.Header.Set("Authorization", "Bearer session")
		invalid.Header.Set("Content-Type", "application/json")
		invalid.Header.Set("Idempotency-Key", "desktop-policy-invalid")
		invalidResponse := httptest.NewRecorder()
		httpAPI.ServeHTTP(invalidResponse, invalid)
		require.Equal(t, http.StatusBadRequest, invalidResponse.Code, "incomplete body")
		require.Contains(t, invalidResponse.Body.String(), `"code":"request.invalid"`, "incomplete body")
	}
}
