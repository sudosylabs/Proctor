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

type replayingAccessPolicyHTTPApplication struct {
	command     *application.ReplaceAccessPolicyCommand
	view        application.AccessPolicyView
	discoveries int
}

func (a *replayingAccessPolicyHTTPApplication) GetAccessPolicy(context.Context, application.Invocation) (application.AccessPolicyView, error) {
	return a.view, nil
}
func (a *replayingAccessPolicyHTTPApplication) PreflightAccessPolicy(context.Context, application.Invocation, application.PreflightAccessPolicyCommand) (application.AccessPolicyPreflightResult, error) {
	return application.AccessPolicyPreflightResult{}, nil
}
func (a *replayingAccessPolicyHTTPApplication) DiscoverAccess(context.Context) (application.PublicAccessDiscovery, error) {
	a.discoveries++
	return application.PublicAccessDiscovery{}, nil
}
func (a *replayingAccessPolicyHTTPApplication) ReplaceAccessPolicy(_ context.Context, _ application.Invocation, command application.ReplaceAccessPolicyCommand) (application.AccessPolicyView, error) {
	if a.command == nil {
		stored := command
		a.command = &stored
		return a.view, nil
	}
	if a.command.IdempotencyKey == command.IdempotencyKey &&
		(a.command.ExpectedRevision != command.ExpectedRevision || a.command.RevokeExistingSessions != command.RevokeExistingSessions ||
			a.command.Settings.DesktopAuthorizationEnabled != command.Settings.DesktopAuthorizationEnabled) {
		return application.AccessPolicyView{}, application.NewError("idempotency.conflict")
	}
	return a.view, nil
}

func TestAccessPolicyRoutesDeclarePublicDiscoveryAndStrongRecentMutation(t *testing.T) {
	t.Parallel()
	resource := accessPolicyResource(nil)
	if len(resource.routes) != 4 {
		t.Fatalf("routes = %d", len(resource.routes))
	}
	byMethodPath := map[string]routeDefinition{}
	for _, route := range resource.routes {
		path, _, err := route.path.compile(model.APIURLSuffix)
		if err != nil {
			t.Fatal(err)
		}
		byMethodPath[route.method+" "+path] = route
	}
	if got := byMethodPath["GET /api/v1/discovery"].auth; got != AuthPublic {
		t.Fatalf("discovery auth = %s", got)
	}
	if got := byMethodPath["POST /api/v1/access-policy/preflight"].auth; got != AuthStrongRecentSessionRequired {
		t.Fatalf("preflight auth = %s", got)
	}
	replace := byMethodPath["PUT /api/v1/access-policy"]
	if replace.auth != AuthStrongRecentSessionRequired || replace.idempotency != IdempotencyRequired {
		t.Fatalf("replace route = %#v", replace)
	}
}

func TestPublicAccessDiscoveryDTOContainsNoPrivatePolicyOrDestinations(t *testing.T) {
	t.Parallel()
	institutionID := model.NewInstitutionID()
	response := publicAccessDiscoveryResponseFromView(application.PublicAccessDiscovery{
		DiscoveryVersion: 1, CanonicalOrigin: "https://proctor.example.edu", InstallationID: institutionID.String(), Initialized: true,
		Institution:    &application.PublicInstitutionPresentation{ID: institutionID, Name: "northbridge", DisplayName: "Northbridge"},
		PolicyRevision: 4, Capabilities: application.PublicAccessCapabilities{LocalLogin: true, DesktopAuthorization: true},
		Providers:            []model.ExternalAuthenticationProvider{{Id: "campus", DisplayName: "Campus", Type: "oidc"}},
		DesktopAuthorization: application.DesktopAuthorizationCompatibility{Protocol: "proctor-desktop-authorization", MinimumVersion: 1, MaximumVersion: 1},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"provider_admissions", "invitation_local_credential", "client_secret", "redirect", "claims", "recipient"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("discovery exposed %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"canonical_origin":"https://proctor.example.edu"`) || !strings.Contains(text, `"id":"campus"`) {
		t.Fatalf("discovery = %s", text)
	}
}

func TestAccessPolicyHTTPRetriesLostResponseAndRejectsConflictingKeyReuse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), time.Now())
	policy.Revision = 2
	app := &replayingAccessPolicyHTTPApplication{view: application.AccessPolicyView{Policy: policy, History: []*model.AccessPolicyTransition{}, AvailableProviders: []model.ExternalAuthenticationProvider{}}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(app))
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
	if first, replay := do(body), do(body); first.Code != http.StatusOK || replay.Code != http.StatusOK || first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	conflicting := strings.Replace(body, `"revoke_existing_sessions":true`, `"revoke_existing_sessions":false`, 1)
	if response := do(conflicting); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"idempotency.conflict"`) {
		t.Fatalf("conflict=%d %s", response.Code, response.Body.String())
	}
}

func TestAccessPolicyHTTPRequiresExplicitSessionRevocationChoice(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	app := &replayingAccessPolicyHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(app))
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
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"request.invalid"`) {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
}

func TestAccessPolicyHTTPRequiresEveryCompleteReplacementSetting(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb}
	app := &replayingAccessPolicyHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, accessPolicyResource(app))
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
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", strings.NewReader(string(encoded)))
				request.Header.Set("Authorization", "Bearer session")
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", "complete-replacement-"+field+"-"+mutation.name)
				response := httptest.NewRecorder()
				httpAPI.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"request.invalid"`) ||
					!strings.Contains(response.Body.String(), `"field":"`+field+`"`) {
					t.Fatalf("response=%d %s", response.Code, response.Body.String())
				}
			})
		}
	}
}
