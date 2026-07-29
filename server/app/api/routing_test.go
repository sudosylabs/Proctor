// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatcherExtractsParametersAndPrefersLiteralRoutes(t *testing.T) {
	t.Parallel()

	dispatcher := &dispatcher{keys: make(map[string]struct{})}
	parameterSegments, _, err := compileRoutePath("/resources/{resource_id}")
	if err != nil {
		t.Fatal(err)
	}
	literalSegments, _, err := compileRoutePath("/resources/search")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.routes = []registeredRoute{
		{
			route:       Route{Method: http.MethodPost, Path: "/resources/{resource_id}"},
			segments:    parameterSegments,
			specificity: routeSpecificity(parameterSegments),
			handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeJSON(writer, http.StatusOK, map[string]string{
					"id": request.PathValue("resource_id"),
				})
			}),
		},
		{
			route:       Route{Method: http.MethodGet, Path: "/resources/search"},
			segments:    literalSegments,
			specificity: routeSpecificity(literalSegments),
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
		},
	}

	parameterRequest := httptest.NewRequest(http.MethodPost, "/resources/"+modelIDForRouteTest, nil)
	parameterResponse := httptest.NewRecorder()
	dispatcher.ServeHTTP(parameterResponse, parameterRequest)
	if parameterResponse.Code != http.StatusOK ||
		parameterRequest.PathValue("resource_id") != modelIDForRouteTest {
		t.Fatalf(
			"parameter route status/id = %d/%q",
			parameterResponse.Code,
			parameterRequest.PathValue("resource_id"),
		)
	}

	literalMethodMismatch := httptest.NewRequest(http.MethodPost, "/resources/search", nil)
	literalResponse := httptest.NewRecorder()
	dispatcher.ServeHTTP(literalResponse, literalMethodMismatch)
	if literalResponse.Code != http.StatusMethodNotAllowed ||
		literalResponse.Header().Get("Allow") != http.MethodGet {
		t.Fatalf(
			"literal route mismatch status/allow = %d/%q",
			literalResponse.Code,
			literalResponse.Header().Get("Allow"),
		)
	}
}

func TestCompileRoutePathRejectsAmbiguousShapes(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"relative",
		"/trailing/",
		"/duplicate/{id}/{id}",
		"/broken/{id",
	} {
		if _, _, err := compileRoutePath(path); err == nil {
			t.Errorf("compileRoutePath(%q) succeeded", path)
		}
	}
}

func TestRegisterRejectsMissingPolicyAndDuplicatePattern(t *testing.T) {
	t.Parallel()

	httpAPI := &API{dispatcher: &dispatcher{keys: make(map[string]struct{})}}
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if err := httpAPI.Register(
		Route{Method: http.MethodGet, Path: "/resources/{id}", Auth: AuthPublic},
		handler,
	); err != nil {
		t.Fatal(err)
	}
	if err := httpAPI.Register(
		Route{Method: http.MethodGet, Path: "/resources/{other}", Auth: AuthPublic},
		handler,
	); err == nil {
		t.Fatal("duplicate route shape was accepted")
	}
	if err := httpAPI.Register(
		Route{Method: http.MethodPost, Path: "/resources", Auth: ""},
		handler,
	); err == nil {
		t.Fatal("route without authentication policy was accepted")
	}
}

const modelIDForRouteTest = "3uuuid3i7bexfcbzmo6s5w4zqo"
