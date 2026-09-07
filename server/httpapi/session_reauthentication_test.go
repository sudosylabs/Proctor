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

type sessionReauthenticationHTTPFake struct {
	calls      int
	invocation application.Invocation
	command    application.ReauthenticatePasswordCommand
}

func (f *sessionReauthenticationHTTPFake) ReauthenticatePassword(_ context.Context, invocation application.Invocation, command application.ReauthenticatePasswordCommand) (*model.Session, error) {
	f.calls++
	f.invocation = invocation
	f.command = command
	return &model.Session{ID: invocation.Principal().SessionID, ReauthenticatedAt: model.OptionalTimeFrom(time.Now())}, nil
}

func TestPasswordReauthenticationHTTPRejectsIdentitySelectionAndProtectsResponse(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now().Add(-time.Hour)}
	fake := &sessionReauthenticationHTTPFake{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, sessionReauthenticationResource(fake, browserCookies{}))
	for _, test := range []struct {
		body   string
		status int
	}{{`{"password":"secret","user_id":"other"}`, 400}, {`{"password":"secret","login_id":"other@example.edu"}`, 400}, {`{"password":"secret"}`, 200}} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reauthenticate/password", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if response.Code == 200 && (response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"reauthenticated_at"`)) {
			t.Fatal("fresh-proof response not protected/projected")
		}
	}
	if fake.calls != 1 || fake.invocation.Principal().UserID != principal.UserID || fake.command.Password != "secret" {
		t.Fatal("transport did not bind the immutable current identity")
	}
}

func (*sessionReauthenticationHTTPFake) BeginExternalReauthentication(context.Context, application.Invocation, application.BeginExternalReauthenticationCommand) (*model.ExternalAuthenticationStart, error) {
	return nil, nil
}
