// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type desktopAuthorizationHTTPApplication struct {
	start    application.StartDesktopAuthorizationCommand
	approve  application.ApproveDesktopAuthorizationCommand
	cancel   application.ApproveDesktopAuthorizationCommand
	exchange application.ExchangeDesktopAuthorizationCommand
	startErr error
}

func (a *desktopAuthorizationHTTPApplication) StartDesktopAuthorization(_ context.Context, _ application.Invocation, command application.StartDesktopAuthorizationCommand) (*application.DesktopAuthorizationStart, error) {
	a.start = command
	if a.startErr != nil {
		return nil, a.startErr
	}
	return &application.DesktopAuthorizationStart{AuthorizationURL: "https://proctor.example.edu/authorize/desktop", ExpiresAt: 100}, nil
}

func TestDesktopAuthorizationHTTPExposesBoundedAttemptFailures(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopAuthorizationHTTPApplication{startErr: application.NewError("authentication.rate_limited")}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, desktopAuthorizationResource(applicationFake))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/authorizations", strings.NewReader(
		`{"callback_url":"http://127.0.0.1:49152/random","state":"state","code_challenge":"challenge","authentication_method":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited start = %d: %s", response.Code, response.Body.String())
	}
}

func (a *desktopAuthorizationHTTPApplication) ApproveDesktopAuthorization(_ context.Context, _ application.Invocation, command application.ApproveDesktopAuthorizationCommand) (*application.DesktopAuthorizationApproval, error) {
	a.approve = command
	return &application.DesktopAuthorizationApproval{RedirectURL: "http://127.0.0.1:49152/callback?code=opaque&state=state", ExpiresAt: 90}, nil
}

func (a *desktopAuthorizationHTTPApplication) CancelDesktopAuthorization(_ context.Context, _ application.Invocation, command application.ApproveDesktopAuthorizationCommand) error {
	a.cancel = command
	return nil
}

func (a *desktopAuthorizationHTTPApplication) ExchangeDesktopAuthorization(_ context.Context, _ application.Invocation, command application.ExchangeDesktopAuthorizationCommand) (*application.DesktopAuthorizationExchangeResult, error) {
	a.exchange = command
	return &application.DesktopAuthorizationExchangeResult{
		Session: &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID(), ClientType: model.SessionClientDesktop},
		Tokens:  &model.AuthenticationTokens{AccessToken: "access", RefreshToken: "refresh"},
	}, nil
}

func TestDesktopAuthorizationHTTPMapsPublicClientProtocolAndDisablesCaching(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopAuthorizationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now()}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, desktopAuthorizationResource(applicationFake))

	tests := []struct {
		path, body, authorization string
		wantStatus                int
	}{
		{"/api/v1/auth/desktop/authorizations", `{"callback_url":"http://127.0.0.1:49152/random","state":"state","code_challenge":"challenge","authentication_method":"oidc","provider_id":"campus","device_id":"device","device_name":"Exam laptop"}`, "", http.StatusCreated},
		{"/api/v1/auth/desktop/authorizations/approve", `{"handle":"handle","browser_proof":"proof","state":"state"}`, "Bearer access", http.StatusOK},
		{"/api/v1/auth/desktop/authorizations/cancel", `{"handle":"handle","browser_proof":"proof","state":"state"}`, "", http.StatusNoContent},
		{"/api/v1/auth/desktop/token", `{"code":"code","state":"state","code_verifier":"verifier"}`, "", http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		if test.authorization != "" {
			request.Header.Set("Authorization", test.authorization)
		}
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if response.Code != test.wantStatus || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d cache=%q body=%s", test.path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
	if applicationFake.start.ProviderID != "campus" || applicationFake.start.AuthenticationMethod != "oidc" ||
		applicationFake.start.Source != "192.0.2.1:1234" || applicationFake.exchange.Source != "192.0.2.1:1234" ||
		applicationFake.approve.Handle != "handle" || applicationFake.cancel.BrowserProof != "proof" ||
		applicationFake.exchange.CodeVerifier != "verifier" {
		t.Fatalf("mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	}
}
