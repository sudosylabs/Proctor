// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type desktopCompatibilityHTTPApplication struct {
	query          application.DesktopCompatibilityQuery
	evaluations    int
	evaluation     application.DesktopCompatibilityResult
	evaluationErr  error
	policy         *model.DesktopCompatibilityPolicy
	replaceCommand application.ReplaceDesktopCompatibilityPolicyCommand
}

func (a *desktopCompatibilityHTTPApplication) EvaluateDesktopCompatibility(
	_ context.Context,
	query application.DesktopCompatibilityQuery,
) (application.DesktopCompatibilityResult, error) {
	a.query = query
	a.evaluations++
	return a.evaluation, a.evaluationErr
}

func (a *desktopCompatibilityHTTPApplication) GetDesktopCompatibilityPolicy(
	context.Context,
	application.Invocation,
) (*model.DesktopCompatibilityPolicy, error) {
	return a.policy.Clone(), nil
}

func (a *desktopCompatibilityHTTPApplication) ReplaceDesktopCompatibilityPolicy(
	_ context.Context,
	_ application.Invocation,
	command application.ReplaceDesktopCompatibilityPolicyCommand,
) (*model.DesktopCompatibilityPolicy, error) {
	a.replaceCommand = command
	return a.policy.Clone(), nil
}

func TestDesktopCompatibilityRoutesDeclarePublicPingAndProtectedPolicyMutation(t *testing.T) {
	t.Parallel()
	resources := []resource{
		systemResourceWithApplication(systemHTTPHealth{}, BuildInfo{}, nil, time.Now),
		desktopCompatibilityPolicyResource(nil),
	}
	byMethodPath := map[string]routeDefinition{}
	for _, resource := range resources {
		for _, route := range resource.routes {
			path, _, err := route.path.compile(model.APIURLSuffix)
			if err != nil {
				t.Fatal(err)
			}
			byMethodPath[route.method+" "+path] = route
		}
	}
	if route := byMethodPath["GET /api/v1/system/ping"]; route.auth != AuthPublic {
		t.Fatalf("ping route = %#v", route)
	}
	if route := byMethodPath["GET /api/v1/desktop-compatibility-policy"]; route.auth != AuthPrincipalRequired {
		t.Fatalf("policy read route = %#v", route)
	}
	if route := byMethodPath["PUT /api/v1/desktop-compatibility-policy"]; route.auth != AuthStrongRecentSessionRequired || route.idempotency != IdempotencyRequired {
		t.Fatalf("policy replacement route = %#v", route)
	}
}

func TestSystemPingReturnsRequestSpecificNoStoreCompatibility(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	now := time.Date(2026, time.August, 28, 10, 11, 12, 345_000_000, time.UTC)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: application.DesktopCompatibilityResult{
			Availability:            model.DesktopAvailabilityReady,
			Compatibility:           application.DesktopCompatibilityCompatible,
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
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if applicationFake.query.DesktopBuildID != "darwin-42" || applicationFake.query.RealtimeProtocol != 2 {
		t.Fatalf("query = %#v", applicationFake.query)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schema_version"] != float64(1) || body["server_time"] != now.Format(time.RFC3339Nano) ||
		body["availability"] != "ready" || body["reason"] != "ready" ||
		body["compatibility"] != "compatible" || body["compatibility_reason"] != "compatible" ||
		body["minimum_desktop_release"] != "1.2.0" || body["maximum_desktop_release"] != "1.4.0" {
		t.Fatalf("ping body = %#v", body)
	}
	for _, forbidden := range []string{"canonical_origin", "installation", "institution", "provider", "access_policy", "version", "commit", "download"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("ping exposed %q: %#v", forbidden, body)
		}
	}
}

func TestSystemPingPublishesMaintenanceAvailabilityAndRetryTime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	retryAt := time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC)
	serverTime := retryAt.Add(-time.Minute)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: application.DesktopCompatibilityResult{
			Availability:         model.DesktopAvailabilityMaintenance,
			RetryAt:              model.OptionalTimeFrom(retryAt),
			Compatibility:        application.DesktopCompatibilityCompatible,
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
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("maintenance ping = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["availability"] != "maintenance" || body["reason"] != "maintenance" ||
		body["retry_at"] != retryAt.Format(time.RFC3339Nano) ||
		body["administrator_message"] != "Scheduled maintenance is in progress." {
		t.Fatalf("maintenance ping body = %#v", body)
	}
}

func TestSystemPingOmitsRetryTimeNotLaterThanItsServerTime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	serverTime := time.Date(2026, time.August, 28, 11, 0, 0, 0, time.UTC)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluation: application.DesktopCompatibilityResult{
			Availability:  model.DesktopAvailabilityMaintenance,
			RetryAt:       model.OptionalTimeFrom(serverTime),
			Compatibility: application.DesktopCompatibilityCompatible,
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
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["server_time"] != serverTime.Format(time.RFC3339Nano) {
		t.Fatalf("server_time = %#v, want %s", body["server_time"], serverTime.Format(time.RFC3339Nano))
	}
	if retryAt, exists := body["retry_at"]; exists {
		t.Fatalf("ping exposed non-future retry_at = %#v", retryAt)
	}
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
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"request.invalid"`) {
			t.Fatalf("%s = %d %s", target, response.Code, response.Body.String())
		}
	}
	if applicationFake.evaluations != 0 {
		t.Fatalf("malformed requests evaluated compatibility %d times", applicationFake.evaluations)
	}
}

func TestSystemPingFailsClosedInsideSuccessfulResponseWhenUnavailable(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopCompatibilityHTTPApplication{
		evaluationErr: application.NewError("desktop_compatibility_policy.unavailable"),
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
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(response.Body.String(), `"availability":"temporarily_unavailable"`) ||
		!strings.Contains(response.Body.String(), `"compatibility":"server_incompatible"`) ||
		strings.Contains(response.Body.String(), "desktop_compatibility_policy.unavailable") {
		t.Fatalf("unavailable ping = %d %s", response.Code, response.Body.String())
	}
}

func TestDesktopCompatibilityPolicyHTTPUsesCompleteReplacementAndNoStore(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
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
		strings.NewReader(`{"expected_revision":1,"minimum_desktop_release":"1.2.0","revoked_desktop_build_ids":["build-7"],"administrator_message":"Update before the exam.","availability":"ready","retry_at":null}`),
	)
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "desktop-policy-1")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("replace = %d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if applicationFake.replaceCommand.ExpectedRevision != 1 ||
		applicationFake.replaceCommand.IdempotencyKey != "desktop-policy-1" ||
		applicationFake.replaceCommand.Settings.MinimumDesktopRelease != "1.2.0" ||
		len(applicationFake.replaceCommand.Settings.RevokedDesktopBuildIDs) != 1 ||
		applicationFake.replaceCommand.Settings.Availability != model.DesktopAvailabilityReady ||
		applicationFake.replaceCommand.Settings.RetryAt.Valid {
		t.Fatalf("replacement command = %#v", applicationFake.replaceCommand)
	}

	for _, invalidBody := range []string{
		`{"expected_revision":1,"revoked_desktop_build_ids":[],"administrator_message":""}`,
		`{"expected_revision":1,"minimum_desktop_release":"","administrator_message":""}`,
		`{"expected_revision":1,"minimum_desktop_release":"","revoked_desktop_build_ids":[]}`,
		`{"expected_revision":1,"minimum_desktop_release":"","revoked_desktop_build_ids":[],"administrator_message":"","availability":"ready"}`,
	} {
		invalid := httptest.NewRequest(http.MethodPut, "/api/v1/desktop-compatibility-policy", strings.NewReader(invalidBody))
		invalid.Header.Set("Authorization", "Bearer session")
		invalid.Header.Set("Content-Type", "application/json")
		invalid.Header.Set("Idempotency-Key", "desktop-policy-invalid")
		invalidResponse := httptest.NewRecorder()
		httpAPI.ServeHTTP(invalidResponse, invalid)
		if invalidResponse.Code != http.StatusBadRequest ||
			!strings.Contains(invalidResponse.Body.String(), `"code":"request.invalid"`) {
			t.Fatalf("incomplete body = %d %s", invalidResponse.Code, invalidResponse.Body.String())
		}
	}
}
