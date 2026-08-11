//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/testlib"
)

type failingBroadcastCluster struct{}

func (*failingBroadcastCluster) NodeID() string              { return "failing-broadcast-node" }
func (*failingBroadcastCluster) Start(context.Context) error { return nil }
func (*failingBroadcastCluster) Stop(context.Context) error  { return nil }
func (*failingBroadcastCluster) Ping(context.Context) error  { return nil }
func (*failingBroadcastCluster) RegisterHandler(cluster.Event, cluster.Handler) error {
	return nil
}
func (*failingBroadcastCluster) Broadcast(context.Context, *cluster.Message) error {
	return errors.New("cluster broadcast unavailable")
}
func (*failingBroadcastCluster) SendToNode(context.Context, string, *cluster.Message) error {
	return nil
}

func TestAuthenticationFanoutFailureIntegration(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
		testlib.WithCluster(&failingBroadcastCluster{}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, &model.User{
		Username: "fanout-failure-user",
		Email:    "fanout-failure-user@example.edu",
	}, password)
	if err != nil {
		t.Fatal(err)
	}
	login, err := helper.App.Login(ctx, application.Invocation{}, application.LoginCommand{
		LoginID:    user.Username,
		Password:   password,
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.App.Logout(
		ctx,
		application.NewInvocation(*principal, model.RequestMetadata{}),
		application.LogoutCommand{},
	); err != nil {
		t.Fatalf("Logout() error = %v, want committed success", err)
	}
	if _, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken); !application.Is(err, "authentication.invalid_token") {
		t.Fatalf("AuthenticateAccess() after logout error = %v, want invalid token", err)
	}
	if !strings.Contains(helper.Logs.String(), "security invalidation broadcast failed") {
		t.Fatal("failed best-effort security fan-out was not diagnosed")
	}
	for _, secret := range []string{password, login.Tokens.AccessToken, login.Tokens.RefreshToken} {
		if strings.Contains(helper.Logs.String(), secret) {
			t.Fatal("authentication secret appeared in fan-out diagnostics")
		}
	}
}

func TestPersonalAccessTokenIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
	)
	institution, err := persistence.Institution().Save(
		context.Background(),
		&model.Institution{
			Name: "northbridge", DisplayName: "Northbridge University",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parentUnit, err := persistence.AcademicUnit().Save(
		context.Background(),
		&model.AcademicUnit{
			InstitutionID: institution.ID, Name: "engineering",
			DisplayName: "Engineering",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	childUnit, err := persistence.AcademicUnit().Save(
		context.Background(),
		&model.AcademicUnit{
			InstitutionID: institution.ID, ParentID: parentUnit.ID,
			Name: "computing", DisplayName: "Computing",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	siblingUnit, err := persistence.AcademicUnit().Save(
		context.Background(),
		&model.AcademicUnit{
			InstitutionID: institution.ID, Name: "health",
			DisplayName: "Health",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(
		context.Background(),
		&model.User{
			Username: "pat-user", Email: "pat-user@example.edu",
			DisplayName: "PAT User",
		},
		password,
	)
	if appErr != nil {
		t.Fatal(appErr)
	}
	role, err := persistence.Role().Save(
		context.Background(),
		&model.Role{
			Name: "academic_reader", DisplayName: "Academic reader",
			Permissions: []string{string(model.ActionAcademicUnitView)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.RoleBinding().Save(
		context.Background(),
		&model.RoleBinding{
			UserID: user.ID, RoleID: role.ID,
			ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		},
	); err != nil {
		t.Fatal(err)
	}
	login := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", login.Code, login.Body.String())
	}
	session := decodeAuthenticationResponse(t, login)
	create := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens",
		map[string]any{
			"description":      "local automation",
			"scopes":           []string{string(model.ActionAcademicUnitView)},
			"academic_unit_id": parentUnit.ID,
			"expires_at":       time.Now().Add(2 * time.Hour).UnixMilli(),
		},
		session.Tokens.AccessToken,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create PAT status = %d: %s", create.Code, create.Body.String())
	}
	var created model.PersonalAccessTokenCreation
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Token == nil || created.Credential == "" ||
		created.Token.TokenHash != "" ||
		create.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create PAT response = %#v", created)
	}
	me := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	if me.Code != http.StatusOK {
		t.Fatalf("PAT current-user status = %d: %s", me.Code, me.Body.String())
	}
	descendant := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/academic-units/"+childUnit.ID.String(),
		nil,
		created.Credential,
	)
	if descendant.Code != http.StatusOK {
		t.Fatalf(
			"PAT descendant status = %d: %s",
			descendant.Code,
			descendant.Body.String(),
		)
	}
	outsideConstraint := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/academic-units/"+siblingUnit.ID.String(),
		nil,
		created.Credential,
	)
	if outsideConstraint.Code != http.StatusForbidden {
		t.Fatalf(
			"PAT sibling status = %d: %s",
			outsideConstraint.Code,
			outsideConstraint.Body.String(),
		)
	}
	outsideScope := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/institution",
		nil,
		created.Credential,
	)
	if outsideScope.Code != http.StatusForbidden {
		t.Fatalf(
			"PAT institution status = %d: %s",
			outsideScope.Code,
			outsideScope.Body.String(),
		)
	}
	sessionOnly := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		created.Credential,
	)
	if sessionOnly.Code != http.StatusUnauthorized {
		t.Fatalf("PAT session-only status = %d: %s", sessionOnly.Code, sessionOnly.Body.String())
	}
	list := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/tokens",
		nil,
		session.Tokens.AccessToken,
	)
	if list.Code != http.StatusOK ||
		strings.Contains(list.Body.String(), created.Credential) ||
		strings.Contains(list.Body.String(), model.HashToken(created.Credential)) {
		t.Fatalf("list PAT response = %d: %s", list.Code, list.Body.String())
	}
	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedAudits, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedAudits, []byte(created.Credential)) ||
		bytes.Contains(encodedAudits, []byte(model.HashToken(created.Credential))) {
		t.Fatal("personal access token credential or hash leaked into audit events")
	}
	disable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens/"+created.Token.ID.String()+"/disable",
		nil,
		session.Tokens.AccessToken,
	)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable PAT status = %d: %s", disable.Code, disable.Body.String())
	}
	whileDisabled := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	if whileDisabled.Code != http.StatusUnauthorized {
		t.Fatalf(
			"disabled PAT status = %d: %s",
			whileDisabled.Code,
			whileDisabled.Body.String(),
		)
	}
	enable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens/"+created.Token.ID.String()+"/enable",
		nil,
		session.Tokens.AccessToken,
	)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable PAT status = %d: %s", enable.Code, enable.Body.String())
	}
	afterEnable := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	if afterEnable.Code != http.StatusOK {
		t.Fatalf(
			"reenabled PAT status = %d: %s",
			afterEnable.Code,
			afterEnable.Body.String(),
		)
	}
	revoke := performJSONRequest(
		helper.Handler(),
		http.MethodDelete,
		"/api/v1/users/me/tokens/"+created.Token.ID.String(),
		nil,
		session.Tokens.AccessToken,
	)
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke PAT status = %d: %s", revoke.Code, revoke.Body.String())
	}
	afterRevoke := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	if afterRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("revoked PAT status = %d: %s", afterRevoke.Code, afterRevoke.Body.String())
	}
}

func TestAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.LoginRateLimit.MaximumAttempts = 2
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithStore(persistence),
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
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": "wrong password",
			"client_type": model.SessionClientCLI,
		},
		"",
	)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password status = %d: %s", wrong.Code, wrong.Body.String())
	}
	assertProblemCode(t, wrong, "authentication.invalid_credentials")

	login := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Email, "password": password,
			"client_type": model.SessionClientCLI,
			"device_id":   "integration-device", "device_name": "Integration Device",
		},
		"",
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", login.Code, login.Body.String())
	}
	first := decodeAuthenticationResponse(t, login)
	if first.User == nil || first.User.ID != user.ID || first.Tokens.AccessToken == "" ||
		first.Tokens.RefreshToken == "" {
		t.Fatalf("login response = %#v", first)
	}
	if cookies := login.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("CLI login unexpectedly set browser cookies = %#v", cookies)
	}

	me := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d: %s", me.Code, me.Body.String())
	}

	refresh := performJSONRequest(
		helper.Handler(),
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
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	if oldAccess.Code != http.StatusUnauthorized {
		t.Fatalf("old access status = %d", oldAccess.Code)
	}
	assertProblemCode(t, oldAccess, "authentication.invalid_token")

	replay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		first.Tokens.RefreshToken,
	)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("refresh replay status = %d: %s", replay.Code, replay.Body.String())
	}
	assertProblemCode(t, replay, "authentication.invalid_token")
	revokedByReplay := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		second.Tokens.AccessToken,
	)
	if revokedByReplay.Code != http.StatusUnauthorized {
		t.Fatalf("replay-family access status = %d", revokedByReplay.Code)
	}
	assertProblemCode(t, revokedByReplay, "authentication.invalid_token")

	loginAgain := performJSONRequest(
		helper.Handler(),
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
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		third.Tokens.AccessToken,
	)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d: %s", logout.Code, logout.Body.String())
	}
	afterLogout := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		third.Tokens.AccessToken,
	)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout access status = %d", afterLogout.Code)
	}
	assertProblemCode(t, afterLogout, "authentication.invalid_token")

	loginBeforeDisable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientCLI,
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
	currentUser, err := persistence.User().Get(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	disabledAt := model.GetMillis()
	auditAttempt, err := persistence.Audit().Save(context.Background(), &model.AuditEvent{
		Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewId(), Status: model.AuditStatusAttempt, NodeID: "authentication-integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.User().SetDisabledWithAudit(context.Background(), &store.UserDisabledStateChange{
		ID: user.ID.String(), ExpectedRevision: currentUser.Revision, Disabled: true,
		ChangedAt: disabledAt, RevocationReason: "authentication integration disabled account",
		AuditEventID: auditAttempt.ID.String(), AuditAt: disabledAt,
	}); err != nil {
		t.Fatal(err)
	}
	refreshAfterDisable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		fourth.Tokens.RefreshToken,
	)
	if refreshAfterDisable.Code != http.StatusUnauthorized {
		t.Fatalf("disabled-user refresh status = %d", refreshAfterDisable.Code)
	}
	assertProblemCode(t, refreshAfterDisable, "authentication.invalid_token")
	for attempt := 1; attempt <= 3; attempt++ {
		rateLimited := performJSONRequest(
			helper.Handler(),
			http.MethodPost,
			"/api/v1/auth/login",
			map[string]any{
				"login_id":    "does-not-exist@example.edu",
				"password":    "irrelevant password",
				"client_type": model.SessionClientCLI,
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
		wantCode := "authentication.invalid_credentials"
		if attempt == 3 {
			wantCode = "authentication.rate_limited"
		}
		assertProblemCode(t, rateLimited, wantCode)
	}

	logs := helper.Logs.String()
	for _, secret := range []string{
		password,
		"wrong password",
		"irrelevant password",
		first.Tokens.AccessToken,
		first.Tokens.RefreshToken,
		second.Tokens.AccessToken,
		second.Tokens.RefreshToken,
		third.Tokens.AccessToken,
		third.Tokens.RefreshToken,
		fourth.Tokens.AccessToken,
		fourth.Tokens.RefreshToken,
	} {
		if strings.Contains(logs, secret) {
			t.Fatal("authentication secret appeared in logs")
		}
	}
}

func TestBrowserCookieAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.edu"
		}),
		testlib.WithStore(persistence),
	)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username: "browser-user", Email: "browser-user@example.edu",
		DisplayName: "Browser User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}

	login := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientDesktop,
		},
		"",
	)
	if login.Code != http.StatusOK {
		t.Fatalf("browser login status = %d: %s", login.Code, login.Body.String())
	}
	var loginBody authenticationResponse
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.User == nil || loginBody.Session == nil || loginBody.Tokens != nil {
		t.Fatalf("browser login response = %#v", loginBody)
	}
	loginCookies := cookieMap(login.Result().Cookies())
	assertBrowserCookieContract(t, loginCookies)
	for _, cookie := range loginCookies {
		if strings.Contains(login.Body.String(), cookie.Value) ||
			strings.Contains(helper.Logs.String(), cookie.Value) {
			t.Fatal("browser credential appeared in body or logs")
		}
	}

	me := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "", "",
	)
	if me.Code != http.StatusOK {
		t.Fatalf("browser current-user status = %d: %s", me.Code, me.Body.String())
	}
	ambiguous := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "another-access-token", "",
	)
	if ambiguous.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous browser credential status = %d: %s", ambiguous.Code, ambiguous.Body.String())
	}

	missingLogoutCSRF := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/logout",
		nil, loginCookies, "", "",
	)
	if missingLogoutCSRF.Code != http.StatusForbidden {
		t.Fatalf(
			"missing logout CSRF status = %d: %s",
			missingLogoutCSRF.Code,
			missingLogoutCSRF.Body.String(),
		)
	}
	assertProblemCode(t, missingLogoutCSRF, "authentication.csrf.invalid")
	missingRefreshCSRF := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/refresh",
		nil, loginCookies, "", "",
	)
	if missingRefreshCSRF.Code != http.StatusForbidden {
		t.Fatalf(
			"missing refresh CSRF status = %d: %s",
			missingRefreshCSRF.Code,
			missingRefreshCSRF.Body.String(),
		)
	}
	assertProblemCode(t, missingRefreshCSRF, "authentication.csrf.invalid")

	refresh := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/refresh",
		nil, loginCookies, "",
		loginCookies[api.BrowserCSRFCookieName].Value,
	)
	if refresh.Code != http.StatusOK {
		t.Fatalf("browser refresh status = %d: %s", refresh.Code, refresh.Body.String())
	}
	var refreshBody authenticationResponse
	if err := json.Unmarshal(refresh.Body.Bytes(), &refreshBody); err != nil {
		t.Fatal(err)
	}
	if refreshBody.Session == nil || refreshBody.Tokens != nil {
		t.Fatalf("browser refresh response = %#v", refreshBody)
	}
	refreshedCookies := cookieMap(refresh.Result().Cookies())
	assertBrowserCookieContract(t, refreshedCookies)
	for name, previous := range loginCookies {
		if refreshedCookies[name].Value == previous.Value {
			t.Fatalf("%s was not rotated", name)
		}
	}

	oldAccess := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "", "",
	)
	if oldAccess.Code != http.StatusUnauthorized {
		t.Fatalf("old browser access status = %d", oldAccess.Code)
	}
	currentAccess := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, refreshedCookies, "", "",
	)
	if currentAccess.Code != http.StatusOK {
		t.Fatalf("refreshed browser access status = %d: %s", currentAccess.Code, currentAccess.Body.String())
	}

	logout := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/logout",
		nil, refreshedCookies, "",
		refreshedCookies[api.BrowserCSRFCookieName].Value,
	)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("browser logout status = %d: %s", logout.Code, logout.Body.String())
	}
	cleared := cookieMap(logout.Result().Cookies())
	if len(cleared) != 4 {
		t.Fatalf("cleared cookies = %#v", cleared)
	}
	for _, cookie := range cleared {
		if cookie.MaxAge >= 0 {
			t.Fatalf("logout did not expire cookie = %#v", cookie)
		}
	}
}

func TestSessionManagementIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username:    "session-user",
		Email:       "session-user@example.edu",
		DisplayName: "Session User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}

	firstLogin := loginIntegrationUser(
		t,
		helper.Handler(),
		user.Username,
		password,
		model.SessionClientCLI,
		"first-device",
	)
	secondLogin := loginIntegrationUser(
		t,
		helper.Handler(),
		user.Username,
		password,
		model.SessionClientCLI,
		"second-device",
	)

	list := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/sessions",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d: %s", list.Code, list.Body.String())
	}
	var sessions []*model.Session
	sessions = decodeSessionList(t, list)
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v", sessions)
	}
	for _, session := range sessions {
		if session.UserID != user.ID || session.RevokedAt.Valid {
			t.Fatalf("unsafe session listing = %#v", session)
		}
	}
	if strings.Contains(list.Body.String(), "token_hash") ||
		strings.Contains(list.Body.String(), firstLogin.Tokens.AccessToken) ||
		strings.Contains(list.Body.String(), firstLogin.Tokens.RefreshToken) {
		t.Fatalf("session listing exposed credential material: %s", list.Body.String())
	}

	unknownField := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": firstLogin.Session.ID, "unknown": true},
		secondLogin.Tokens.AccessToken,
	)
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown revoke field status = %d", unknownField.Code)
	}

	revokeFirst := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": firstLogin.Session.ID},
		secondLogin.Tokens.AccessToken,
	)
	if revokeFirst.Code != http.StatusNoContent {
		t.Fatalf("revoke session status = %d: %s", revokeFirst.Code, revokeFirst.Body.String())
	}
	firstAfterRevoke := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		firstLogin.Tokens.AccessToken,
	)
	if firstAfterRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("revoked access status = %d", firstAfterRevoke.Code)
	}
	secondStillActive := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	if secondStillActive.Code != http.StatusOK {
		t.Fatalf("unrelated session status = %d", secondStillActive.Code)
	}

	invalidID := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": "not-an-id"},
		secondLogin.Tokens.AccessToken,
	)
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("invalid session id status = %d", invalidID.Code)
	}
	assertProblemCode(t, invalidID, "session.id.invalid")

	otherUser, appErr := helper.App.CreateLocalUser(context.Background(), &model.User{
		Username:    "other-session-user",
		Email:       "other-session-user@example.edu",
		DisplayName: "Other Session User",
	}, password)
	if appErr != nil {
		t.Fatal(appErr)
	}
	otherLogin := loginIntegrationUser(
		t,
		helper.Handler(),
		otherUser.Username,
		password,
		model.SessionClientCLI,
		"other-device",
	)
	crossUserRevoke := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": otherLogin.Session.ID},
		secondLogin.Tokens.AccessToken,
	)
	if crossUserRevoke.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-user revoke status = %d: %s",
			crossUserRevoke.Code,
			crossUserRevoke.Body.String(),
		)
	}
	assertProblemCode(t, crossUserRevoke, "session.not_found")
	otherStillActive := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		otherLogin.Tokens.AccessToken,
	)
	if otherStillActive.Code != http.StatusOK {
		t.Fatalf("cross-user session status = %d", otherStillActive.Code)
	}

	revokeAll := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke-all",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	if revokeAll.Code != http.StatusNoContent {
		t.Fatalf("revoke-all status = %d: %s", revokeAll.Code, revokeAll.Body.String())
	}
	secondAfterRevokeAll := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	if secondAfterRevokeAll.Code != http.StatusUnauthorized {
		t.Fatalf("revoke-all access status = %d", secondAfterRevokeAll.Code)
	}

	thirdLogin := loginIntegrationUser(
		t,
		helper.Handler(),
		user.Username,
		password,
		model.SessionClientCLI,
		"third-device",
	)
	activeAfterRevokeAll := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/sessions",
		nil,
		thirdLogin.Tokens.AccessToken,
	)
	sessions = decodeSessionList(t, activeAfterRevokeAll)
	if activeAfterRevokeAll.Code != http.StatusOK ||
		len(sessions) != 1 ||
		sessions[0].ID != thirdLogin.Session.ID {
		t.Fatalf(
			"active sessions after revoke-all status=%d sessions=%#v",
			activeAfterRevokeAll.Code,
			sessions,
		)
	}

	logs := helper.Logs.String()
	for _, token := range []string{
		firstLogin.Tokens.AccessToken,
		firstLogin.Tokens.RefreshToken,
		secondLogin.Tokens.AccessToken,
		secondLogin.Tokens.RefreshToken,
		otherLogin.Tokens.AccessToken,
		otherLogin.Tokens.RefreshToken,
		thirdLogin.Tokens.AccessToken,
		thirdLogin.Tokens.RefreshToken,
	} {
		if strings.Contains(logs, token) {
			t.Fatal("raw session-management credential appeared in logs")
		}
	}
}

func loginIntegrationUser(
	t *testing.T,
	handler http.Handler,
	loginID string,
	password string,
	clientType model.SessionClientType,
	deviceID string,
) authenticationResponse {
	t.Helper()
	response := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": loginID, "password": password,
			"client_type": clientType, "device_id": deviceID,
		},
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", response.Code, response.Body.String())
	}
	return decodeAuthenticationResponse(t, response)
}

type authenticationResponse struct {
	User    *model.User
	Session *model.Session
	Tokens  *model.AuthenticationTokens `json:"tokens"`
}

type wireUserProfileResponse struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	Username       string `json:"username"`
	Email          string `json:"email"`
	EmailVerified  bool   `json:"email_verified"`
	DisplayName    string `json:"display_name"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Locale         string `json:"locale"`
	Timezone       string `json:"timezone"`
	LastLoginAt    int64  `json:"last_login_at"`
	LastActivityAt int64  `json:"last_activity_at"`
	DisabledAt     int64  `json:"disabled_at"`
}

func (w *wireUserProfileResponse) model() *model.User {
	if w == nil {
		return nil
	}
	return &model.User{
		ID: model.UserID(w.ID), CreatedAt: model.TimeFromMillis(w.CreateAt),
		UpdatedAt: model.TimeFromMillis(w.UpdateAt), ArchivedAt: model.OptionalTimeFromMillis(w.DeleteAt),
		Username: w.Username, Email: w.Email, EmailVerified: w.EmailVerified,
		DisplayName: w.DisplayName, FirstName: w.FirstName, LastName: w.LastName,
		Locale: w.Locale, Timezone: w.Timezone,
		LastLoginAt:    model.OptionalTimeFromMillis(w.LastLoginAt),
		LastActivityAt: model.OptionalTimeFromMillis(w.LastActivityAt),
		DisabledAt:     model.OptionalTimeFromMillis(w.DisabledAt),
	}
}

type wireSessionResponse struct {
	ID                     string `json:"id"`
	CreateAt               int64  `json:"create_at"`
	UpdateAt               int64  `json:"update_at"`
	DeleteAt               int64  `json:"delete_at"`
	UserID                 string `json:"user_id"`
	ClientType             string `json:"client_type"`
	DeviceID               string `json:"device_id"`
	DeviceName             string `json:"device_name"`
	AuthenticationMethod   string `json:"authentication_method"`
	AuthenticationStrength string `json:"authentication_strength"`
	AuthenticatedAt        int64  `json:"authenticated_at"`
	MFACompletedAt         int64  `json:"mfa_completed_at"`
	LastActivityAt         int64  `json:"last_activity_at"`
	IdleExpiresAt          int64  `json:"idle_expires_at"`
	ExpiresAt              int64  `json:"expires_at"`
	RevokedAt              int64  `json:"revoked_at"`
	RevocationReason       string `json:"revocation_reason"`
}

type wireAuthenticationTokensResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	AccessExpiresAt  int64  `json:"access_expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at"`
}

func (w *wireAuthenticationTokensResponse) model() *model.AuthenticationTokens {
	if w == nil {
		return nil
	}
	return &model.AuthenticationTokens{
		AccessToken:      w.AccessToken,
		RefreshToken:     w.RefreshToken,
		AccessExpiresAt:  model.TimeFromMillis(w.AccessExpiresAt),
		RefreshExpiresAt: model.TimeFromMillis(w.RefreshExpiresAt),
	}
}

func (w *wireSessionResponse) model() *model.Session {
	if w == nil {
		return nil
	}
	return &model.Session{
		ID: model.SessionID(w.ID), CreatedAt: model.TimeFromMillis(w.CreateAt),
		UpdatedAt: model.TimeFromMillis(w.UpdateAt), ArchivedAt: model.OptionalTimeFromMillis(w.DeleteAt),
		UserID: model.UserID(w.UserID), ClientType: model.SessionClientType(w.ClientType),
		DeviceID: w.DeviceID, DeviceName: w.DeviceName,
		AuthenticationMethod:   w.AuthenticationMethod,
		AuthenticationStrength: model.AuthenticationStrength(w.AuthenticationStrength),
		AuthenticatedAt:        model.TimeFromMillis(w.AuthenticatedAt),
		MFACompletedAt:         model.OptionalTimeFromMillis(w.MFACompletedAt),
		LastActivityAt:         model.TimeFromMillis(w.LastActivityAt),
		IdleExpiresAt:          model.TimeFromMillis(w.IdleExpiresAt), ExpiresAt: model.TimeFromMillis(w.ExpiresAt),
		RevokedAt: model.OptionalTimeFromMillis(w.RevokedAt), RevocationReason: w.RevocationReason,
	}
}

func decodeAuthenticationResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
) authenticationResponse {
	t.Helper()
	var wire struct {
		User    *wireUserProfileResponse          `json:"user"`
		Session *wireSessionResponse              `json:"session"`
		Tokens  *wireAuthenticationTokensResponse `json:"tokens"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	decoded := authenticationResponse{User: wire.User.model(), Session: wire.Session.model(), Tokens: wire.Tokens.model()}
	if decoded.Session == nil || decoded.Tokens == nil {
		t.Fatalf("authentication response = %#v", decoded)
	}
	return decoded
}

func decodeSessionList(t *testing.T, response *httptest.ResponseRecorder) []*model.Session {
	t.Helper()
	var wire []*wireSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	sessions := make([]*model.Session, 0, len(wire))
	for _, item := range wire {
		sessions = append(sessions, item.model())
	}
	return sessions
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

func performBrowserJSONRequest(
	handler http.Handler,
	method string,
	path string,
	body any,
	cookies map[string]*http.Cookie,
	bearer string,
	csrf string,
) *httptest.ResponseRecorder {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if csrf != "" {
		request.Header.Set(api.BrowserCSRFHeader, csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func cookieMap(cookies []*http.Cookie) map[string]*http.Cookie {
	mapped := make(map[string]*http.Cookie, len(cookies))
	for _, cookie := range cookies {
		mapped[cookie.Name] = cookie
	}
	return mapped
}

func assertBrowserCookieContract(
	t *testing.T,
	cookies map[string]*http.Cookie,
) {
	t.Helper()
	if api.BrowserAccessCookieName != "PROCTOR_ACCESS" ||
		api.BrowserRefreshCookieName != "PROCTOR_REFRESH" ||
		api.BrowserCSRFBindingCookieName != "PROCTOR_CSRF_BINDING" ||
		api.BrowserCSRFCookieName != "PROCTOR_CSRF" ||
		api.BrowserCSRFHeader != "X-Proctor-CSRF-Token" {
		t.Fatal("browser cookie or CSRF public name changed")
	}
	if len(cookies) != 4 {
		t.Fatalf("browser cookies = %#v", cookies)
	}
	access := cookies["PROCTOR_ACCESS"]
	refresh := cookies["PROCTOR_REFRESH"]
	binding := cookies["PROCTOR_CSRF_BINDING"]
	csrf := cookies["PROCTOR_CSRF"]
	if access == nil || refresh == nil || binding == nil || csrf == nil ||
		!access.HttpOnly || !refresh.HttpOnly || !binding.HttpOnly ||
		csrf.HttpOnly || !access.Secure || !refresh.Secure ||
		!binding.Secure || !csrf.Secure ||
		access.SameSite != http.SameSiteLaxMode ||
		refresh.SameSite != http.SameSiteLaxMode ||
		binding.SameSite != http.SameSiteLaxMode ||
		csrf.SameSite != http.SameSiteLaxMode ||
		access.Domain != "" || refresh.Domain != "" ||
		binding.Domain != "" || csrf.Domain != "" ||
		access.Path != "/" ||
		refresh.Path != "/api/v1/auth/refresh" ||
		binding.Path != "/" || csrf.Path != "/" {
		t.Fatalf("browser cookie contract = %#v", cookies)
	}
}

func assertProblemCode(
	t *testing.T,
	response *httptest.ResponseRecorder,
	want string,
) {
	t.Helper()
	if response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem Content-Type = %q", response.Header().Get("Content-Type"))
	}
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != want {
		t.Fatalf("problem code = %q, want %q", problem.Code, want)
	}
}

func openAuthenticationStore(t *testing.T, dataSource string) *sqlstore.SQLStore {
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
			external_login_states, installation_states, audit_events, mfa_recovery_codes, mfa_credentials,
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
