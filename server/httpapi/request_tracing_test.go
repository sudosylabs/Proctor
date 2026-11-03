// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type requestSpanFake struct {
	mu      sync.Mutex
	request TracedRequest
	route   string
	ended   []int
}

func (s *requestSpanFake) SetRoute(route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.route = route
}

func (s *requestSpanFake) End(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = append(s.ended, status)
}

type requestTracingFake struct {
	spans []*requestSpanFake
}

type tracedContextKey struct{}

func (f *requestTracingFake) StartRequest(ctx context.Context, request TracedRequest) (context.Context, RequestSpan) {
	span := &requestSpanFake{request: request}
	f.spans = append(f.spans, span)
	return context.WithValue(ctx, tracedContextKey{}, span), span
}

func TestRequestTracingContinuesParentsOnlyFromTrustedProxies(t *testing.T) {
	t.Parallel()

	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, test := range []struct {
		name           string
		remoteAddress  string
		continueParent bool
	}{
		{name: "untrusted client", remoteAddress: "203.0.113.9:4000", continueParent: false},
		{name: "trusted proxy", remoteAddress: "10.0.0.5:4000", continueParent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tracing := &requestTracingFake{}
			var inner any
			handler := withMiddleware(nameRequestSpan(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				inner = request.Context().Value(tracedContextKey{})
				writer.WriteHeader(http.StatusNoContent)
			}), "/api/v1/users/{user_id:[0-9a-z]{26}}/affiliations"), discardLogger{}, middlewarePolicy{trustedProxies: proxies, tracing: tracing})
			request := httptest.NewRequestWithContext(t.Context(), "PROPFIND", "/api/v1/users/abc/affiliations?token=secret", nil)
			request.RemoteAddr = test.remoteAddress
			request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			require.Len(t, tracing.spans, 1)
			span := tracing.spans[0]
			require.Same(t, span, inner, "handler context carries the request span")
			require.Equal(t, test.continueParent, span.request.ContinueParent)
			require.Equal(t, "OTHER", span.request.Method, "method is bounded")
			require.Equal(t, response.Header().Get(RequestIDHeader), span.request.RequestID)
			require.Equal(t, "/api/v1/users/{user_id}/affiliations", span.route)
			require.Equal(t, []int{http.StatusNoContent}, span.ended)
		})
	}
}

func TestRequestTracingRecordsPanicsAsServerErrors(t *testing.T) {
	t.Parallel()

	tracing := &requestTracingFake{}
	handler := traceRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler failed")
	}), tracing)
	require.Panics(t, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	})
	require.Equal(t, []int{http.StatusInternalServerError}, tracing.spans[0].ended)
}

type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil, nil
}

func TestRequestTracingEndsUpgradeSpansAtHijack(t *testing.T) {
	t.Parallel()

	tracing := &requestTracingFake{}
	hijacked := make(chan struct{})
	release := make(chan struct{})
	handler := traceRequests(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			panic(errors.New("recorder lost the hijacker"))
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			panic(err)
		}
		defer func() { _ = connection.Close() }()
		close(hijacked)
		<-release
	}), tracing)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(hijackableRecorder{httptest.NewRecorder()}, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/websocket", nil))
	}()
	<-hijacked
	span := tracing.spans[0]
	span.mu.Lock()
	ended := append([]int(nil), span.ended...)
	span.mu.Unlock()
	require.Equal(t, []int{http.StatusSwitchingProtocols}, ended, "span ended at the handshake, not the connection's end")
	close(release)
	<-done
}

func TestRouteTemplateKeepsOnlyParameterNames(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"/health/live":                         "/health/live",
		"/api/v1/users/{user_id:[0-9a-z]{26}}": "/api/v1/users/{user_id}",
		"/api/v1/exams/{exam_id:[a-z]+}/sittings/{sequence:[1-9][0-9]{0,15}}": "/api/v1/exams/{exam_id}/sittings/{sequence}",
	} {
		require.Equal(t, want, routeTemplate(path), path)
	}
}
