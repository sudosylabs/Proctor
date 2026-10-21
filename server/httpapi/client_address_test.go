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
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func serveRequestIdentity(t *testing.T, proxies []netip.Prefix, configure func(*http.Request)) (model.RequestMetadata, *httptest.ResponseRecorder) {
	t.Helper()
	var metadata model.RequestMetadata
	handler := withMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		metadata = RequestMetadata(request.Context())
	}), discardLogger{}, middlewarePolicy{trustedProxies: proxies})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/live", nil)
	configure(request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return metadata, response
}

func TestRequestIdentityResolvesClientAddressThroughTrustedProxies(t *testing.T) {
	t.Parallel()

	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	tests := []struct {
		name          string
		proxies       []netip.Prefix
		remoteAddress string
		forwardedFor  []string
		want          string
	}{
		{"no trusted proxies ignores forwarding", nil, "10.0.0.5:4000", []string{"198.51.100.7"}, "10.0.0.5"},
		{"untrusted peer ignores forwarding", proxies, "203.0.113.9:4000", []string{"198.51.100.7"}, "203.0.113.9"},
		{"trusted peer without forwarding", proxies, "10.0.0.5:4000", nil, "10.0.0.5"},
		{"trusted peer uses forwarded client", proxies, "10.0.0.5:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"rightmost untrusted hop wins over forged left hops", proxies, "10.0.0.5:4000", []string{"192.0.2.66, 198.51.100.7, 10.1.1.1"}, "198.51.100.7"},
		{"repeated headers are one hop list", proxies, "10.0.0.5:4000", []string{"192.0.2.66", "198.51.100.7"}, "198.51.100.7"},
		{"all trusted hops use leftmost", proxies, "10.0.0.5:4000", []string{"10.2.2.2, 10.1.1.1"}, "10.2.2.2"},
		{"malformed hop stops at last trusted address", proxies, "10.0.0.5:4000", []string{"198.51.100.7, unknown, 10.1.1.1"}, "10.1.1.1"},
		{"hop with port", proxies, "10.0.0.5:4000", []string{"198.51.100.7:5555"}, "198.51.100.7"},
		{"ipv6 proxy and client", proxies, "[2001:db8::1]:4000", []string{"2001:db8:ffff::9, 2606:4700::1"}, "2606:4700::1"},
		{"ipv4-mapped peer is trusted", proxies, "[::ffff:10.0.0.5]:4000", []string{"198.51.100.7"}, "198.51.100.7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, _ := serveRequestIdentity(t, test.proxies, func(request *http.Request) {
				request.RemoteAddr = test.remoteAddress
				for _, value := range test.forwardedFor {
					request.Header.Add("X-Forwarded-For", value)
				}
				request.Header.Set("Forwarded", "for=192.0.2.200")
			})
			require.Equal(t, test.want, metadata.IPAddress, "client address")
		})
	}
}

func TestRequestIdentityAcceptsRequestIDOnlyFromTrustedProxy(t *testing.T) {
	t.Parallel()

	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	tests := []struct {
		name          string
		remoteAddress string
		requestID     string
		keep          bool
	}{
		{"client value is replaced", "203.0.113.9:4000", "client-chosen-id", false},
		{"trusted proxy value is kept", "10.0.0.5:4000", "proxy-request-id", true},
		{"malformed trusted proxy value is replaced", "10.0.0.5:4000", "bad id\nvalue", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, response := serveRequestIdentity(t, proxies, func(request *http.Request) {
				request.RemoteAddr = test.remoteAddress
				request.Header.Set(RequestIDHeader, test.requestID)
			})
			require.Equal(t, test.keep, metadata.RequestID == test.requestID, "request ID = %q, client supplied %q, keep", metadata.RequestID, test.requestID)
			require.NotEmpty(t, metadata.RequestID, "request ID = %q, client supplied %q, keep", metadata.RequestID, test.requestID)
			require.Equal(t, metadata.RequestID, response.Header().Get(RequestIDHeader), "response request ID")
		})
	}
}
