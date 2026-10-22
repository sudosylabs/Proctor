// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"fmt"
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

type desktopAuthorizationHTTPApplication struct {
	start    app.StartDesktopAuthorizationCommand
	approve  app.ApproveDesktopAuthorizationCommand
	cancel   app.ApproveDesktopAuthorizationCommand
	exchange app.ExchangeDesktopAuthorizationCommand
	startErr error
}

func (a *desktopAuthorizationHTTPApplication) BindDesktopAuthorization(_ context.Context, _ invocation.Invocation, command app.BindDesktopAuthorizationCommand) (*app.DesktopAuthorizationBinding, error) {
	return &app.DesktopAuthorizationBinding{Binding: "binding", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
}

func (a *desktopAuthorizationHTTPApplication) DesktopAuthorizationContext(context.Context, invocation.Invocation, string) (*app.DesktopAuthorizationBrowserContext, error) {
	return &app.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateBound, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
}

func (a *desktopAuthorizationHTTPApplication) AuthenticateDesktopAuthorizationSession(context.Context, invocation.Invocation, app.AuthenticateDesktopAuthorizationCommand) (*app.DesktopAuthorizationBrowserContext, error) {
	return &app.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateAuthenticated}, nil
}

func (a *desktopAuthorizationHTTPApplication) AuthenticateDesktopAuthorizationLocally(context.Context, invocation.Invocation, app.AuthenticateDesktopAuthorizationLocallyCommand) (*app.DesktopAuthorizationBrowserContext, error) {
	return &app.DesktopAuthorizationBrowserContext{State: model.BrowserAuthenticationStateAuthenticated}, nil
}

func (a *desktopAuthorizationHTTPApplication) ResetDesktopAuthorizationAccount(context.Context, invocation.Invocation, string) error {
	return nil
}

func (a *desktopAuthorizationHTTPApplication) BeginDesktopExternalAuthentication(context.Context, invocation.Invocation, app.BeginDesktopExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error) {
	return &model.ExternalAuthenticationStart{RedirectURL: "https://identity.example/login", Binding: "external-binding", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}, nil
}

func (a *desktopAuthorizationHTTPApplication) StartDesktopAuthorization(_ context.Context, _ invocation.Invocation, command app.StartDesktopAuthorizationCommand) (*app.DesktopAuthorizationStart, error) {
	a.start = command
	if a.startErr != nil {
		return nil, a.startErr
	}
	return &app.DesktopAuthorizationStart{AuthorizationURL: "https://proctor.example.edu/authorize/desktop", ExpiresAt: 100}, nil
}

func TestDesktopAuthorizationHTTPExposesBoundedAttemptFailures(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopAuthorizationHTTPApplication{startErr: apperror.New("authentication.rate_limited")}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, desktopAuthorizationResource(applicationFake, testDesktopAuthorizationCookies(t)))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/desktop/authorizations", strings.NewReader(
		`{"callback_url":"http://127.0.0.1:49152/random","state":"state","code_challenge":"challenge"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusTooManyRequests, response.Code, "rate-limited start = %d: %s", response.Code, response.Body.String())
}

func (a *desktopAuthorizationHTTPApplication) ApproveDesktopAuthorization(_ context.Context, _ invocation.Invocation, command app.ApproveDesktopAuthorizationCommand) (*app.DesktopAuthorizationApproval, error) {
	a.approve = command
	return &app.DesktopAuthorizationApproval{RedirectURL: "http://127.0.0.1:49152/callback?code=opaque&state=state", ExpiresAt: 90}, nil
}

func (a *desktopAuthorizationHTTPApplication) CancelDesktopAuthorization(_ context.Context, _ invocation.Invocation, command app.ApproveDesktopAuthorizationCommand) error {
	a.cancel = command
	return nil
}

func (a *desktopAuthorizationHTTPApplication) ExchangeDesktopAuthorization(_ context.Context, _ invocation.Invocation, command app.ExchangeDesktopAuthorizationCommand) (*app.DesktopAuthorizationExchangeResult, error) {
	a.exchange = command
	return &app.DesktopAuthorizationExchangeResult{
		Session: &model.Session{ID: model.NewSessionID(), UserID: model.NewUserID(), ClientType: model.SessionClientDesktop},
		Tokens:  &model.AuthenticationTokens{AccessToken: "access", RefreshToken: "refresh"},
	}, nil
}

func TestDesktopAuthorizationHTTPMapsPublicClientProtocolAndDisablesCaching(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &desktopAuthorizationHTTPApplication{}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
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
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if strings.Contains(test.path, "/authorizations/") {
				request.AddCookie(&http.Cookie{Name: BrowserDesktopAuthorizationCookieName, Value: "binding", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			}
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, test.wantStatus, response.Code, "%s = %d cache=%q body=%s", test.path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "%s = %d cache=%q body=%s", test.path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		})
	}
	require.Equal(t, "192.0.2.1", applicationFake.start.Source, "mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	require.Equal(t, "192.0.2.1", applicationFake.exchange.Source, "mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	require.Equal(t, "binding", applicationFake.approve.Binding, "mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	require.Equal(t, "binding", applicationFake.cancel.Binding, "mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
	require.Equal(t, "verifier", applicationFake.exchange.CodeVerifier, "mapped commands = %#v %#v %#v %#v", applicationFake.start, applicationFake.approve, applicationFake.cancel, applicationFake.exchange)
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
	require.Equal(t, http.StatusNoContent, bindResponse.Code, "bind = %d cache=%q body=%s", bindResponse.Code, bindResponse.Header().Get("Cache-Control"), bindResponse.Body.String())
	require.Equal(t, "no-store", bindResponse.Header().Get("Cache-Control"), "bind = %d cache=%q body=%s", bindResponse.Code, bindResponse.Header().Get("Cache-Control"), bindResponse.Body.String())
	bindingCookie := responseCookie(t, bindResponse.Result().Cookies(), BrowserDesktopAuthorizationCookieName)
	require.Equal(t, "binding", bindingCookie.Value)
	require.Equal(t, "/api/v1/auth/desktop/authorizations", bindingCookie.Path)
	require.True(t, bindingCookie.HttpOnly)
	require.True(t, bindingCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, bindingCookie.SameSite)

	providerRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/desktop/authorizations/authenticate/providers/provider/login?state=state", nil)
	providerRequest.AddCookie(bindingCookie)
	providerResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(providerResponse, providerRequest)
	require.Equal(t, http.StatusSeeOther, providerResponse.Code, "provider start = %d location=%q cache=%q body=%s", providerResponse.Code, providerResponse.Header().Get("Location"), providerResponse.Header().Get("Cache-Control"), providerResponse.Body.String())
	require.Equal(t, "https://identity.example/login", providerResponse.Header().Get("Location"), "provider start = %d location=%q cache=%q body=%s", providerResponse.Code, providerResponse.Header().Get("Location"), providerResponse.Header().Get("Cache-Control"), providerResponse.Body.String())
	require.Equal(t, "no-store", providerResponse.Header().Get("Cache-Control"), "provider start = %d location=%q cache=%q body=%s", providerResponse.Code, providerResponse.Header().Get("Location"), providerResponse.Header().Get("Cache-Control"), providerResponse.Body.String())
	externalCookie := responseCookie(t, providerResponse.Result().Cookies(), BrowserExternalLoginCookieName)
	require.Equal(t, "external-binding", externalCookie.Value)
	require.Equal(t, "/api/v1/auth/providers/", externalCookie.Path)
	require.True(t, externalCookie.HttpOnly)
	require.True(t, externalCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, externalCookie.SameSite)
}

func responseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	require.FailNow(t, fmt.Sprintf("response cookie %q was not set", name))
	return nil
}

func testDesktopAuthorizationCookies(t *testing.T) browserCookies {
	t.Helper()
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	require.NoError(t, err)
	return cookies
}
