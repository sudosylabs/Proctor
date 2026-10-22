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

type authenticationEntryHTTPApplication struct {
	authenticationEntryApplication
	loginCommand        app.LoginCommand
	loginResult         *app.LoginResult
	loginError          error
	loginCalls          int
	logoutCommand       app.LogoutCommand
	logoutCalls         int
	registrationCommand app.RegisterLocalUserCommand
	registrationCalls   int
	registrationError   error
}

func (af *authenticationEntryHTTPApplication) RegisterLocalUser(
	_ context.Context,
	_ invocation.Invocation,
	command app.RegisterLocalUserCommand,
) error {
	af.registrationCommand = command
	af.registrationCalls++
	return af.registrationError
}

func (af *authenticationEntryHTTPApplication) Login(
	_ context.Context,
	_ invocation.Invocation,
	command app.LoginCommand,
) (*app.LoginResult, error) {
	af.loginCommand = command
	af.loginCalls++
	return af.loginResult, af.loginError
}

func (af *authenticationEntryHTTPApplication) Logout(
	_ context.Context,
	_ invocation.Invocation,
	command app.LogoutCommand,
) error {
	af.logoutCommand = command
	af.logoutCalls++
	return nil
}

type externalAuthenticationEntryHTTPApplication struct {
	externalAuthenticationEntryApplication
	providers       []model.ExternalAuthenticationProvider
	providersError  error
	beginCommand    app.BeginExternalAuthenticationCommand
	start           *model.ExternalAuthenticationStart
	completeCommand app.CompleteExternalAuthenticationCommand
	completion      *model.ExternalAuthenticationCompletion
}

func (af *externalAuthenticationEntryHTTPApplication) CompleteExternalAuthentication(
	_ context.Context,
	_ invocation.Invocation,
	command app.CompleteExternalAuthenticationCommand,
) (*model.ExternalAuthenticationCompletion, error) {
	af.completeCommand = command
	return af.completion, nil
}

func (af *externalAuthenticationEntryHTTPApplication) ExternalAuthenticationProviders(context.Context) ([]model.ExternalAuthenticationProvider, error) {
	return af.providers, af.providersError
}

func TestExternalAuthenticationCallbackReturnsValidatedRedirectAndCookies(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	now := time.Now()
	externalAuthentication := &externalAuthenticationEntryHTTPApplication{
		completion: &model.ExternalAuthenticationCompletion{
			ReturnTo: "/exams",
			Tokens: &model.AuthenticationTokens{
				AccessToken: "access", RefreshToken: "refresh",
				AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
			},
		},
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(externalAuthentication, cookies))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers/oidc/callback?code=accepted", nil)
	request.AddCookie(&http.Cookie{Name: BrowserExternalLoginCookieName, Value: "binding"})
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusSeeOther, response.Code, "callback redirect = %d %#v %s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, "/exams", response.Header().Get("Location"), "callback redirect = %d %#v %s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, "oidc", externalAuthentication.completeCommand.ProviderID)
	require.Equal(t, "binding", externalAuthentication.completeCommand.Binding)
	require.Equal(t, "accepted", externalAuthentication.completeCommand.Callback.Values["code"][0])
	require.Len(t, response.Header().Values("Set-Cookie"), 5)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func (af *externalAuthenticationEntryHTTPApplication) BeginExternalAuthentication(
	_ context.Context,
	_ invocation.Invocation,
	command app.BeginExternalAuthenticationCommand,
) (*model.ExternalAuthenticationStart, error) {
	af.beginCommand = command
	return af.start, nil
}

func TestExternalAuthenticationLoginDefaultsToWebAndNeverCreatesDesktopSessionsDirectly(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	applicationFake := &externalAuthenticationEntryHTTPApplication{start: &model.ExternalAuthenticationStart{
		RedirectURL: "https://identity.example.test/login", Binding: model.NewCredentialToken(),
		ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers/campus/login", nil)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusSeeOther, response.Code, "login redirect = %d %s", response.Code, response.Body.String())
	require.Equal(t, model.SessionClientWeb, applicationFake.beginCommand.ClientType, "default external login client type")
}

func TestExternalAuthenticationInvitationClaimUsesStrictPOSTBody(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	claim := model.NewCredentialToken()
	applicationFake := &externalAuthenticationEntryHTTPApplication{start: &model.ExternalAuthenticationStart{
		RedirectURL: "https://identity.example.test/login", Binding: model.NewCredentialToken(), ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	request := newJSONRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"invitation_claim":"`+claim+`","return_to":"/join"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusSeeOther, response.Code, "invitation start = %d command=%#v headers=%#v body", response.Code, applicationFake.beginCommand, response.Header())
	require.Equal(t, claim, applicationFake.beginCommand.InvitationClaim, "invitation start = %d command=%#v headers=%#v body", response.Code, applicationFake.beginCommand, response.Header())
	require.Equal(t, "/join", applicationFake.beginCommand.ReturnTo, "invitation start = %d command=%#v headers=%#v body", response.Code, applicationFake.beginCommand, response.Header())
	require.NotContains(t, response.Body.String(), claim, "invitation start = %d command=%#v headers=%#v body", response.Code, applicationFake.beginCommand, response.Header())
	require.NotContains(t, response.Header().Get("Location"), claim, "invitation start = %d command=%#v headers=%#v body", response.Code, applicationFake.beginCommand, response.Header())

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, newJSONRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"invitation_claim":"`+claim+`","unknown":true}`)))
	require.Equal(t, http.StatusBadRequest, invalid.Code, "unknown invitation start field = %d %s", invalid.Code, invalid.Body.String())
	missing := httptest.NewRecorder()
	httpAPI.ServeHTTP(missing, newJSONRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"return_to":"/join"}`)))
	require.Equal(t, http.StatusBadRequest, missing.Code, "missing invitation claim = %d %s", missing.Code, missing.Body.String())
}

func TestAuthenticationResourceRunsPublicAndSessionEntriesThroughKernel(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	now := time.Now()
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: now,
	}
	authentication := &authenticationEntryHTTPApplication{
		loginResult: &app.LoginResult{
			User: &model.User{ID: principal.UserID, Username: "student", Email: "student@example.edu"},
			Session: &model.Session{
				ID: principal.SessionID, UserID: principal.UserID, ClientType: model.SessionClientWeb,
				AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
				AuthenticatedAt: now, LastActivityAt: now, IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(time.Hour),
			},
			Tokens: &model.AuthenticationTokens{
				AccessToken: "access", RefreshToken: "refresh",
				AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
			},
		},
	}
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal},
		authenticationResource(authentication, cookies),
	)

	loginRequest := newJSONRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"login_id":"student@example.edu","password":"secret","client_type":"web"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(loginResponse, loginRequest)
	require.Equal(t, http.StatusOK, loginResponse.Code, "login status = %d: %s", loginResponse.Code, loginResponse.Body.String())
	require.Equal(t, "student@example.edu", authentication.loginCommand.LoginID)
	require.Equal(t, model.SessionClientWeb, authentication.loginCommand.ClientType)
	require.Equal(t, "no-store", loginResponse.Header().Get("Cache-Control"))
	require.Len(t, loginResponse.Header().Values("Set-Cookie"), 4)
	var loginBody authenticationResponse
	require.NoError(t, json.Unmarshal(loginResponse.Body.Bytes(), &loginBody))
	require.Nil(t, loginBody.Tokens)
	require.NotNil(t, loginBody.Session)

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.Header.Set("Authorization", "Bearer access")
	logoutResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(logoutResponse, logoutRequest)
	require.Equal(t, http.StatusNoContent, logoutResponse.Code, "logout status/calls = %d/%d: %s", logoutResponse.Code, authentication.logoutCalls, logoutResponse.Body.String())
	require.Equal(t, 1, authentication.logoutCalls, "logout status/calls = %d/%d: %s", logoutResponse.Code, authentication.logoutCalls, logoutResponse.Body.String())
}

func TestExternalAuthenticationProviderListFailsClosedWhenPolicyIsUnavailable(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	applicationFake := &externalAuthenticationEntryHTTPApplication{
		providers:      []model.ExternalAuthenticationProvider{{ID: "configured-but-unknown", Type: "oidc"}},
		providersError: apperror.New("authentication.internal"),
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil))
	require.Equal(t, http.StatusInternalServerError, response.Code, "provider-list failure")
	require.NotContains(t, response.Body.String(), "configured-but-unknown", "provider-list failure")
}

func TestAuthenticationResourceKernelRejectsMissingCredentialAndInvalidJSON(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	authentication := &authenticationEntryHTTPApplication{}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{},
		authenticationResource(authentication, cookies),
	)

	refresh := httptest.NewRecorder()
	httpAPI.ServeHTTP(refresh, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil))
	require.Equal(t, http.StatusUnauthorized, refresh.Code, "missing refresh credential")
	require.Contains(t, refresh.Body.String(), `"code":"authentication.required"`, "missing refresh credential")

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		invalid,
		newJSONRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login_id":"student","unknown":true}`)),
	)
	require.Equal(t, http.StatusBadRequest, invalid.Code, "invalid login body")
	require.Contains(t, invalid.Body.String(), `"code":"request.invalid"`, "invalid login body")

	authentication.loginError = apperror.New("resource.not_found")
	undeclared := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		undeclared,
		newJSONRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(`{"login_id":"student","password":"secret","client_type":"desktop"}`),
		),
	)
	require.Equal(t, http.StatusInternalServerError, undeclared.Code, "undeclared login error")
	require.Contains(t, undeclared.Body.String(), `"code":"server.internal"`, "undeclared login error")
	require.NotContains(t, undeclared.Body.String(), "resource.not_found", "undeclared login error")

	authentication.loginError = apperror.New("authentication.mfa.code_invalid")
	invalidSecondFactor := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		invalidSecondFactor,
		newJSONRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(`{"login_id":"student","password":"secret","client_type":"desktop","mfa_code":"used-recovery-code"}`),
		),
	)
	require.Equal(t, http.StatusUnauthorized, invalidSecondFactor.Code, "invalid login second factor")
	require.Contains(t, invalidSecondFactor.Body.String(), `"code":"authentication.mfa.code_invalid"`, "invalid login second factor")
}

func TestPublicRegistrationUsesStrictBodyAndReturnsNoAccountProjection(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	applicationFake := &authenticationEntryHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, authenticationResource(applicationFake, browserCookies{}))

	request := newJSONRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"long-private-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code, "registration response = %d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, 0, response.Body.Len(), "registration response = %d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "registration response = %d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	require.Equal(t, 1, applicationFake.registrationCalls)
	require.Equal(t, "student", applicationFake.registrationCommand.Username)
	require.Equal(t, "student@example.edu", applicationFake.registrationCommand.Email)
	require.Equal(t, "New", applicationFake.registrationCommand.FirstName)
	require.Equal(t, "Student", applicationFake.registrationCommand.LastName)
	require.Equal(t, "long-private-password", applicationFake.registrationCommand.Password)
	require.NotEmpty(t, applicationFake.registrationCommand.Source)

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, newJSONRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"private","display_name":"New Student"}`)))
	require.Equal(t, http.StatusBadRequest, invalid.Code, "display-name registration = %d calls=%d body=%s", invalid.Code, applicationFake.registrationCalls, invalid.Body.String())
	require.Equal(t, 1, applicationFake.registrationCalls, "display-name registration = %d calls=%d body=%s", invalid.Code, applicationFake.registrationCalls, invalid.Body.String())

	applicationFake.registrationError = apperror.New("authentication.registration.invitation_required")
	disabled := httptest.NewRecorder()
	httpAPI.ServeHTTP(disabled, newJSONRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"private"}`)))
	require.Equal(t, http.StatusForbidden, disabled.Code, "disabled registration")
	require.Contains(t, disabled.Body.String(), `"code":"authentication.registration.invitation_required"`, "disabled registration")
	require.NotContains(t, disabled.Body.String(), "student@example.edu", "disabled registration")
}

func TestExternalAuthenticationRedirectIsNamedKernelProtocolOperation(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	require.NoError(t, err)
	externalAuthentication := &externalAuthenticationEntryHTTPApplication{
		providers: []model.ExternalAuthenticationProvider{{ID: "oidc", DisplayName: "Institution Login", Type: "oidc"}},
		start: &model.ExternalAuthenticationStart{
			RedirectURL: "https://identity.example.edu/authorize", Binding: "binding",
			ExpiresAt: model.MillisFromTime(time.Now().Add(5 * time.Minute)),
		},
	}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{},
		externalAuthenticationResource(externalAuthentication, cookies),
	)

	providers := httptest.NewRecorder()
	httpAPI.ServeHTTP(providers, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil))
	require.Equal(t, http.StatusOK, providers.Code, "providers response")
	require.Contains(t, providers.Body.String(), `"id":"oidc"`, "providers response")

	begin := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		begin,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/auth/providers/oidc/login?return_to=%2Fexams&client_type=desktop&device_id=device-a",
			nil,
		),
	)
	require.Equal(t, http.StatusSeeOther, begin.Code, "external login redirect = %d %#v", begin.Code, begin.Header())
	require.Equal(t, externalAuthentication.start.RedirectURL, begin.Header().Get("Location"), "external login redirect = %d %#v", begin.Code, begin.Header())
	require.Equal(t, "oidc", externalAuthentication.beginCommand.ProviderID)
	require.Equal(t, "/exams", externalAuthentication.beginCommand.ReturnTo)
	require.Equal(t, "device-a", externalAuthentication.beginCommand.DeviceID)
	require.Len(t, begin.Header().Values("Set-Cookie"), 1)
	require.Equal(t, "no-store", begin.Header().Get("Cache-Control"))
}
