// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanicRecoveryReturnsSafeProblemAndRetainsDiagnostic(t *testing.T) {
	t.Parallel()

	logger, logs := newTestLogger(t)
	handler := withMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret panic detail")
	}), logger)

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret panic detail") {
		t.Fatal("panic detail leaked to client")
	}
	if !strings.Contains(logs.String(), "secret panic detail") {
		t.Fatal("panic detail was not retained in operational logs")
	}
}
