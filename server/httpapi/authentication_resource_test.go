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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type authenticationEntryHTTPApplication struct {
	authenticationEntryApplication
	loginCommand        application.LoginCommand
	loginResult         *application.LoginResult
	loginError          error
	logoutCommand       application.LogoutCommand
	logoutCalls         int
	registrationCommand application.RegisterLocalUserCommand
	registrationCalls   int
	registrationError   error
}

func (applicationFake *authenticationEntryHTTPApplication) RegisterLocalUser(
	_ context.Context,
	_ application.Invocation,
	command application.RegisterLocalUserCommand,
) error {
	applicationFake.registrationCommand = command
	applicationFake.registrationCalls++
	return applicationFake.registrationError
}

func (applicationFake *authenticationEntryHTTPApplication) Login(
	_ context.Context,
	_ application.Invocation,
	command application.LoginCommand,
) (*application.LoginResult, error) {
	applicationFake.loginCommand = command
	return applicationFake.loginResult, applicationFake.loginError
}

func (applicationFake *authenticationEntryHTTPApplication) Logout(
	_ context.Context,
	_ application.Invocation,
	command application.LogoutCommand,
) error {
	applicationFake.logoutCommand = command
	applicationFake.logoutCalls++
	return nil
}

type externalAuthenticationEntryHTTPApplication struct {
	externalAuthenticationEntryApplication
	providers       []model.ExternalAuthenticationProvider
	providersError  error
	beginCommand    application.BeginExternalAuthenticationCommand
	start           *model.ExternalAuthenticationStart
	completeCommand application.CompleteExternalAuthenticationCommand
	completion      *model.ExternalAuthenticationCompletion
}

func (applicationFake *externalAuthenticationEntryHTTPApplication) CompleteExternalAuthentication(
	_ context.Context,
	_ application.Invocation,
	command application.CompleteExternalAuthenticationCommand,
) (*model.ExternalAuthenticationCompletion, error) {
	applicationFake.completeCommand = command
	return applicationFake.completion, nil
}

func (applicationFake *externalAuthenticationEntryHTTPApplication) ExternalAuthenticationProviders(context.Context) ([]model.ExternalAuthenticationProvider, error) {
	return applicationFake.providers, applicationFake.providersError
}

func TestExternalAuthenticationCallbackReturnsValidatedRedirectAndCookies(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
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
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/exams" {
		t.Fatalf("callback redirect = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	if externalAuthentication.completeCommand.ProviderID != "oidc" || externalAuthentication.completeCommand.Binding != "binding" || externalAuthentication.completeCommand.Callback.Values["code"][0] != "accepted" {
		t.Fatalf("callback command = %#v", externalAuthentication.completeCommand)
	}
	if len(response.Header().Values("Set-Cookie")) != 5 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("callback effects = %#v", response.Header())
	}
}

func (applicationFake *externalAuthenticationEntryHTTPApplication) BeginExternalAuthentication(
	_ context.Context,
	_ application.Invocation,
	command application.BeginExternalAuthenticationCommand,
) (*model.ExternalAuthenticationStart, error) {
	applicationFake.beginCommand = command
	return applicationFake.start, nil
}

func TestExternalAuthenticationLoginDefaultsToWebAndNeverCreatesDesktopSessionsDirectly(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	applicationFake := &externalAuthenticationEntryHTTPApplication{start: &model.ExternalAuthenticationStart{
		RedirectURL: "https://identity.example.test/login", Binding: model.NewCredentialToken(),
		ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers/campus/login", nil)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login redirect = %d %s", response.Code, response.Body.String())
	}
	if applicationFake.beginCommand.ClientType != model.SessionClientWeb {
		t.Fatalf("default external login client type = %q, want web", applicationFake.beginCommand.ClientType)
	}
}

func TestExternalAuthenticationInvitationClaimUsesStrictPOSTBody(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	claim := model.NewCredentialToken()
	applicationFake := &externalAuthenticationEntryHTTPApplication{start: &model.ExternalAuthenticationStart{
		RedirectURL: "https://identity.example.test/login", Binding: model.NewCredentialToken(), ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"invitation_claim":"`+claim+`","return_to":"/join"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || applicationFake.beginCommand.InvitationClaim != claim ||
		applicationFake.beginCommand.ReturnTo != "/join" || strings.Contains(response.Body.String(), claim) ||
		strings.Contains(response.Header().Get("Location"), claim) {
		t.Fatalf("invitation start = %d command=%#v headers=%#v body=%s", response.Code, applicationFake.beginCommand, response.Header(), response.Body.String())
	}

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"invitation_claim":"`+claim+`","unknown":true}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown invitation start field = %d %s", invalid.Code, invalid.Body.String())
	}
	missing := httptest.NewRecorder()
	httpAPI.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/api/v1/auth/providers/campus/login",
		strings.NewReader(`{"return_to":"/join"}`)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing invitation claim = %d %s", missing.Code, missing.Body.String())
	}
}

func TestAuthenticationResourceRunsPublicAndSessionEntriesThroughKernel(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	now := time.Now()
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientCLI, AuthenticatedAt: now,
	}
	authentication := &authenticationEntryHTTPApplication{
		loginResult: &application.LoginResult{
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
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{principal: principal},
		authenticationResource(authentication, cookies),
	)

	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		strings.NewReader(`{"login_id":"student@example.edu","password":"secret","client_type":"web"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
	if authentication.loginCommand.LoginID != "student@example.edu" || authentication.loginCommand.ClientType != model.SessionClientWeb {
		t.Fatalf("login command = %#v", authentication.loginCommand)
	}
	if loginResponse.Header().Get("Cache-Control") != "no-store" || len(loginResponse.Header().Values("Set-Cookie")) != 4 {
		t.Fatalf("login security headers = %#v", loginResponse.Header())
	}
	var loginBody authenticationResponse
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.Tokens != nil || loginBody.Session == nil {
		t.Fatalf("browser login response = %#v", loginBody)
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.Header.Set("Authorization", "Bearer access")
	logoutResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(logoutResponse, logoutRequest)
	if logoutResponse.Code != http.StatusNoContent || authentication.logoutCalls != 1 {
		t.Fatalf("logout status/calls = %d/%d: %s", logoutResponse.Code, authentication.logoutCalls, logoutResponse.Body.String())
	}
}

func TestExternalAuthenticationProviderListFailsClosedWhenPolicyIsUnavailable(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	applicationFake := &externalAuthenticationEntryHTTPApplication{
		providers:      []model.ExternalAuthenticationProvider{{Id: "configured-but-unknown", Type: "oidc"}},
		providersError: application.NewError("authentication.internal"),
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, externalAuthenticationResource(applicationFake, cookies))
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", nil))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "configured-but-unknown") {
		t.Fatalf("provider-list failure = %d %s", response.Code, response.Body.String())
	}
}

func TestAuthenticationResourceKernelRejectsMissingCredentialAndInvalidJSON(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	authentication := &authenticationEntryHTTPApplication{}
	httpAPI := newFocusedResourceAPI(
		t, logger, classRouteAuthenticator{},
		authenticationResource(authentication, cookies),
	)

	refresh := httptest.NewRecorder()
	httpAPI.ServeHTTP(refresh, httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil))
	if refresh.Code != http.StatusUnauthorized || !strings.Contains(refresh.Body.String(), `"code":"authentication.required"`) {
		t.Fatalf("missing refresh credential = %d %s", refresh.Code, refresh.Body.String())
	}

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		invalid,
		httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"login_id":"student","unknown":true}`)),
	)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"request.invalid"`) {
		t.Fatalf("invalid login body = %d %s", invalid.Code, invalid.Body.String())
	}

	authentication.loginError = application.NewError("resource.not_found")
	undeclared := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		undeclared,
		httptest.NewRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(`{"login_id":"student","password":"secret","client_type":"desktop"}`),
		),
	)
	if undeclared.Code != http.StatusInternalServerError || !strings.Contains(undeclared.Body.String(), `"code":"internal"`) || strings.Contains(undeclared.Body.String(), "resource.not_found") {
		t.Fatalf("undeclared login error = %d %s", undeclared.Code, undeclared.Body.String())
	}

	authentication.loginError = application.NewError("authentication.mfa.invalid_code")
	invalidSecondFactor := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		invalidSecondFactor,
		httptest.NewRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(`{"login_id":"student","password":"secret","client_type":"desktop","mfa_code":"used-recovery-code"}`),
		),
	)
	if invalidSecondFactor.Code != http.StatusUnauthorized ||
		!strings.Contains(invalidSecondFactor.Body.String(), `"code":"authentication.mfa.invalid_code"`) {
		t.Fatalf("invalid login second factor = %d %s", invalidSecondFactor.Code, invalidSecondFactor.Body.String())
	}
}

func TestPublicRegistrationUsesStrictBodyAndReturnsNoAccountProjection(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	applicationFake := &authenticationEntryHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, authenticationResource(applicationFake, browserCookies{}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"long-private-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("registration response = %d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if applicationFake.registrationCalls != 1 || applicationFake.registrationCommand.Username != "student" ||
		applicationFake.registrationCommand.Email != "student@example.edu" ||
		applicationFake.registrationCommand.FirstName != "New" || applicationFake.registrationCommand.LastName != "Student" ||
		applicationFake.registrationCommand.Password != "long-private-password" || applicationFake.registrationCommand.Source == "" {
		t.Fatalf("registration command = %#v", applicationFake.registrationCommand)
	}

	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"private","display_name":"New Student"}`)))
	if invalid.Code != http.StatusBadRequest || applicationFake.registrationCalls != 1 {
		t.Fatalf("display-name registration = %d calls=%d body=%s", invalid.Code, applicationFake.registrationCalls, invalid.Body.String())
	}

	applicationFake.registrationError = application.NewError("authentication.registration.invitation_required")
	disabled := httptest.NewRecorder()
	httpAPI.ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"username":"student","email":"student@example.edu","first_name":"New","last_name":"Student","password":"private"}`)))
	if disabled.Code != http.StatusForbidden || !strings.Contains(disabled.Body.String(), `"code":"authentication.registration.invitation_required"`) ||
		strings.Contains(disabled.Body.String(), "student@example.edu") {
		t.Fatalf("disabled registration = %d %s", disabled.Code, disabled.Body.String())
	}
}

func TestExternalAuthenticationRedirectIsNamedKernelProtocolOperation(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	externalAuthentication := &externalAuthenticationEntryHTTPApplication{
		providers: []model.ExternalAuthenticationProvider{{Id: "oidc", DisplayName: "Institution Login", Type: "oidc"}},
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
	if providers.Code != http.StatusOK || !strings.Contains(providers.Body.String(), `"id":"oidc"`) {
		t.Fatalf("providers response = %d %s", providers.Code, providers.Body.String())
	}

	begin := httptest.NewRecorder()
	httpAPI.ServeHTTP(
		begin,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/auth/providers/oidc/login?return_to=%2Fexams&client_type=desktop&device_id=device-a",
			nil,
		),
	)
	if begin.Code != http.StatusSeeOther || begin.Header().Get("Location") != externalAuthentication.start.RedirectURL {
		t.Fatalf("external login redirect = %d %#v", begin.Code, begin.Header())
	}
	if externalAuthentication.beginCommand.ProviderID != "oidc" || externalAuthentication.beginCommand.ReturnTo != "/exams" || externalAuthentication.beginCommand.DeviceID != "device-a" {
		t.Fatalf("external login command = %#v", externalAuthentication.beginCommand)
	}
	if len(begin.Header().Values("Set-Cookie")) != 1 || begin.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("external login headers = %#v", begin.Header())
	}
}
