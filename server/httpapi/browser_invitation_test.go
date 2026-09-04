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

type browserInvitationHTTPApplication struct {
	start   application.StartBrowserInvitationCommand
	local   application.BrowserInvitationAcceptanceCommand
	session application.BrowserInvitationSessionAcceptanceCommand
}

func (a *browserInvitationHTTPApplication) StartBrowserInvitation(_ context.Context, _ application.Invocation, command application.StartBrowserInvitationCommand) (*application.BrowserInvitationStart, error) {
	a.start = command
	return &application.BrowserInvitationStart{
		Handle: model.NewCredentialToken(), BrowserProof: model.NewCredentialToken(),
		Purpose: model.InvitationPurposeStudentClass, Requirement: application.BrowserInvitationRequirementAccount,
		ExpiresAt: time.Now().Add(time.Minute).UnixMilli(),
	}, nil
}

func (a *browserInvitationHTTPApplication) AcceptBrowserInvitation(_ context.Context, _ application.Invocation, command application.BrowserInvitationAcceptanceCommand) (*application.InvitationAcceptanceView, error) {
	a.local = command
	return browserInvitationAcceptance(), nil
}

func (a *browserInvitationHTTPApplication) AcceptBrowserInvitationWithSession(_ context.Context, _ application.Invocation, command application.BrowserInvitationSessionAcceptanceCommand) (*application.InvitationAcceptanceView, error) {
	a.session = command
	return browserInvitationAcceptance(), nil
}

func TestBrowserInvitationHTTPKeepsSecondProofInHttpOnlyCookie(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &browserInvitationHTTPApplication{}
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{}, browserInvitationResource(applicationFake, cookies))

	start := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations", strings.NewReader(`{"claim":"`+model.NewCredentialToken()+`"}`))
	start.Header.Set("Content-Type", "application/json")
	started := httptest.NewRecorder()
	httpAPI.ServeHTTP(started, start)
	if started.Code != http.StatusCreated || started.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start = %d cache=%q body=%s", started.Code, started.Header().Get("Cache-Control"), started.Body.String())
	}
	proofCookies := started.Result().Cookies()
	if len(proofCookies) != 1 || proofCookies[0].Name != BrowserInvitationProofCookieName ||
		!proofCookies[0].HttpOnly || !proofCookies[0].Secure || proofCookies[0].Path != "/api/v1/auth/browser/invitations" {
		t.Fatalf("proof cookies = %#v", proofCookies)
	}
	if strings.Contains(started.Body.String(), proofCookies[0].Value) {
		t.Fatal("browser proof was disclosed in the JSON response")
	}

	accept := httptest.NewRequest(http.MethodPost, "/api/v1/auth/browser/invitations/accept", strings.NewReader(`{"handle":"`+model.NewCredentialToken()+`","password":"correct horse battery staple","username":"student"}`))
	accept.Header.Set("Content-Type", "application/json")
	accept.AddCookie(proofCookies[0])
	accepted := httptest.NewRecorder()
	httpAPI.ServeHTTP(accepted, accept)
	if accepted.Code != http.StatusOK || accepted.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("accept = %d cache=%q body=%s", accepted.Code, accepted.Header().Get("Cache-Control"), accepted.Body.String())
	}
	cleared := accepted.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != BrowserInvitationProofCookieName || cleared[0].MaxAge >= 0 {
		t.Fatalf("cleared cookies = %#v", cleared)
	}
	if applicationFake.start.Source != "192.0.2.1:1234" || applicationFake.local.BrowserProof != proofCookies[0].Value || applicationFake.local.Username != "student" {
		t.Fatalf("mapped commands = %#v %#v", applicationFake.start, applicationFake.local)
	}
}

func TestBrowserInvitationSessionAcceptanceUsesAuthenticatedPrincipal(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &browserInvitationHTTPApplication{}
	cookies, err := newBrowserCookies("https://proctor.example.edu")
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
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
	if response.Code != http.StatusOK || applicationFake.session.BrowserProof == "" {
		t.Fatalf("accept session = %d command=%#v body=%s", response.Code, applicationFake.session, response.Body.String())
	}
}

func browserInvitationAcceptance() *application.InvitationAcceptanceView {
	return &application.InvitationAcceptanceView{
		Invitation: application.InvitationView{ID: model.NewInvitationID()},
		User:       &model.User{ID: model.NewUserID()},
	}
}
