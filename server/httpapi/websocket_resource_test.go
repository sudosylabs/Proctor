// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type focusedWebSocketTransport struct {
	principal          model.Principal
	metadata           model.RequestMetadata
	connectionID       string
	sequence           int64
	allowMissingOrigin bool
	acceptCalls        int
	acceptErr          error
}

func (t *focusedWebSocketTransport) Accept(
	writer http.ResponseWriter,
	_ *http.Request,
	principal model.Principal,
	metadata model.RequestMetadata,
	connectionID string,
	sequence int64,
	allowMissingOrigin bool,
) error {
	t.principal = principal
	t.metadata = metadata
	t.connectionID = connectionID
	t.sequence = sequence
	t.allowMissingOrigin = allowMissingOrigin
	t.acceptCalls++
	if t.acceptErr != nil {
		return t.acceptErr
	}
	writer.WriteHeader(http.StatusSwitchingProtocols)
	return nil
}

func TestWebSocketResourceMapsDeclaredAndRejectsUndeclaredTransportFailures(t *testing.T) {
	t.Parallel()

	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: time.Now(), ClientType: model.SessionClientCLI,
	}
	tests := []struct {
		name string
		code string
		want int
	}{
		{name: "origin", code: "websocket.origin.invalid", want: http.StatusForbidden},
		{name: "unavailable", code: "websocket.unavailable", want: http.StatusServiceUnavailable},
		{name: "undeclared", code: "user.conflict", want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			logger, _ := newTestLogger(t)
			transport := &focusedWebSocketTransport{acceptErr: apperror.New(apperror.Code(test.code))}
			httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, webSocketResource(transport))

			request := httptest.NewRequest(http.MethodGet, "/api/v1/websocket", nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, test.want, response.Code, "upgrade failure = %d/%d: %s", response.Code, transport.acceptCalls, response.Body.String())
			require.Equal(t, 1, transport.acceptCalls, "upgrade failure = %d/%d: %s", response.Code, transport.acceptCalls, response.Body.String())
		})
	}
}

func (*focusedWebSocketTransport) Close() error { return nil }

func TestWebSocketResourceRunsNamedUpgradeThroughRoutingKernel(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: time.Now(), ClientType: model.SessionClientCLI,
	}
	transport := &focusedWebSocketTransport{}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		webSocketResource(transport),
	)

	routes := httpAPI.Routes()
	require.Len(t, routes, 1)
	require.Equal(t, "/api/v1/websocket", routes[0].Path)
	require.Equal(t, AuthSessionRequired, routes[0].Auth)
	require.Equal(t, "websocket-upgrade", routes[0].ProtocolName)
	require.Equal(t, RouteProtocolUpgrade, routes[0].ProtocolKind)

	connectionID := model.NewID()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/websocket?connection_id="+connectionID+"&sequence_number=7",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set(RequestIDHeader, "websocket-request")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusSwitchingProtocols, response.Code, "upgrade status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, 1, transport.acceptCalls)
	require.Equal(t, principal.UserID, transport.principal.UserID)
	require.NotEqual(t, "websocket-request", transport.metadata.RequestID)
	require.Equal(t, response.Header().Get(RequestIDHeader), transport.metadata.RequestID)
	require.Equal(t, connectionID, transport.connectionID)
	require.Equal(t, int64(7), transport.sequence)
	require.True(t, transport.allowMissingOrigin)
}

func TestWebSocketResourceRejectsMissingCredentialAndInvalidResume(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: time.Now(), ClientType: model.SessionClientCLI,
	}
	transport := &focusedWebSocketTransport{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, webSocketResource(transport))

	missing := httptest.NewRecorder()
	httpAPI.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/websocket", nil))
	require.Equal(t, http.StatusUnauthorized, missing.Code, "missing credential = %d/%d: %s", missing.Code, transport.acceptCalls, missing.Body.String())
	require.Equal(t, 0, transport.acceptCalls, "missing credential = %d/%d: %s", missing.Code, transport.acceptCalls, missing.Body.String())

	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/v1/websocket?connection_id=invalid&sequence_number=7", nil)
	invalidRequest.Header.Set("Authorization", "Bearer credential")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, invalidRequest)
	require.Equal(t, http.StatusBadRequest, invalid.Code, "invalid resume = %d/%d: %s", invalid.Code, transport.acceptCalls, invalid.Body.String())
	require.Equal(t, 0, transport.acceptCalls, "invalid resume = %d/%d: %s", invalid.Code, transport.acceptCalls, invalid.Body.String())
}
