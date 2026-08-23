// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	transport, err := newRootHTTPTransport(owned, api, webapp)
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
	transport, err := newRootHTTPTransport(owned, http.NotFoundHandler(), http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); !errors.Is(err, closeErr) || owned.closed != 1 {
		t.Fatalf("Close() = %v, closes=%d", err, owned.closed)
	}
}
