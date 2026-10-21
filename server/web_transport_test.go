// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type routingHandler struct {
	name string
	seen int
}

func (h *routingHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	h.seen++
	_, _ = writer.Write([]byte(h.name))
}

type routingTransport struct {
	routingHandler
	closeErr error
	closed   int
}

func (t *routingTransport) Close() error {
	t.closed++
	return t.closeErr
}

func TestRootHTTPTransportDispatchesWithoutCrossFallback(t *testing.T) {
	t.Parallel()
	owned := &routingTransport{routingHandler: routingHandler{name: "owned"}}
	api := &routingHandler{name: "api"}
	webapp := &routingHandler{name: "webapp"}
	transport, err := newRootHTTPTransport(owned, api, webapp, "")
	require.NoError(t, err)

	for _, tt := range []struct {
		path string
		want string
	}{
		{path: "/api", want: "api"},
		{path: "/api/v1/discovery", want: "api"},
		{path: "/health/live", want: "api"},
		{path: "/missing", want: "api"},
		{path: "/", want: "webapp"},
		{path: "/login", want: "webapp"},
		{path: "/assets/index-AbCdEf12.js", want: "webapp"},
		{path: "/assets/index.js", want: "api"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			transport.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))
			require.Equal(t, tt.want, response.Body.String(), "GET %s reached", tt.path)
		})
	}
	require.Equal(t, 5, api.seen, "dispatch counts api")
	require.Equal(t, 3, webapp.seen, "dispatch counts api")
	require.Equal(t, 0, owned.seen, "dispatch counts api")
}

func TestRootHTTPTransportRetainsOnlyOwnedLifecycle(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close failed")
	owned := &routingTransport{closeErr: closeErr}
	transport, err := newRootHTTPTransport(owned, http.NotFoundHandler(), http.NotFoundHandler(), "")
	require.NoError(t, err)
	require.ErrorIs(t, transport.Close(), closeErr, "Close()")
	require.Equal(t, 1, owned.closed, "Close()")
}

func TestRootHTTPTransportAppliesStrictTransportSecurityToEveryResponse(t *testing.T) {
	t.Parallel()
	owned := &routingTransport{}
	transport, err := newRootHTTPTransport(owned, &routingHandler{name: "api"}, &routingHandler{name: "webapp"}, "max-age=31536000")
	require.NoError(t, err)
	for _, path := range []string{"/api/v1/discovery", "/login", "/missing"} {
		response := httptest.NewRecorder()
		transport.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		require.Equal(t, "max-age=31536000", response.Header().Get("Strict-Transport-Security"), "GET %s Strict-Transport-Security", path)
	}

	plain, err := newRootHTTPTransport(owned, &routingHandler{name: "api"}, &routingHandler{name: "webapp"}, "")
	require.NoError(t, err)
	response := httptest.NewRecorder()
	plain.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil))
	require.Empty(t, response.Header().Values("Strict-Transport-Security"))
}
