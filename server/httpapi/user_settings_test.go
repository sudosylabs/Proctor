// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type userSettingsHTTPApplication struct {
	result         app.UserSettingsView
	err            error
	invocation     app.Invocation
	calls          int
	replaceResult  app.UserSettingsReplacementResult
	replaceErr     error
	replaceCommand app.ReplaceOwnUserSettingsCommand
	replaceCalls   int
}

func (a *userSettingsHTTPApplication) ReplaceOwnUserSettings(
	_ context.Context,
	invocation app.Invocation,
	command app.ReplaceOwnUserSettingsCommand,
) (app.UserSettingsReplacementResult, error) {
	a.replaceCalls++
	a.invocation = invocation
	a.replaceCommand = command
	return a.replaceResult, a.replaceErr
}

func (a *userSettingsHTTPApplication) ReadOwnUserSettings(
	_ context.Context,
	invocation app.Invocation,
) (app.UserSettingsView, error) {
	a.calls++
	a.invocation = invocation
	return a.result, a.err
}

func TestUserSettingsHTTPReplacesWithoutEchoingSource(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: at,
	}
	revision := model.NewUserSettingsRevision()
	next := model.NewUserSettingsRevision()
	source := "{\n  // exact\n  \"workbench.colorTheme\": \"Proctor Dark\",\n}\n"
	applicationFake := &userSettingsHTTPApplication{replaceResult: app.UserSettingsReplacementResult{
		Revision: next, FormatVersion: 1, UpdatedAt: at, Changed: true,
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userSettingsResource(applicationFake))
	body, err := json.Marshal(map[string]any{
		"source": source, "format_version": 1, "expected_revision": revision.String(),
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/settings", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session-credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "settings-save-1")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, 1, applicationFake.replaceCalls)
	require.Equal(t, source, applicationFake.replaceCommand.Source)
	require.Equal(t, 1, applicationFake.replaceCommand.FormatVersion)
	require.Equal(t, revision, applicationFake.replaceCommand.ExpectedRevision)
	require.Equal(t, "settings-save-1", applicationFake.replaceCommand.IdempotencyKey)
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	_, exposed := result["source"]
	require.False(t, exposed)
	require.Equal(t, next.String(), result["revision"])
	require.Equal(t, true, result["changed"])
}

func TestUserSettingsHTTPReplacementIsStrictBoundedAndIdempotent(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(),
	}
	revision := model.NewUserSettingsRevision()
	applicationFake := &userSettingsHTTPApplication{replaceResult: app.UserSettingsReplacementResult{
		Revision: revision, FormatVersion: 1, UpdatedAt: time.Now(),
	}}
	newAPI := func() *API {
		return newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userSettingsResource(applicationFake))
	}
	for name, configure := range map[string]func(*http.Request){
		"missing idempotency": func(request *http.Request) {},
		"unknown outer field": func(request *http.Request) {
			request.Header.Set("Idempotency-Key", "strict-save")
		},
		"trailing value": func(request *http.Request) {
			request.Header.Set("Idempotency-Key", "strict-save")
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"source":"{}\\n","format_version":1,"expected_revision":"` + revision.String() + `"}`
			if name == "unknown outer field" {
				body = `{"source":"{}\\n","format_version":1,"expected_revision":"` + revision.String() + `","unknown":true}`
			}
			if name == "trailing value" {
				body += `{}`
			}
			request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/settings", bytes.NewBufferString(body))
			request.Header.Set("Authorization", "Bearer session-credential")
			request.Header.Set("Content-Type", "application/json")
			configure(request)
			response := httptest.NewRecorder()
			newAPI().ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status = %d: %s", response.Code, response.Body.String())
		})
	}
	require.Equal(t, 0, applicationFake.replaceCalls, "invalid requests reached application")

	maxSource := strings.Repeat(" ", model.UserSettingsSourceMaxBytes-3) + "{}\n"
	body, err := json.Marshal(userSettingsReplaceRequest{
		Source: maxSource, FormatVersion: 1, ExpectedRevision: revision.String(),
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/settings", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session-credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "maximum-save")
	response := httptest.NewRecorder()
	newAPI().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "maximum source status/size = %d/%d: %s", response.Code, len(applicationFake.replaceCommand.Source), response.Body.String())
	require.Equal(t, maxSource, applicationFake.replaceCommand.Source, "maximum source status/size = %d/%d: %s", response.Code, len(applicationFake.replaceCommand.Source), response.Body.String())
}

func TestUserSettingsHTTPReadsExactSelfDocumentWithoutCaching(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: at,
	}
	source := "{\n  // exact\n  \"workbench.colorTheme\": \"Proctor Dark\",\n}\n"
	applicationFake := &userSettingsHTTPApplication{result: app.UserSettingsView{
		Source: source, FormatVersion: 1, Revision: model.NewUserSettingsRevision(),
		Writable: true, UpdatedAt: at,
	}}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		userSettingsResource(applicationFake),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/settings", nil)
	request.Header.Set("Authorization", "Bearer session-credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, 1, applicationFake.calls, "calls/invocation = %d/%#v", applicationFake.calls, applicationFake.invocation)
	require.Equal(t, principal.UserID, applicationFake.invocation.Principal().UserID, "calls/invocation = %d/%#v", applicationFake.calls, applicationFake.invocation)
	var body userSettingsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, source, body.Source)
	require.Equal(t, 1, body.FormatVersion)
	require.Equal(t, applicationFake.result.Revision.String(), body.Revision)
	require.True(t, body.Writable)
	require.Equal(t, model.FormatRFC3339(at), body.UpdatedAt)
}

func TestUserSettingsHTTPReadsUnsupportedFormatAsExactReadOnlySource(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:         model.PrincipalCredentialID(model.NewSessionCredentialID()),
		CredentialType:       model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: at,
	}
	const source = "future-format(source => remains.exact);\n"
	applicationFake := &userSettingsHTTPApplication{result: app.UserSettingsView{
		Source: source, FormatVersion: 2, Revision: model.NewUserSettingsRevision(),
		Writable: false, UpdatedAt: at,
	}}
	httpAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: principal},
		userSettingsResource(applicationFake),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/settings", nil)
	request.Header.Set("Authorization", "Bearer session-credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status = %d: %s", response.Code, response.Body.String())
	var body userSettingsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, source, body.Source)
	require.Equal(t, 2, body.FormatVersion)
	require.False(t, body.Writable)
	require.Equal(t, applicationFake.result.Revision.String(), body.Revision)
	require.Equal(t, model.FormatRFC3339(at), body.UpdatedAt)
}

func TestUserSettingsHTTPRejectsNonSessionAndArbitraryUserPaths(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &userSettingsHTTPApplication{}
	patAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{err: apperror.New("authentication.token_invalid")},
		userSettingsResource(applicationFake),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/settings", nil)
	request.Header.Set("Authorization", "Bearer personal-access-token")
	response := httptest.NewRecorder()
	patAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code, "PAT status = %d: %s", response.Code, response.Body.String())

	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewSessionCredentialID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientWeb, AuthenticatedAt: time.Now(),
	}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userSettingsResource(applicationFake))
	for name, path := range map[string]string{
		"arbitrary user":   "/api/v1/users/" + model.NewUserID().String() + "/settings",
		"settings listing": "/api/v1/users/settings",
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusNotFound, response.Code, "status = %d: %s", response.Code, response.Body.String())
		})
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		request := httptest.NewRequest(method, "/api/v1/users/me/settings", nil)
		request.Header.Set("Authorization", "Bearer session-credential")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		require.Equal(t, http.StatusMethodNotAllowed, response.Code, "%s status = %d: %s", method, response.Code, response.Body.String())
	}
	require.Equal(t, 0, applicationFake.calls, "rejected requests reached application")
}
