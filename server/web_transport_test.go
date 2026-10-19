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
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		path string
		want string
	}{
		{path: "/api", want: "api"},
		{path: "/api/v1/discovery", want: "api"},
		{path: "/health/live", want: "api"},
		{path: "/missing", want: "api"},
		{path: "/", want: "api"},
		{path: "/login", want: "webapp"},
		{path: "/assets/index-AbCdEf12.js", want: "webapp"},
		{path: "/assets/index.js", want: "api"},
	} {
		response := httptest.NewRecorder()
		transport.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if response.Body.String() != tt.want {
			t.Fatalf("GET %s reached %q, want %q", tt.path, response.Body.String(), tt.want)
		}
	}
	if api.seen != 6 || webapp.seen != 2 || owned.seen != 0 {
		t.Fatalf("dispatch counts api=%d webapp=%d owned=%d", api.seen, webapp.seen, owned.seen)
	}
}

func TestRootHTTPTransportRetainsOnlyOwnedLifecycle(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close failed")
	owned := &routingTransport{closeErr: closeErr}
	transport, err := newRootHTTPTransport(owned, http.NotFoundHandler(), http.NotFoundHandler(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); !errors.Is(err, closeErr) || owned.closed != 1 {
		t.Fatalf("Close() = %v, closes=%d", err, owned.closed)
	}
}

func TestRootHTTPTransportAppliesStrictTransportSecurityToEveryResponse(t *testing.T) {
	t.Parallel()
	owned := &routingTransport{}
	transport, err := newRootHTTPTransport(owned, &routingHandler{name: "api"}, &routingHandler{name: "webapp"}, "max-age=31536000")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/discovery", "/login", "/missing"} {
		response := httptest.NewRecorder()
		transport.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if got := response.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
			t.Fatalf("GET %s Strict-Transport-Security = %q", path, got)
		}
	}

	plain, err := newRootHTTPTransport(owned, &routingHandler{name: "api"}, &routingHandler{name: "webapp"}, "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	plain.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/login", nil))
	if got := response.Header().Values("Strict-Transport-Security"); len(got) != 0 {
		t.Fatalf("plain HTTP Strict-Transport-Security = %q", got)
	}
}
