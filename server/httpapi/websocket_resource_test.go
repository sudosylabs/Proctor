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

	application "github.com/sudosylabs/proctor/server/app"
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

func (transport *focusedWebSocketTransport) Accept(
	writer http.ResponseWriter,
	_ *http.Request,
	principal model.Principal,
	metadata model.RequestMetadata,
	connectionID string,
	sequence int64,
	allowMissingOrigin bool,
) error {
	transport.principal = principal
	transport.metadata = metadata
	transport.connectionID = connectionID
	transport.sequence = sequence
	transport.allowMissingOrigin = allowMissingOrigin
	transport.acceptCalls++
	if transport.acceptErr != nil {
		return transport.acceptErr
	}
	writer.WriteHeader(http.StatusSwitchingProtocols)
	return nil
}

func TestWebSocketResourceMapsDeclaredAndRejectsUndeclaredTransportFailures(t *testing.T) {
	t.Parallel()

	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewId()),
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
			transport := &focusedWebSocketTransport{acceptErr: application.NewError(test.code)}
			httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, webSocketResource(transport))

			request := httptest.NewRequest(http.MethodGet, "/api/v1/websocket", nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != test.want || transport.acceptCalls != 1 {
				t.Fatalf("upgrade failure = %d/%d: %s", response.Code, transport.acceptCalls, response.Body.String())
			}
		})
	}
}

func (*focusedWebSocketTransport) Close() error { return nil }

func TestWebSocketResourceRunsNamedUpgradeThroughRoutingKernel(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewId()),
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
	if len(routes) != 1 || routes[0].Path != "/api/v1/websocket" ||
		routes[0].Auth != AuthSessionRequired ||
		routes[0].ProtocolName != "websocket-upgrade" ||
		routes[0].ProtocolKind != RouteProtocolUpgrade {
		t.Fatalf("WebSocket manifest = %#v", routes)
	}

	connectionID := model.NewId()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/websocket?connection_id="+connectionID+"&sequence_number=7",
		nil,
	)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set(RequestIDHeader, "websocket-request")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d: %s", response.Code, response.Body.String())
	}
	if transport.acceptCalls != 1 || transport.principal.UserID != principal.UserID ||
		transport.metadata.RequestID != "websocket-request" ||
		transport.connectionID != connectionID || transport.sequence != 7 ||
		!transport.allowMissingOrigin {
		t.Fatalf("upgrade invocation = %#v", transport)
	}
}

func TestWebSocketResourceRejectsMissingCredentialAndInvalidResume(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewId()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: time.Now(), ClientType: model.SessionClientCLI,
	}
	transport := &focusedWebSocketTransport{}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, webSocketResource(transport))

	missing := httptest.NewRecorder()
	httpAPI.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/websocket", nil))
	if missing.Code != http.StatusUnauthorized || transport.acceptCalls != 0 {
		t.Fatalf("missing credential = %d/%d: %s", missing.Code, transport.acceptCalls, missing.Body.String())
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/v1/websocket?connection_id=invalid&sequence_number=7", nil)
	invalidRequest.Header.Set("Authorization", "Bearer credential")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest || transport.acceptCalls != 0 {
		t.Fatalf("invalid resume = %d/%d: %s", invalid.Code, transport.acceptCalls, invalid.Body.String())
	}
}
