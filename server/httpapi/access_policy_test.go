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
	"github.com/sudosylabs/proctor/server/model"
)

type replayingAccessPolicyHTTPApplication struct {
	command     *app.ReplaceAccessPolicyCommand
	view        app.AccessPolicyView
	discoveries int
}

func (a *replayingAccessPolicyHTTPApplication) GetAccessPolicy(context.Context, app.Invocation) (app.AccessPolicyView, error) {
	return a.view, nil
}
func (a *replayingAccessPolicyHTTPApplication) PreflightAccessPolicy(context.Context, app.Invocation, app.PreflightAccessPolicyCommand) (app.AccessPolicyPreflightResult, error) {
	return app.AccessPolicyPreflightResult{}, nil
}
func (a *replayingAccessPolicyHTTPApplication) DiscoverAccess(context.Context) (app.PublicAccessDiscovery, error) {
	a.discoveries++
	return app.PublicAccessDiscovery{}, nil
}
func (a *replayingAccessPolicyHTTPApplication) ReplaceAccessPolicy(_ context.Context, _ app.Invocation, command app.ReplaceAccessPolicyCommand) (app.AccessPolicyView, error) {
	if a.command == nil {
		stored := command
		a.command = &stored
		return a.view, nil
	}
	if a.command.IdempotencyKey == command.IdempotencyKey &&
		(a.command.ExpectedRevision != command.ExpectedRevision || a.command.RevokeExistingSessions != command.RevokeExistingSessions ||
			a.command.Settings.DesktopAuthorizationEnabled != command.Settings.DesktopAuthorizationEnabled) {
		return app.AccessPolicyView{}, apperror.New("idempotency.conflict")
	}
	return a.view, nil
}

func TestAccessPolicyRoutesDeclarePublicDiscoveryAndStrongRecentMutation(t *testing.T) {
	t.Parallel()
	resource := accessPolicyResource(nil)
	require.Len(t, resource.routes, 4, "routes")
	byMethodPath := map[string]routeDefinition{}
	for _, route := range resource.routes {
		path, _, err := route.path.compile(model.APIURLSuffix)
		require.NoError(t, err)
		byMethodPath[route.method+" "+path] = route
	}
	require.Equal(t, AuthPublic, byMethodPath["GET /api/v1/discovery"].auth)
	require.Equal(t, AuthStrongRecentSessionRequired, byMethodPath["POST /api/v1/access-policy/preflight"].auth)
	replace := byMethodPath["PUT /api/v1/access-policy"]
	require.Equal(t, AuthStrongRecentSessionRequired, replace.auth)
	require.Equal(t, IdempotencyRequired, replace.idempotency)
}

func TestPublicAccessDiscoveryDTOContainsNoPrivatePolicyOrDestinations(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	response := publicAccessDiscoveryResponseFromView(app.PublicAccessDiscovery{
		DiscoveryVersion: 1, CanonicalOrigin: "https://proctor.example.edu", InstallationID: institutionID.String(), Initialized: true,
		Institution:    &app.PublicInstitutionPresentation{ID: institutionID, Name: "northbridge", DisplayName: "Northbridge"},
		PolicyRevision: 4, Capabilities: app.PublicAccessCapabilities{LocalLogin: true, DesktopAuthorization: true},
		Providers:            []model.ExternalAuthenticationProvider{{ID: "campus", DisplayName: "Campus", Type: "oidc"}},
		DesktopAuthorization: app.DesktopAuthorizationCompatibility{Protocol: "proctor-desktop-authorization", MinimumVersion: 1, MaximumVersion: 1},
	})
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	text := string(encoded)
	for _, forbidden := range []string{"provider_admissions", "invitation_local_credential", "client_secret", "redirect", "claims", "recipient"} {
		require.NotContains(t, text, forbidden, "discovery exposed")
	}
	require.Contains(t, text, `"canonical_origin":"https://proctor.example.edu"`)
	require.Contains(t, text, `"id":"campus"`)
}

func TestAccessPolicyHTTPRetriesLostResponseAndRejectsConflictingKeyReuse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), time.Now())
	policy.Revision = 2
	application := &replayingAccessPolicyHTTPApplication{view: app.AccessPolicyView{Policy: policy, History: []*model.AccessPolicyTransition{}, AvailableProviders: []model.ExternalAuthenticationProvider{}}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(application))
	body := `{"expected_revision":1,"revoke_existing_sessions":true,"local_login_enabled":true,"public_registration_enabled":false,"invitation_admission_enabled":true,"invitation_local_credential_enabled":true,"desktop_authorization_enabled":false,"provider_admissions":{}}`
	do := func(candidate string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", strings.NewReader(candidate))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "lost-response")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		return response
	}
	first, replay := do(body), do(body)
	require.Equal(t, http.StatusOK, first.Code, "first")
	require.Equal(t, http.StatusOK, replay.Code, "first")
	require.Equal(t, replay.Body.String(), first.Body.String(), "first")
	conflicting := strings.Replace(body, `"revoke_existing_sessions":true`, `"revoke_existing_sessions":false`, 1)
	response := do(conflicting)
	require.Equal(t, http.StatusConflict, response.Code, "conflict")
	require.Contains(t, response.Body.String(), `"code":"idempotency.conflict"`, "conflict")
}

func TestAccessPolicyHTTPRequiresExplicitSessionRevocationChoice(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	application := &replayingAccessPolicyHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(application))
	for _, candidate := range []string{
		`{"expected_revision":1,"local_login_enabled":true,"public_registration_enabled":false,"invitation_admission_enabled":true,"invitation_local_credential_enabled":true,"desktop_authorization_enabled":false,"provider_admissions":{}}`,
		`{"expected_revision":1,"revoke_existing_sessions":null,"local_login_enabled":true,"public_registration_enabled":false,"invitation_admission_enabled":true,"invitation_local_credential_enabled":true,"desktop_authorization_enabled":false,"provider_admissions":{}}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", strings.NewReader(candidate))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "explicit-choice")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, "response")
		require.Contains(t, response.Body.String(), `"code":"request.invalid"`, "response")
	}
}

func TestAccessPolicyHTTPRequiresEveryCompleteReplacementSetting(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	application := &replayingAccessPolicyHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(application))
	valid := map[string]any{
		"expected_revision": 1, "revoke_existing_sessions": false,
		"local_login_enabled": true, "public_registration_enabled": false,
		"invitation_admission_enabled": true, "invitation_local_credential_enabled": true,
		"desktop_authorization_enabled": true,
		"provider_admissions":           map[string]any{},
	}
	for _, field := range []string{
		"local_login_enabled", "public_registration_enabled", "invitation_admission_enabled",
		"invitation_local_credential_enabled", "desktop_authorization_enabled", "provider_admissions",
	} {
		field := field
		for _, mutation := range []struct {
			name  string
			apply func(map[string]any)
		}{
			{name: "omitted", apply: func(body map[string]any) { delete(body, field) }},
			{name: "null", apply: func(body map[string]any) { body[field] = nil }},
		} {
			mutation := mutation
			t.Run(field+"/"+mutation.name, func(t *testing.T) {
				body := make(map[string]any, len(valid))
				for key, value := range valid {
					body[key] = value
				}
				mutation.apply(body)
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", strings.NewReader(string(encoded)))
				request.Header.Set("Authorization", "Bearer session")
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", "complete-replacement-"+field+"-"+mutation.name)
				response := httptest.NewRecorder()
				httpAPI.ServeHTTP(response, request)
				require.Equal(t, http.StatusBadRequest, response.Code, "response")
				require.Contains(t, response.Body.String(), `"code":"request.invalid"`, "response")
				require.Contains(t, response.Body.String(), `"field":"`+field+`"`, "response")
			})
		}
	}
}
