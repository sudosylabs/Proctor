// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

// TestOversizedJSONBodyReturnsContentTooLargeProblem covers a body whose
// length is unknown until the decoder reads past the route limit. The
// operation maps its decode failure to request.invalid, but the kernel still
// answers 413 because the cause is the body limit.
func TestOversizedJSONBodyReturnsContentTooLargeProblem(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	decode := func(request operationRequest) (operationResult, error) {
		var body map[string]string
		if err := decodeRequestJSON(request.request, &body); err != nil {
			return operationResult{}, invalidRequestError(err)
		}
		return noContentResult(), nil
	}
	resources := []resource{newResource(
		"bounded",
		route(AuthPublic, http.MethodPost, apiPath(literal("bounded")), []string{"request.invalid"}, decode),
	)}
	cookies, err := newBrowserCookies("http://localhost:8065")
	if err != nil {
		t.Fatal(err)
	}
	httpAPI := &API{authenticator: classRouteAuthenticator{}, logger: logger, localizer: newTestLocalizer(t), cookies: cookies, recentAuthenticationTTL: time.Minute}
	if err := httpAPI.buildRoutingKernel(model.APIURLSuffix, 1024, func() error {
		return httpAPI.collectResources(model.APIURLSuffix, resources...)
	}); err != nil {
		t.Fatal(err)
	}

	for name, configure := range map[string]func(*http.Request){
		"declared length": func(*http.Request) {},
		"unknown length":  func(request *http.Request) { request.ContentLength = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"value":"` + strings.Repeat("a", 2048) + `"}`
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/bounded", io.MultiReader(strings.NewReader(body)))
			request.ContentLength = int64(len(body))
			configure(request)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			var problem Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem %q: %v", response.Body.String(), err)
			}
			if response.Code != http.StatusRequestEntityTooLarge || problem.Status != http.StatusRequestEntityTooLarge ||
				problem.Code != "request.too_large" || problem.Title != "Request too large" ||
				problem.RequestID == "" || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("oversized body = %d %#v", response.Code, problem)
			}
		})
	}

	small := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/bounded", strings.NewReader(`{"value":"ok"}`))
	small.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, small)
	if response.Code != http.StatusNoContent {
		t.Fatalf("bounded body = %d %s", response.Code, response.Body.String())
	}
}
