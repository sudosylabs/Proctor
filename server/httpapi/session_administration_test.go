// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAdminSessionListUsesApplicationQueryAndOmitsCredentials(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewId()
	sessionID := model.NewId()
	sessions := &sessionAdministrationHTTPApplication{
		list: []*model.Session{{
			ID: model.SessionID(sessionID), UserID: model.UserID(userID), ClientType: model.SessionClientCLI,
			AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
			AuthenticatedAt: model.TimeFromMillis(100), CreatedAt: model.TimeFromMillis(100),
			UpdatedAt: model.TimeFromMillis(100), LastActivityAt: model.TimeFromMillis(100),
			IdleExpiresAt: model.TimeFromMillis(200), ExpiresAt: model.TimeFromMillis(300),
		}},
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(&accountStateHTTPApplication{}, sessions))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/sessions?include_revoked=true", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if sessions.listQuery.UserID != userID || !sessions.listQuery.IncludeRevoked {
		t.Fatalf("query = %#v", sessions.listQuery)
	}
	var body []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0]["id"] != sessionID {
		t.Fatalf("body = %#v", body)
	}
	for _, key := range []string{"token", "access_token", "refresh_token", "credential"} {
		if _, exposed := body[0][key]; exposed {
			t.Fatalf("credential field %q exposed: %#v", key, body[0])
		}
	}
}

func TestAdminSessionRevokeUsesApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewId()
	sessionID := model.NewId()
	sessions := &sessionAdministrationHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(&accountStateHTTPApplication{}, sessions))
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID+"/sessions/"+sessionID, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if sessions.revokeCommand.UserID != userID || sessions.revokeCommand.SessionID != sessionID {
		t.Fatalf("command = %#v", sessions.revokeCommand)
	}
}
