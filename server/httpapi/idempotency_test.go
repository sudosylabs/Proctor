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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdempotencyHeaderPolicy(t *testing.T) {
	logger, _ := newTestLogger(t)
	api := &API{logger: logger}
	tests := []struct {
		name       string
		mode       IdempotencyRequirement
		values     []string
		wantStatus int
		wantKey    string
	}{
		{name: "optional omitted", mode: IdempotencyOptional, wantStatus: http.StatusNoContent},
		{name: "optional valid", mode: IdempotencyOptional, values: []string{"save_01.A~z"}, wantStatus: http.StatusNoContent, wantKey: "save_01.A~z"},
		{name: "required omitted", mode: IdempotencyRequired, wantStatus: http.StatusBadRequest},
		{name: "unsupported", mode: IdempotencyNone, values: []string{"key"}, wantStatus: http.StatusBadRequest},
		{name: "invalid characters", mode: IdempotencyOptional, values: []string{"not valid"}, wantStatus: http.StatusBadRequest},
		{name: "duplicate fields", mode: IdempotencyOptional, values: []string{"one", "two"}, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/command", nil)
			for _, value := range test.values {
				request.Header.Add(idempotencyHeader, value)
			}
			response := httptest.NewRecorder()
			api.withIdempotency(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assert.Equal(t, test.wantKey, idempotencyKeyFromContext(request.Context()), "key")
				writer.WriteHeader(http.StatusNoContent)
			}), test.mode).ServeHTTP(response, request)
			require.Equal(t, test.wantStatus, response.Code, "status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
		})
	}
}

func TestRetryableRoutesDeclareIdempotency(t *testing.T) {
	api := newRoutingTestAPI("/api/v1")
	require.NoError(t, api.collectResources("/api/v1", academicPeriodResource(nil), academicUnitResource(nil)))
	want := map[string]bool{"POST /api/v1/academic-periods": true, "POST /api/v1/academic-units": true}
	for _, route := range api.Routes() {
		key := route.Method + " " + route.Path
		if want[key] {
			require.Equal(t, IdempotencyOptional, route.Idempotency, "%s idempotency", key)
			delete(want, key)
		}
	}
	require.Empty(t, want)
}
