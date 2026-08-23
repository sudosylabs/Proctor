// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sudosylabs/proctor/server/webui"
)

func TestHandlerServesOnlyDeclaredPagesAndAssets(t *testing.T) {
	t.Parallel()
	handler := newHandler(t)

	for _, page := range []string{"/login", "/account/reset-password"} {
		response := perform(t, handler, http.MethodGet, page)
		if response.Code != http.StatusOK || response.Body.String() != "<!doctype html><title>Proctor</title>" {
			t.Fatalf("GET %s = %d %q", page, response.Code, response.Body.String())
		}
		assertSecurityHeaders(t, response)
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q", page, got)
		}
	}

	asset := perform(t, handler, http.MethodGet, "/assets/index-AbCdEf12.js")
	if asset.Code != http.StatusOK || asset.Body.String() != "export {};" {
		t.Fatalf("asset = %d %q", asset.Code, asset.Body.String())
	}
	if got := asset.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q", got)
	}

	for _, missing := range []string{"/", "/api/v1/discovery", "/unknown", "/assets/missing.js", "/assets/unversioned-logo.svg", "/assets/"} {
		response := perform(t, handler, http.MethodGet, missing)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", missing, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q", missing, got)
		}
	}
}

func TestHandlerImplementsHEADAndRejectsMutations(t *testing.T) {
	t.Parallel()
	handler := newHandler(t)

	head := perform(t, handler, http.MethodHead, "/login")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD /login = %d body=%q length=%q", head.Code, head.Body.String(), head.Header().Get("Content-Length"))
	}
	mutation := perform(t, handler, http.MethodPost, "/login")
	if mutation.Code != http.StatusMethodNotAllowed || mutation.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /login = %d Allow=%q", mutation.Code, mutation.Header().Get("Allow"))
	}
}

func TestNewRejectsIncompleteAndMismatchedDistributions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{name: "manifest missing", files: fstest.MapFS{}, want: "read webapp build manifest"},
		{name: "manifest malformed", files: fstest.MapFS{webui.BuildManifestName: {Data: []byte(`{`)}}, want: "decode webapp build manifest"},
		{name: "build mismatch", files: testFiles("other", "commit"), want: "does not match server build"},
		{name: "index missing", files: fstest.MapFS{webui.BuildManifestName: {Data: []byte(`{"schema_version":1,"version":"1.2.3","commit":"abc"}`)}}, want: "read webapp index"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := webui.New(tt.files, webui.Options{BuildVersion: "1.2.3", BuildCommit: "abc"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := webui.New(testFiles("1.2.3", "abc"), webui.Options{BuildVersion: "1.2.3", BuildCommit: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testFiles(version, commit string) fstest.MapFS {
	return fstest.MapFS{
		webui.BuildManifestName:       {Data: []byte(`{"schema_version":1,"version":"` + version + `","commit":"` + commit + `"}`)},
		"index.html":                  {Data: []byte("<!doctype html><title>Proctor</title>")},
		"assets/index-AbCdEf12.js":    {Data: []byte("export {};")},
		"assets/unversioned-logo.svg": {Data: []byte("<svg></svg>")},
	}
}

func perform(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if body, err := io.ReadAll(response.Result().Body); err == nil {
		response.Body.Reset()
		_, _ = response.Body.Write(body)
	}
	return response
}

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{
		"Content-Security-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy",
		"Permissions-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options",
	} {
		if response.Header().Get(name) == "" {
			t.Errorf("%s is missing", name)
		}
	}
}
