// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

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

func (a *desktopAuthorizationHTTPApplication) BindDesktopAuthorization(_ context.Context, _ application.Invocation, command application.BindDesktopAuthorizationCommand) (*application.DesktopAuthorizationBinding, error) {
	return &application.DesktopAuthorizationBinding{Binding: "binding", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
}

func (a *desktopAuthorizationHTTPApplication) DesktopAuthorizationContext(context.Context, application.Invocation, string) (*application.DesktopAuthorizationBrowserContext, error) {
	return &application.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateBound, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
}

func (a *desktopAuthorizationHTTPApplication) AuthenticateDesktopAuthorizationSession(context.Context, application.Invocation, application.AuthenticateDesktopAuthorizationCommand) (*application.DesktopAuthorizationBrowserContext, error) {
	return &application.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateAuthenticated}, nil
}

func (a *desktopAuthorizationHTTPApplication) AuthenticateDesktopAuthorizationLocally(context.Context, application.Invocation, application.AuthenticateDesktopAuthorizationLocallyCommand) (*application.DesktopAuthorizationBrowserContext, error) {
	return &application.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateAuthenticated}, nil
}

func (a *desktopAuthorizationHTTPApplication) ResetDesktopAuthorizationAccount(context.Context, application.Invocation, string) error {
	return nil
}

func (a *desktopAuthorizationHTTPApplication) BeginDesktopExternalAuthentication(context.Context, application.Invocation, application.BeginDesktopExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error) {
	return &model.ExternalAuthenticationStart{RedirectURL: "https://identity.example/login", Binding: "external-binding", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
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
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, desktopAuthorizationResource(applicationFake, testDesktopAuthorizationCookies(t)))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/authorizations", strings.NewReader(
		`{"callback_url":"http://127.0.0.1:49152/random","state":"state","code_challenge":"challenge"}`))
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
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, desktopAuthorizationResource(applicationFake, testDesktopAuthorizationCookies(t)))

	tests := []struct {
		path, body, authorization string
		wantStatus                int
	}{
		{"/api/v1/auth/desktop/authorizations", `{"callback_url":"http://127.0.0.1:49152/random","state":"state","code_challenge":"challenge","device_id":"device","device_name":"Exam laptop"}`, "", http.StatusCreated},
		{"/api/v1/auth/desktop/authorizations/approve", `{"state":"state"}`, "", http.StatusOK},
		{"/api/v1/auth/desktop/authorizations/cancel", `{"state":"state"}`, "", http.StatusNoContent},
		{"/api/v1/auth/desktop/token", `{"code":"code","state":"state","code_verifier":"verifier"}`, "", http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		if strings.Contains(test.path, "/authorizations/") {
			request.AddCookie(&http.Cookie{Name: BrowserDesktopAuthorizationCookieName, Value: "binding"})
		}
		if test.authorization != "" {
			request.Header.Set("Authorization", test.authorization)
		}
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if response.Code != test.wantStatus || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d cache=%q body=%s", test.path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
	if applicationFake.start.Source != "192.0.2.1:1234" || applicationFake.exchange.Source != "192.0.2.1:1234" ||
		applicationFake.approve.Binding != "binding" || applicationFake.cancel.Binding != "binding" ||
		applicationFake.exchange.CodeVerifier != "verifier" {
		t.Fatalf("mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	}
}

func TestDesktopAuthorizationHTTPExchangesFragmentProofForScopedCookies(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{},
		desktopAuthorizationResource(&desktopAuthorizationHTTPApplication{}, testDesktopAuthorizationCookies(t)))

	bindRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/authorizations/bind", strings.NewReader(
		`{"handle":"handle","browser_proof":"proof","state":"state"}`))
	bindRequest.Header.Set("Content-Type", "application/json")
	bindResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(bindResponse, bindRequest)
	if bindResponse.Code != http.StatusNoContent || bindResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bind = %d cache=%q body=%s", bindResponse.Code, bindResponse.Header().Get("Cache-Control"), bindResponse.Body.String())
	}
	bindingCookie := responseCookie(t, bindResponse.Result().Cookies(), BrowserDesktopAuthorizationCookieName)
	if bindingCookie.Value != "binding" || bindingCookie.Path != "/api/v1/auth/desktop/authorizations" ||
		!bindingCookie.HttpOnly || !bindingCookie.Secure || bindingCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("binding cookie = %#v", bindingCookie)
	}

	providerRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/desktop/authorizations/authenticate/providers/provider/login?state=state", nil)
	providerRequest.AddCookie(bindingCookie)
	providerResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(providerResponse, providerRequest)
	if providerResponse.Code != http.StatusSeeOther || providerResponse.Header().Get("Location") != "https://identity.example/login" ||
		providerResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("provider start = %d location=%q cache=%q body=%s", providerResponse.Code,
			providerResponse.Header().Get("Location"), providerResponse.Header().Get("Cache-Control"), providerResponse.Body.String())
	}
	externalCookie := responseCookie(t, providerResponse.Result().Cookies(), BrowserExternalLoginCookieName)
	if externalCookie.Value != "external-binding" || externalCookie.Path != "/api/v1/auth/providers/" ||
		!externalCookie.HttpOnly || !externalCookie.Secure || externalCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("external binding cookie = %#v", externalCookie)
	}
}

func responseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response cookie %q was not set", name)
	return nil
}

func testDesktopAuthorizationCookies(t *testing.T) browserCookies {
	t.Helper()
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	if err != nil {
		t.Fatal(err)
	}
	return cookies
}
