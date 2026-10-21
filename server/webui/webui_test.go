// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/webui"
)

func TestHandlerServesOnlyDeclaredPagesAndAssets(t *testing.T) {
	t.Parallel()
	handler := newHandler(t)

	for _, page := range []string{"/login", "/account/reset-password"} {
		response := perform(t, handler, http.MethodGet, page)
		require.Equal(t, http.StatusOK, response.Code, "GET %s", page)
		require.Equal(t, "<!doctype html><title>Proctor</title>", response.Body.String(), "GET %s", page)
		assertSecurityHeaders(t, response)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "GET %s Cache-Control", page)
	}

	asset := perform(t, handler, http.MethodGet, "/assets/index-AbCdEf12.js")
	require.Equal(t, http.StatusOK, asset.Code, "asset")
	require.Equal(t, "export {};", asset.Body.String(), "asset")
	require.Equal(t, "public, max-age=31536000, immutable", asset.Header().Get("Cache-Control"))

	for _, missing := range []string{"/api/v1/discovery", "/unknown", "/assets/missing.js", "/assets/unversioned-logo.svg", "/assets/"} {
		response := perform(t, handler, http.MethodGet, missing)
		require.Equal(t, http.StatusNotFound, response.Code, "GET %s", missing)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "GET %s Cache-Control", missing)
	}
}

func TestHandlerImplementsHEADAndRejectsMutations(t *testing.T) {
	t.Parallel()
	handler := newHandler(t)

	head := perform(t, handler, http.MethodHead, "/login")
	require.Equal(t, http.StatusOK, head.Code, "HEAD /login = %d body=%q length", head.Code, head.Body.String())
	require.Equal(t, 0, head.Body.Len(), "HEAD /login = %d body=%q length", head.Code, head.Body.String())
	require.NotEmpty(t, head.Header().Get("Content-Length"), "HEAD /login = %d body=%q length", head.Code, head.Body.String())
	mutation := perform(t, handler, http.MethodPost, "/login")
	require.Equal(t, http.StatusMethodNotAllowed, mutation.Code, "POST /login")
	require.Equal(t, "GET, HEAD", mutation.Header().Get("Allow"), "POST /login")
}

func TestHandlerRedirectsRootToHostedLogin(t *testing.T) {
	t.Parallel()
	require.True(t, webui.HandlesPath(webui.LoginPath), "browser module does not own the root and login paths")
	require.True(t, webui.HandlesPath("/"), "browser module does not own the root and login paths")
	handler := newHandler(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := perform(t, handler, method, "/")
		require.Equal(t, http.StatusFound, response.Code, "%s / = %d Location", method, response.Code)
		require.Equal(t, webui.LoginPath, response.Header().Get("Location"), "%s / = %d Location", method, response.Code)
	}
}

func TestHandlerDelegatesRefusalsToConfiguredProblemHandlers(t *testing.T) {
	t.Parallel()
	var refusals []string
	record := func(name string) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			refusals = append(refusals, name+" "+request.Method+" "+request.URL.Path+" allow="+writer.Header().Get("Allow"))
			writer.WriteHeader(http.StatusTeapot)
		})
	}
	handler, err := webui.New(testFiles("1.2.3", "abc"), webui.Options{
		BuildVersion: "1.2.3", BuildCommit: "abc",
		NotFound: record("not-found"), MethodNotAllowed: record("method-not-allowed"),
	})
	require.NoError(t, err)
	perform(t, handler, http.MethodPost, "/login")
	perform(t, handler, http.MethodPost, "/")
	perform(t, handler, http.MethodGet, "/assets/missing-AbCdEf12.js")
	perform(t, handler, http.MethodGet, "/assets/unversioned-logo.svg")
	want := []string{
		"method-not-allowed POST /login allow=GET, HEAD",
		"method-not-allowed POST / allow=GET, HEAD",
		"not-found GET /assets/missing-AbCdEf12.js allow=",
		"not-found GET /assets/unversioned-logo.svg allow=",
	}
	require.Equal(t, strings.Join(want, "|"), strings.Join(refusals, "|"), "refusals = %q, want %q", refusals, want)
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
			require.Error(t, err, "New() error")
			require.Contains(t, err.Error(), tt.want, "New() error")
		})
	}
}

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := webui.New(testFiles("1.2.3", "abc"), webui.Options{BuildVersion: "1.2.3", BuildCommit: "abc"})
	require.NoError(t, err)
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
		assert.NotEmpty(t, response.Header().Get(name), "%s is missing", name)
	}
}
