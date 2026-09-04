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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userSettingsHTTPApplication struct {
	result         application.UserSettingsView
	err            error
	invocation     application.Invocation
	calls          int
	replaceResult  application.UserSettingsReplacementResult
	replaceErr     error
	replaceCommand application.ReplaceOwnUserSettingsCommand
	replaceCalls   int
}

func (a *userSettingsHTTPApplication) ReplaceOwnUserSettings(
	_ context.Context,
	invocation application.Invocation,
	command application.ReplaceOwnUserSettingsCommand,
) (application.UserSettingsReplacementResult, error) {
	a.replaceCalls++
	a.invocation = invocation
	a.replaceCommand = command
	return a.replaceResult, a.replaceErr
}

func (a *userSettingsHTTPApplication) ReadOwnUserSettings(
	_ context.Context,
	invocation application.Invocation,
) (application.UserSettingsView, error) {
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
	applicationFake := &userSettingsHTTPApplication{replaceResult: application.UserSettingsReplacementResult{
		Revision: next, FormatVersion: 1, UpdatedAt: at, Changed: true,
	}}
	httpAPI := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, userSettingsResource(applicationFake))
	body, err := json.Marshal(map[string]any{
		"source": source, "format_version": 1, "expected_revision": revision.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/settings", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session-credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "settings-save-1")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if applicationFake.replaceCalls != 1 || applicationFake.replaceCommand.Source != source ||
		applicationFake.replaceCommand.FormatVersion != 1 ||
		applicationFake.replaceCommand.ExpectedRevision != revision ||
		applicationFake.replaceCommand.IdempotencyKey != "settings-save-1" {
		t.Fatalf("replacement command = %#v", applicationFake.replaceCommand)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, exposed := result["source"]; exposed || result["revision"] != next.String() || result["changed"] != true {
		t.Fatalf("response = %#v", result)
	}
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
	applicationFake := &userSettingsHTTPApplication{replaceResult: application.UserSettingsReplacementResult{
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
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if applicationFake.replaceCalls != 0 {
		t.Fatalf("invalid requests reached application %d times", applicationFake.replaceCalls)
	}

	maxSource := strings.Repeat(" ", model.UserSettingsSourceMaxBytes-3) + "{}\n"
	body, err := json.Marshal(userSettingsReplaceRequest{
		Source: maxSource, FormatVersion: 1, ExpectedRevision: revision.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/settings", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer session-credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "maximum-save")
	response := httptest.NewRecorder()
	newAPI().ServeHTTP(response, request)
	if response.Code != http.StatusOK || applicationFake.replaceCommand.Source != maxSource {
		t.Fatalf("maximum source status/size = %d/%d: %s", response.Code, len(applicationFake.replaceCommand.Source), response.Body.String())
	}
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
	applicationFake := &userSettingsHTTPApplication{result: application.UserSettingsView{
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
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	if applicationFake.calls != 1 || applicationFake.invocation.Principal().UserID != principal.UserID {
		t.Fatalf("calls/invocation = %d/%#v", applicationFake.calls, applicationFake.invocation)
	}
	var body userSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Source != source || body.FormatVersion != 1 ||
		body.Revision != applicationFake.result.Revision.String() || !body.Writable ||
		body.UpdatedAt != at.UnixMilli() {
		t.Fatalf("body = %#v", body)
	}
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
	applicationFake := &userSettingsHTTPApplication{result: application.UserSettingsView{
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
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body userSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Source != source || body.FormatVersion != 2 || body.Writable ||
		body.Revision != applicationFake.result.Revision.String() || body.UpdatedAt != at.UnixMilli() {
		t.Fatalf("body = %#v", body)
	}
}

func TestUserSettingsHTTPRejectsNonSessionAndArbitraryUserPaths(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	applicationFake := &userSettingsHTTPApplication{}
	patAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{err: application.NewError("authentication.invalid_token")},
		userSettingsResource(applicationFake),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/settings", nil)
	request.Header.Set("Authorization", "Bearer personal-access-token")
	response := httptest.NewRecorder()
	patAPI.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("PAT status = %d: %s", response.Code, response.Body.String())
	}

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
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		request := httptest.NewRequest(method, "/api/v1/users/me/settings", nil)
		request.Header.Set("Authorization", "Bearer session-credential")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d: %s", method, response.Code, response.Body.String())
		}
	}
	if applicationFake.calls != 0 {
		t.Fatalf("rejected requests reached application %d times", applicationFake.calls)
	}
}
