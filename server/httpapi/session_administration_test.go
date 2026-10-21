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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAdminSessionListUsesApplicationQueryAndOmitsCredentials(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewID()
	sessionID := model.NewID()
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
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, model.UserID(userID), sessions.listQuery.UserID)
	require.True(t, sessions.listQuery.IncludeRevoked)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body, 1)
	require.Equal(t, sessionID, body[0]["id"])
	for _, key := range []string{"token", "access_token", "refresh_token", "credential"} {
		_, exposed := body[0][key]
		require.False(t, exposed, "credential field")
	}
}

func TestAdminSessionRevokeUsesApplicationCommand(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewID()
	sessionID := model.NewID()
	sessions := &sessionAdministrationHTTPApplication{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(&accountStateHTTPApplication{}, sessions))
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+userID+"/sessions/"+sessionID, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, model.UserID(userID), sessions.revokeCommand.UserID)
	require.Equal(t, model.SessionID(sessionID), sessions.revokeCommand.SessionID)
}
