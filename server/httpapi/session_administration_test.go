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
	require.Equal(t, 50, sessions.listQuery.Limit)
	var body struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	require.Equal(t, sessionID, body.Items[0]["id"])
	require.Nil(t, body.NextCursor, "a short page is the final page")
	for _, key := range []string{"token", "access_token", "refresh_token", "credential"} {
		_, exposed := body.Items[0][key]
		require.False(t, exposed, "credential field")
	}
}

func TestAdminSessionListPagesWithAnOpaqueCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType:             model.SessionClientCLI, AuthenticatedAt: time.Now(),
	}
	userID := model.NewUserID()
	created := time.Date(2026, 8, 12, 9, 30, 0, 123_456_000, time.UTC)
	last := &model.Session{
		ID: model.NewSessionID(), UserID: userID, ClientType: model.SessionClientCLI,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: created, CreatedAt: created, UpdatedAt: created, LastActivityAt: created,
		IdleExpiresAt: created.Add(time.Hour), ExpiresAt: created.Add(2 * time.Hour),
	}
	sessions := &sessionAdministrationHTTPApplication{list: []*model.Session{last}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userAdministrationResource(&accountStateHTTPApplication{}, sessions))
	serve := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer credential")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		return response
	}
	response := serve("/api/v1/users/" + userID.String() + "/sessions?include_revoked=true&limit=1")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		NextCursor string `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEmpty(t, body.NextCursor, "a full page continues")

	response = serve("/api/v1/users/" + userID.String() + "/sessions?include_revoked=true&limit=1&cursor=" + body.NextCursor)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, sessions.listQuery.BeforeCreatedAt.Equal(created), "cursor keeps microseconds: %v", sessions.listQuery.BeforeCreatedAt)
	require.Equal(t, last.ID, sessions.listQuery.BeforeID)

	for _, query := range []string{"limit=0", "limit=201", "cursor=not-a-cursor"} {
		response = serve("/api/v1/users/" + userID.String() + "/sessions?" + query)
		require.Equal(t, http.StatusBadRequest, response.Code, "%s: %s", query, response.Body.String())
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
