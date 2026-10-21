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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type browserInvitationHTTPApplication struct {
	start   app.StartBrowserInvitationCommand
	local   app.BrowserInvitationAcceptanceCommand
	session app.BrowserInvitationSessionAcceptanceCommand
}

func (a *browserInvitationHTTPApplication) StartBrowserInvitation(_ context.Context, _ app.Invocation, command app.StartBrowserInvitationCommand) (*app.BrowserInvitationStart, error) {
	a.start = command
	return &app.BrowserInvitationStart{
		Handle: model.NewCredentialToken(), BrowserProof: model.NewCredentialToken(),
		Purpose: model.InvitationPurposeStudentClass, Requirement: app.BrowserInvitationRequirementAccount,
		ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}, nil
}

func (a *browserInvitationHTTPApplication) AcceptBrowserInvitation(_ context.Context, _ app.Invocation, command app.BrowserInvitationAcceptanceCommand) (*app.InvitationAcceptanceView, error) {
	a.local = command
	return browserInvitationAcceptance(), nil
}

func (a *browserInvitationHTTPApplication) AcceptBrowserInvitationWithSession(_ context.Context, _ app.Invocation, command app.BrowserInvitationSessionAcceptanceCommand) (*app.InvitationAcceptanceView, error) {
	a.session = command
	return browserInvitationAcceptance(), nil
}

func TestBrowserInvitationHTTPKeepsSecondProofInHttpOnlyCookie(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &browserInvitationHTTPApplication{}
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	require.NoError(t, err)
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, browserInvitationResource(applicationFake, cookies))

	start := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations", strings.NewReader(`{"claim":"`+model.NewCredentialToken()+`"}`))
	start.Header.Set("Content-Type", "application/json")
	started := httptest.NewRecorder()
	httpAPI.ServeHTTP(started, start)
	require.Equal(t, http.StatusCreated, started.Code, "start = %d cache=%q body=%s", started.Code, started.Header().Get("Cache-Control"), started.Body.String())
	require.Equal(t, "no-store", started.Header().Get("Cache-Control"), "start = %d cache=%q body=%s", started.Code, started.Header().Get("Cache-Control"), started.Body.String())
	proofCookies := started.Result().Cookies()
	require.Len(t, proofCookies, 1)
	require.Equal(t, BrowserInvitationProofCookieName, proofCookies[0].Name)
	require.True(t, proofCookies[0].HttpOnly)
	require.True(t, proofCookies[0].Secure)
	require.Equal(t, "/api/v1/auth/browser/invitations", proofCookies[0].Path)
	require.NotContains(t, started.Body.String(), proofCookies[0].Value, "browser proof was disclosed in the JSON response")

	accept := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations/accept", strings.NewReader(`{"handle":"`+model.NewCredentialToken()+`","password":"correct horse battery staple","username":"student"}`))
	accept.Header.Set("Content-Type", "application/json")
	accept.AddCookie(proofCookies[0])
	accepted := httptest.NewRecorder()
	httpAPI.ServeHTTP(accepted, accept)
	require.Equal(t, http.StatusOK, accepted.Code, "accept = %d cache=%q body=%s", accepted.Code, accepted.Header().Get("Cache-Control"), accepted.Body.String())
	require.Equal(t, "no-store", accepted.Header().Get("Cache-Control"), "accept = %d cache=%q body=%s", accepted.Code, accepted.Header().Get("Cache-Control"), accepted.Body.String())
	cleared := accepted.Result().Cookies()
	require.Len(t, cleared, 1)
	require.Equal(t, BrowserInvitationProofCookieName, cleared[0].Name)
	require.Negative(t, cleared[0].MaxAge)
	require.Equal(t, "192.0.2.1", applicationFake.start.Source, "mapped commands = %#v %#v", applicationFake.start, applicationFake.local)
	require.Equal(t, proofCookies[0].Value, applicationFake.local.BrowserProof, "mapped commands = %#v %#v", applicationFake.start, applicationFake.local)
	require.Equal(t, "student", applicationFake.local.Username, "mapped commands = %#v %#v", applicationFake.start, applicationFake.local)
}

func TestBrowserInvitationSessionAcceptanceUsesAuthenticatedPrincipal(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &browserInvitationHTTPApplication{}
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	require.NoError(t, err)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(),
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, browserInvitationResource(applicationFake, cookies))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations/accept-session", strings.NewReader(`{"handle":"`+model.NewCredentialToken()+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer access")
	request.AddCookie(&http.Cookie{Name: BrowserInvitationProofCookieName, Value: model.NewCredentialToken()})
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "accept session = %d command=%#v body=%s", response.Code, applicationFake.session, response.Body.String())
	require.NotEmpty(t, applicationFake.session.BrowserProof, "accept session = %d command=%#v body=%s", response.Code, applicationFake.session, response.Body.String())
}

func browserInvitationAcceptance() *app.InvitationAcceptanceView {
	return &app.InvitationAcceptanceView{
		Invitation: app.InvitationView{ID: model.NewInvitationID()},
		User:       &model.User{ID: model.NewUserID()},
	}
}
