// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

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

type accountSecurityHTTPApplication struct {
	Sessions
	MFA
	PersonalAccessTokens

	createToken application.CreatePersonalAccessTokenCommand
}

func (applicationStub *accountSecurityHTTPApplication) ListSessions(
	context.Context,
	application.Invocation,
	application.ListSessionsQuery,
) ([]*model.Session, error) {
	return []*model.Session{}, nil
}

func (applicationStub *accountSecurityHTTPApplication) GetMFAStatus(
	context.Context,
	application.Invocation,
	application.GetMFAStatusQuery,
) (*application.MFAStatus, error) {
	return &application.MFAStatus{Enabled: true, RecoveryCodesRemaining: 7}, nil
}

func (applicationStub *accountSecurityHTTPApplication) CreatePersonalAccessToken(
	_ context.Context,
	_ application.Invocation,
	command application.CreatePersonalAccessTokenCommand,
) (*model.PersonalAccessTokenCreation, error) {
	applicationStub.createToken = command
	return &model.PersonalAccessTokenCreation{
		Token: &model.PersonalAccessToken{
			ID: model.NewPersonalAccessTokenID(), UserID: model.NewUserID(),
			Scopes: []string{"user.profile_picture.manage"},
		},
		Credential: "one-time-secret",
	}, nil
}

func TestAccountSecurityResourcesRunThroughFocusedKernel(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	now := time.Now()
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewId()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: now, MFACompletedAt: model.OptionalTimeFrom(now),
		ClientType: model.SessionClientWeb,
	}
	applicationStub := &accountSecurityHTTPApplication{}
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		sessionResource(applicationStub, cookies),
		mfaResource(applicationStub),
		personalAccessTokenResource(applicationStub),
	)

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/mfa", nil)
	statusRequest.Header.Set("Authorization", "Bearer credential")
	statusResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || statusResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("MFA status = %d, cache = %q: %s", statusResponse.Code, statusResponse.Header().Get("Cache-Control"), statusResponse.Body.String())
	}

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/users/me/tokens",
		strings.NewReader(`{"description":"editor","scopes":["user.profile_picture.manage"],"expires_at":123}`),
	)
	createRequest.Header.Set("Authorization", "Bearer credential")
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated || createResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token create = %d, cache = %q: %s", createResponse.Code, createResponse.Header().Get("Cache-Control"), createResponse.Body.String())
	}
	if applicationStub.createToken.Description != "editor" || applicationStub.createToken.ExpiresAt != 123 {
		t.Fatalf("token command = %#v", applicationStub.createToken)
	}
	var created map[string]any
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["credential"] != "one-time-secret" {
		t.Fatalf("credential response = %#v", created)
	}

	invalidRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/users/me/tokens",
		strings.NewReader(`{"description":"editor","scopes":[],"expires_at":123,"unknown":true}`),
	)
	invalidRequest.Header.Set("Authorization", "Bearer credential")
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("strict decode status = %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}

	missingCredential := httptest.NewRecorder()
	httpAPI.ServeHTTP(missingCredential, httptest.NewRequest(http.MethodGet, "/api/v1/users/me/sessions", nil))
	if missingCredential.Code != http.StatusUnauthorized {
		t.Fatalf("missing credential status = %d: %s", missingCredential.Code, missingCredential.Body.String())
	}

	weakPrincipal := principal
	weakPrincipal.AuthenticationStrength = model.AuthenticationSingleFactor
	weakPrincipal.MFACompletedAt = model.OptionalTime{}
	weakAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: weakPrincipal},
		mfaResource(applicationStub),
	)
	weakRequest := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/mfa/disable", nil)
	weakRequest.Header.Set("Authorization", "Bearer credential")
	weakResponse := httptest.NewRecorder()
	weakAPI.ServeHTTP(weakResponse, weakRequest)
	if weakResponse.Code != http.StatusForbidden || !strings.Contains(weakResponse.Body.String(), `"code":"authentication.strong_required"`) {
		t.Fatalf("weak assurance response = %d: %s", weakResponse.Code, weakResponse.Body.String())
	}

	stalePrincipal := principal
	stalePrincipal.AuthenticatedAt = now.Add(-time.Hour)
	stalePrincipal.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Hour))
	staleAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: stalePrincipal},
		personalAccessTokenResource(applicationStub),
	)
	staleRequest := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/tokens", strings.NewReader(`{"description":"editor","scopes":[],"expires_at":123}`))
	staleRequest.Header.Set("Authorization", "Bearer credential")
	staleResponse := httptest.NewRecorder()
	staleAPI.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusForbidden || !strings.Contains(staleResponse.Body.String(), `"code":"authentication.reauthentication_required"`) {
		t.Fatalf("stale assurance response = %d: %s", staleResponse.Code, staleResponse.Body.String())
	}
}
