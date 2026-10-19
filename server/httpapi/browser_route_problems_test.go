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
)

func TestBrowserRouteProblemHandlersWriteProblemDetails(t *testing.T) {
	t.Parallel()
	api, err := New(validHTTPOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		status  int
		code    string
		allow   string
	}{
		{"not found", api.NotFoundHandler(), http.MethodGet, "/assets/missing-AbCdEf12.js", http.StatusNotFound, "not_found", ""},
		{"method not allowed", api.MethodNotAllowedHandler(http.MethodGet, http.MethodHead), http.MethodPost, "/login", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), test.method, test.path, nil))
			var problem Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode %q: %v", response.Body.String(), err)
			}
			if response.Code != test.status || problem.Status != test.status || problem.Code != test.code ||
				problem.Title == "" || problem.Instance != test.path || problem.RequestID == "" ||
				problem.RequestID != response.Header().Get(RequestIDHeader) ||
				response.Header().Get("Content-Type") != "application/problem+json" ||
				response.Header().Get("Allow") != test.allow {
				t.Fatalf("response = %d %#v headers=%v", response.Code, problem, response.Header())
			}
		})
	}
}
