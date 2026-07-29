// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Skip("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.LoginRateLimit.MaximumAttempts = 2
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithServerOptions(app.WithStore(persistence)),
	)

	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username:    "integration-user",
		Email:       "integration-user@example.edu",
		DisplayName: "Integration User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}

	wrong := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": "wrong password",
			"client_type": model.SessionClientDesktop,
		},
		"",
	)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password status = %d: %s", wrong.Code, wrong.Body.String())
	}

	login := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Email, "password": password,
			"client_type": model.SessionClientDesktop,
			"device_id":   "integration-device", "device_name": "Integration Device",
		},
		"",
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", login.Code, login.Body.String())
	}
	first := decodeAuthenticationResponse(t, login)
	if first.User == nil || first.User.Id != user.Id || first.Tokens.AccessToken == "" ||
		first.Tokens.RefreshToken == "" {
		t.Fatalf("login response = %#v", first)
	}

	me := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d: %s", me.Code, me.Body.String())
	}

	refresh := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		first.Tokens.RefreshToken,
	)
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refresh.Code, refresh.Body.String())
	}
	second := decodeAuthenticationResponse(t, refresh)
	if second.Tokens.AccessToken == first.Tokens.AccessToken ||
		second.Tokens.RefreshToken == first.Tokens.RefreshToken {
		t.Fatal("refresh did not rotate both credentials")
	}
	oldAccess := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	if oldAccess.Code != http.StatusUnauthorized {
		t.Fatalf("old access status = %d", oldAccess.Code)
	}

	replay := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		first.Tokens.RefreshToken,
	)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("refresh replay status = %d: %s", replay.Code, replay.Body.String())
	}
	revokedByReplay := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		second.Tokens.AccessToken,
	)
	if revokedByReplay.Code != http.StatusUnauthorized {
		t.Fatalf("replay-family access status = %d", revokedByReplay.Code)
	}

	loginAgain := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	if loginAgain.Code != http.StatusOK {
		t.Fatalf("second login status = %d: %s", loginAgain.Code, loginAgain.Body.String())
	}
	third := decodeAuthenticationResponse(t, loginAgain)
	logout := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		third.Tokens.AccessToken,
	)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d: %s", logout.Code, logout.Body.String())
	}
	afterLogout := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		third.Tokens.AccessToken,
	)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout access status = %d", afterLogout.Code)
	}

	loginBeforeDisable := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientDesktop,
		},
		"",
	)
	if loginBeforeDisable.Code != http.StatusOK {
		t.Fatalf(
			"pre-disable login status = %d: %s",
			loginBeforeDisable.Code,
			loginBeforeDisable.Body.String(),
		)
	}
	fourth := decodeAuthenticationResponse(t, loginBeforeDisable)
	currentUser, err := persistence.User().Get(context.Background(), user.Id)
	if err != nil {
		t.Fatal(err)
	}
	currentUser.DisabledAt = model.GetMillis()
	if _, err := persistence.User().Update(context.Background(), currentUser); err != nil {
		t.Fatal(err)
	}
	refreshAfterDisable := performJSONRequest(
		helper.Server.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		fourth.Tokens.RefreshToken,
	)
	if refreshAfterDisable.Code != http.StatusUnauthorized {
		t.Fatalf("disabled-user refresh status = %d", refreshAfterDisable.Code)
	}
	cachedAccessAfterDisable := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		fourth.Tokens.AccessToken,
	)
	if cachedAccessAfterDisable.Code != http.StatusUnauthorized {
		t.Fatalf("disabled-user cached access status = %d", cachedAccessAfterDisable.Code)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		rateLimited := performJSONRequest(
			helper.Server.Handler(),
			http.MethodPost,
			"/api/v1/auth/login",
			map[string]any{
				"login_id":    "does-not-exist@example.edu",
				"password":    "irrelevant password",
				"client_type": model.SessionClientDesktop,
			},
			"",
		)
		want := http.StatusUnauthorized
		if attempt == 3 {
			want = http.StatusTooManyRequests
		}
		if rateLimited.Code != want {
			t.Fatalf(
				"rate-limit attempt %d status = %d, want %d: %s",
				attempt,
				rateLimited.Code,
				want,
				rateLimited.Body.String(),
			)
		}
	}

	logs := helper.Logs.String()
	for _, token := range []string{
		first.Tokens.AccessToken,
		first.Tokens.RefreshToken,
		second.Tokens.AccessToken,
		second.Tokens.RefreshToken,
		third.Tokens.AccessToken,
		third.Tokens.RefreshToken,
		fourth.Tokens.AccessToken,
		fourth.Tokens.RefreshToken,
	} {
		if strings.Contains(logs, token) {
			t.Fatal("raw authentication credential appeared in logs")
		}
	}
}

type authenticationResponse struct {
	User    *model.User                 `json:"user"`
	Session *model.Session              `json:"session"`
	Tokens  *model.AuthenticationTokens `json:"tokens"`
}

func decodeAuthenticationResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
) authenticationResponse {
	t.Helper()
	var decoded authenticationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Session == nil || decoded.Tokens == nil {
		t.Fatalf("authentication response = %#v", decoded)
	}
	return decoded
}

func performJSONRequest(
	handler http.Handler,
	method string,
	path string,
	body any,
	bearer string,
) *httptest.ResponseRecorder {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func openAuthenticationStore(t *testing.T, dataSource string) *sqlstore.SqlStore {
	t.Helper()
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	persistence, err := sqlstore.New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		_ = persistence.Close()
		t.Fatal(err)
	}
	return persistence
}
