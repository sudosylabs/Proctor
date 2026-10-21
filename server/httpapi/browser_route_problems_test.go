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

	"github.com/stretchr/testify/require"
)

func TestBrowserRouteProblemHandlersWriteProblemDetails(t *testing.T) {
	t.Parallel()
	api, err := New(validHTTPOptions(t))
	require.NoError(t, err)
	tests := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		status  int
		code    string
		allow   string
	}{
		{"not found", api.NotFoundHandler(), http.MethodGet, "/assets/missing-AbCdEf12.js", http.StatusNotFound, "route.not_found", ""},
		{"method not allowed", api.MethodNotAllowedHandler(http.MethodGet, http.MethodHead), http.MethodPost, "/login", http.StatusMethodNotAllowed, "route.method_not_allowed", "GET, HEAD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), test.method, test.path, nil))
			var problem Problem
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem), "decode %q", response.Body.String())
			require.Equal(t, test.status, response.Code, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, test.status, problem.Status, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, test.code, problem.Code, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.NotEmpty(t, problem.Title, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, test.path, problem.Instance, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.NotEmpty(t, problem.RequestID, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, response.Header().Get(RequestIDHeader), problem.RequestID, "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, "application/problem+json", response.Header().Get("Content-Type"), "response = %d %#v headers=%v", response.Code, problem, response.Header())
			require.Equal(t, test.allow, response.Header().Get("Allow"), "response = %d %#v headers=%v", response.Code, problem, response.Header())
		})
	}
}
