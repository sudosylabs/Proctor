//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/store/storetest"
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
	seedInitialAuthenticationAccessPolicy(t, persistence)
	seedAuthenticationAuditInstitution(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithStore(persistence),
		testlib.WithCluster(&failingBroadcastCluster{}),
	)
	ctx := context.Background()
	const password = "correct horse battery staple"
	user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
		Username: "fanout-failure-user",
		Email:    "fanout-failure-user@example.edu",
	}, Password: password})
	require.NoError(t, err)
	login, err := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{
		LoginID:    user.Username,
		Password:   password,
		ClientType: model.SessionClientCLI,
		Source:     "127.0.0.1:1",
	})
	require.NoError(t, err)
	principal, err := helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.NoError(t, err)
	require.NoError(t, helper.App.Logout(
		ctx,
		invocation.New(*principal, model.RequestMetadata{}),
		app.LogoutCommand{},
	), "Logout() error")
	_, err = helper.App.AuthenticateAccess(ctx, login.Tokens.AccessToken)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "AuthenticateAccess() after logout error = %v, want invalid token", err)
	require.Contains(t, helper.Logs.String(), "session revocation broadcast failed", "failed best-effort security fan-out was not diagnosed")
	require.Contains(t, helper.Logs.String(), "realtime cluster fan-out failed", "failed best-effort security fan-out was not diagnosed")
	for _, secret := range []string{password, login.Tokens.AccessToken, login.Tokens.RefreshToken} {
		require.NotContains(t, helper.Logs.String(), secret, "authentication secret appeared in fan-out diagnostics")
	}
}

func TestSessionExpiryEnforcementAcrossHTTPRefreshAndEstablishedWebSocket(t *testing.T) {
	dataSource := requireAuthenticationDatabase(t)
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	seedAuthenticationAuditInstitution(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	ctx := context.Background()

	login := func(identity string) (*model.Session, *model.AuthenticationTokens) {
		t.Helper()
		user, err := helper.App.CreateLocalUser(ctx, app.CreateLocalUserCommand{User: &model.User{
			Username: identity, Email: identity + "@example.edu",
		}, Password: "correct horse battery staple"})
		require.NoError(t, err)
		result, err := helper.App.Login(ctx, invocation.Invocation{}, app.LoginCommand{
			LoginID: user.Username, Password: "correct horse battery staple",
			ClientType: model.SessionClientCLI, Source: "127.0.0.1:1",
		})
		require.NoError(t, err)
		return result.Session, result.Tokens
	}
	expire := func(sessionID model.SessionID, accessToken string) {
		t.Helper()
		_, err := persistence.GetMaster().Exec(ctx, `UPDATE sessions
			SET last_activity_at=created_at, idle_expires_at=created_at+interval '1 millisecond'
			WHERE id=?`, sessionID.String())
		require.NoError(t, err)
		// The test shortens an already-cached deadline out of band. Production
		// deadline changes flow through the application and its invalidation
		// effects, so mirror that invalidation before exercising the read path.
		require.NoError(t, helper.Cache.Delete(ctx, "authentication/access/"+model.HashToken(accessToken)))
	}
	assertExpired := func(sessionID model.SessionID) {
		t.Helper()
		session, err := persistence.Session().Get(ctx, sessionID)
		require.NoError(t, err)
		require.True(t, session.RevokedAt.Valid)
		require.Equal(t, model.SessionRevocationExpired, session.RevocationReason)
	}

	httpSession, httpTokens := login("expiry-http")
	expire(httpSession.ID, httpTokens.AccessToken)
	response := performJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me", nil, httpTokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, response.Code, "expired HTTP access status = %d: %s", response.Code, response.Body.String())
	assertProblemCode(t, response, "authentication.token_invalid")
	assertExpired(httpSession.ID)

	refreshSession, refreshTokens := login("expiry-refresh")
	expire(refreshSession.ID, refreshTokens.AccessToken)
	response = performJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/refresh", nil, refreshTokens.RefreshToken,
	)
	require.Equal(t, http.StatusUnauthorized, response.Code, "expired refresh status = %d: %s", response.Code, response.Body.String())
	assertProblemCode(t, response, "authentication.token_invalid")
	assertExpired(refreshSession.ID)

	websocketSession, websocketTokens := login("expiry-websocket")
	principal, err := helper.App.AuthenticateAccess(ctx, websocketTokens.AccessToken)
	require.NoError(t, err)
	expire(websocketSession.ID, websocketTokens.AccessToken)
	err = helper.App.ValidateWebSocketPrincipal(ctx, *principal)
	require.True(t, apperror.Is(err, "authentication.token_invalid"), "expired WebSocket principal error = %v", err)
	assertExpired(websocketSession.ID)
}

func TestPersonalAccessTokenIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
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
	require.NoError(t, err)
	parentUnit, err := storetest.SaveAcademicUnit(context.Background(), persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "engineering",
		DisplayName: "Engineering",
	})
	require.NoError(t, err)
	childUnit, err := storetest.SaveAcademicUnit(context.Background(), persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, ParentID: parentUnit.ID,
		Name: "computing", DisplayName: "Computing",
	})
	require.NoError(t, err)
	siblingUnit, err := storetest.SaveAcademicUnit(context.Background(), persistence, &model.AcademicUnit{
		InstitutionID: institution.ID, Name: "health",
		DisplayName: "Health",
	})
	require.NoError(t, err)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(
		context.Background(),
		app.CreateLocalUserCommand{User: &model.User{
			Username: "pat-user", Email: "pat-user@example.edu",
			DisplayName: "PAT User",
		}, Password: password},
	)
	require.NoError(t, appErr)
	role, err := storetest.SaveRole(context.Background(), persistence, &model.Role{
		Name: "academic_reader", DisplayName: "Academic reader",
		Permissions: []string{string(model.ActionAcademicUnitView)},
	})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(context.Background(), persistence, &model.RoleBinding{
		UserID: user.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
	})
	require.NoError(t, err)
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
	require.Equal(t, http.StatusOK, login.Code, "login status = %d: %s", login.Code, login.Body.String())
	session := decodeAuthenticationResponse(t, login)
	create := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens",
		map[string]any{
			"description":      "local automation",
			"scopes":           []string{string(model.ActionAcademicUnitView)},
			"academic_unit_id": parentUnit.ID,
			"expires_at":       model.FormatRFC3339(time.Now().Add(2 * time.Hour)),
		},
		session.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusCreated, create.Code, "create PAT status = %d: %s", create.Code, create.Body.String())
	var created struct {
		Token *struct {
			ID        model.PersonalAccessTokenID `json:"id"`
			TokenHash *string                     `json:"token_hash"`
		} `json:"token"`
		Credential string `json:"credential"`
	}
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &created))
	require.NotNil(t, created.Token)
	require.NotEmpty(t, created.Credential)
	require.Nil(t, created.Token.TokenHash)
	require.Equal(t, "no-store", create.Header().Get("Cache-Control"))
	me := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusOK, me.Code, "PAT current-user status = %d: %s", me.Code, me.Body.String())
	descendant := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/academic-units/"+childUnit.ID.String(),
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusOK, descendant.Code, "PAT descendant status = %d: %s", descendant.Code, descendant.Body.String())
	outsideConstraint := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/academic-units/"+siblingUnit.ID.String(),
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusForbidden, outsideConstraint.Code, "PAT sibling status = %d: %s", outsideConstraint.Code, outsideConstraint.Body.String())
	outsideScope := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/institution",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusForbidden, outsideScope.Code, "PAT institution status = %d: %s", outsideScope.Code, outsideScope.Body.String())
	sessionOnly := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusUnauthorized, sessionOnly.Code, "PAT session-only status = %d: %s", sessionOnly.Code, sessionOnly.Body.String())
	list := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me/tokens",
		nil,
		session.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, list.Code, "list PAT response")
	require.NotContains(t, list.Body.String(), created.Credential, "list PAT response")
	require.NotContains(t, list.Body.String(), model.HashToken(created.Credential), "list PAT response")
	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{Limit: 200, Visibility: store.AuditVisibilityScope{InstitutionWide: true}},
	)
	require.NoError(t, err)
	encodedAudits, err := json.Marshal(audits)
	require.NoError(t, err)
	require.False(t, bytes.Contains(encodedAudits, []byte(created.Credential)), "personal access token credential or hash leaked into audit events")
	require.False(t, bytes.Contains(encodedAudits, []byte(model.HashToken(created.Credential))), "personal access token credential or hash leaked into audit events")
	disable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens/"+created.Token.ID.String()+"/disable",
		nil,
		session.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, disable.Code, "disable PAT status = %d: %s", disable.Code, disable.Body.String())
	whileDisabled := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusUnauthorized, whileDisabled.Code, "disabled PAT status = %d: %s", whileDisabled.Code, whileDisabled.Body.String())
	enable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/tokens/"+created.Token.ID.String()+"/enable",
		nil,
		session.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, enable.Code, "enable PAT status = %d: %s", enable.Code, enable.Body.String())
	afterEnable := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusOK, afterEnable.Code, "reenabled PAT status = %d: %s", afterEnable.Code, afterEnable.Body.String())
	revoke := performJSONRequest(
		helper.Handler(),
		http.MethodDelete,
		"/api/v1/users/me/tokens/"+created.Token.ID.String(),
		nil,
		session.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, revoke.Code, "revoke PAT status = %d: %s", revoke.Code, revoke.Body.String())
	afterRevoke := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		created.Credential,
	)
	require.Equal(t, http.StatusUnauthorized, afterRevoke.Code, "revoked PAT status = %d: %s", afterRevoke.Code, afterRevoke.Body.String())
}

func TestAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	seedAuthenticationAuditInstitution(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Authentication.LoginRateLimit.MaximumAttempts = 2
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
		}),
		testlib.WithStore(persistence),
	)

	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username:    "integration-user",
		Email:       "integration-user@example.edu",
		DisplayName: "Integration User",
	}, Password: password})
	require.NoError(t, appErr)

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
	require.Equal(t, http.StatusUnauthorized, wrong.Code, "wrong-password status = %d: %s", wrong.Code, wrong.Body.String())
	assertProblemCode(t, wrong, "authentication.credentials_invalid")

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
	require.Equal(t, http.StatusOK, login.Code, "login status = %d: %s", login.Code, login.Body.String())
	first := decodeAuthenticationResponse(t, login)
	require.NotNil(t, first.User)
	require.Equal(t, user.ID, first.User.ID)
	require.NotEmpty(t, first.Tokens.AccessToken)
	require.NotEmpty(t, first.Tokens.RefreshToken)
	require.Empty(t, login.Result().Cookies())
	loginAudits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{
		Action: "authentication.login", Resource: &model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err, "login audits")
	require.Len(t, loginAudits, 2, "login audits")
	outcomes := map[model.AuditStatus]*model.AuditEvent{}
	for _, event := range loginAudits {
		outcomes[event.Status] = event
	}
	failed := outcomes[model.AuditStatusFail]
	require.NotNil(t, failed)
	require.Empty(t, failed.ActorID)
	require.Equal(t, "authentication.credentials_invalid", failed.ErrorCode)
	require.Equal(t, "password", failed.AuthMethod)
	succeeded := outcomes[model.AuditStatusSuccess]
	require.NotNil(t, succeeded)
	require.Equal(t, user.ID, succeeded.ActorID)
	require.Contains(t, string(succeeded.Result), first.Session.ID.String())
	encodedLoginAudits, err := json.Marshal(loginAudits)
	require.NoError(t, err)
	require.NotContains(t, string(encodedLoginAudits), password)
	require.NotContains(t, string(encodedLoginAudits), "wrong password")

	me := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, me.Code, "me status = %d: %s", me.Code, me.Body.String())

	refresh := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		first.Tokens.RefreshToken,
	)
	require.Equal(t, http.StatusOK, refresh.Code, "refresh status = %d: %s", refresh.Code, refresh.Body.String())
	second := decodeAuthenticationResponse(t, refresh)
	require.NotEqual(t, first.Tokens.AccessToken, second.Tokens.AccessToken, "refresh did not rotate both credentials")
	require.NotEqual(t, first.Tokens.RefreshToken, second.Tokens.RefreshToken, "refresh did not rotate both credentials")
	oldAccess := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		first.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, oldAccess.Code, "old access status")
	assertProblemCode(t, oldAccess, "authentication.token_invalid")

	replay := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		first.Tokens.RefreshToken,
	)
	require.Equal(t, http.StatusUnauthorized, replay.Code, "refresh replay status = %d: %s", replay.Code, replay.Body.String())
	assertProblemCode(t, replay, "authentication.token_invalid")
	revokedByReplay := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		second.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, revokedByReplay.Code, "replay-family access status")
	assertProblemCode(t, revokedByReplay, "authentication.token_invalid")

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
	require.Equal(t, http.StatusOK, loginAgain.Code, "second login status = %d: %s", loginAgain.Code, loginAgain.Body.String())
	third := decodeAuthenticationResponse(t, loginAgain)
	logout := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		third.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, logout.Code, "logout status = %d: %s", logout.Code, logout.Body.String())
	afterLogout := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		third.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, afterLogout.Code, "post-logout access status")
	assertProblemCode(t, afterLogout, "authentication.token_invalid")

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
	require.Equal(t, http.StatusOK, loginBeforeDisable.Code, "pre-disable login status = %d: %s", loginBeforeDisable.Code, loginBeforeDisable.Body.String())
	fourth := decodeAuthenticationResponse(t, loginBeforeDisable)
	currentUser, err := persistence.User().Get(context.Background(), user.ID)
	require.NoError(t, err)
	disabledAt := model.GetMillis()
	auditAttempt, err := persistence.Audit().Save(context.Background(), &model.AuditEvent{
		Action: string(model.ActionUserManage), Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()},
		ScopeType: model.RoleScopeInstitution, ScopeID: model.NewID(), Status: model.AuditStatusAttempt, NodeID: "authentication-integration",
	})
	require.NoError(t, err)
	_, err = persistence.User().SetDisabled(context.Background(), storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
		ID: user.ID, ExpectedRevision: currentUser.Revision, Disabled: true,
		ChangedAt: disabledAt, RevocationReason: model.SessionRevocationAccountDisabled,
		AuditEventID: auditAttempt.ID, AuditAt: disabledAt,
	}))
	require.NoError(t, err)
	refreshAfterDisable := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/refresh",
		nil,
		fourth.Tokens.RefreshToken,
	)
	require.Equal(t, http.StatusUnauthorized, refreshAfterDisable.Code, "disabled-user refresh status")
	assertProblemCode(t, refreshAfterDisable, "authentication.token_invalid")
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
		require.Equal(t, want, rateLimited.Code, "rate-limit attempt %d status = %d, want %d: %s", attempt, rateLimited.Code, want, rateLimited.Body.String())
		wantCode := "authentication.credentials_invalid"
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
		require.NotContains(t, logs, secret, "authentication secret appeared in logs")
	}
}

func TestBrowserCookieAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	seedAuthenticationAuditInstitution(t, persistence)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.edu"
		}),
		testlib.WithStore(persistence),
	)
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username: "browser-user", Email: "browser-user@example.edu",
		DisplayName: "Browser User",
	}, Password: password})
	require.NoError(t, appErr)

	login := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]any{
			"login_id": user.Username, "password": password,
			"client_type": model.SessionClientWeb,
		},
		"",
	)
	require.Equal(t, http.StatusOK, login.Code, "browser login status = %d: %s", login.Code, login.Body.String())
	var loginBody authenticationResponse
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &loginBody))
	require.NotNil(t, loginBody.User)
	require.NotNil(t, loginBody.Session)
	require.Nil(t, loginBody.Tokens)
	loginCookies := cookieMap(login.Result().Cookies())
	assertBrowserCookieContract(t, loginCookies)
	for _, cookie := range loginCookies {
		require.NotContains(t, login.Body.String(), cookie.Value, "browser credential appeared in body or logs")
		require.NotContains(t, helper.Logs.String(), cookie.Value, "browser credential appeared in body or logs")
	}

	me := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "", "",
	)
	require.Equal(t, http.StatusOK, me.Code, "browser current-user status = %d: %s", me.Code, me.Body.String())
	ambiguous := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "another-access-token", "",
	)
	require.Equal(t, http.StatusBadRequest, ambiguous.Code, "ambiguous browser credential status = %d: %s", ambiguous.Code, ambiguous.Body.String())

	missingLogoutCSRF := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/logout",
		nil, loginCookies, "", "",
	)
	require.Equal(t, http.StatusForbidden, missingLogoutCSRF.Code, "missing logout CSRF status = %d: %s", missingLogoutCSRF.Code, missingLogoutCSRF.Body.String())
	assertProblemCode(t, missingLogoutCSRF, "authentication.csrf.invalid")
	missingRefreshCSRF := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/refresh",
		nil, loginCookies, "", "",
	)
	require.Equal(t, http.StatusForbidden, missingRefreshCSRF.Code, "missing refresh CSRF status = %d: %s", missingRefreshCSRF.Code, missingRefreshCSRF.Body.String())
	assertProblemCode(t, missingRefreshCSRF, "authentication.csrf.invalid")

	refresh := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/refresh",
		nil, loginCookies, "",
		loginCookies[httpapi.BrowserCSRFCookieName].Value,
	)
	require.Equal(t, http.StatusOK, refresh.Code, "browser refresh status = %d: %s", refresh.Code, refresh.Body.String())
	var refreshBody authenticationResponse
	require.NoError(t, json.Unmarshal(refresh.Body.Bytes(), &refreshBody))
	require.NotNil(t, refreshBody.Session)
	require.Nil(t, refreshBody.Tokens)
	refreshedCookies := cookieMap(refresh.Result().Cookies())
	assertBrowserCookieContract(t, refreshedCookies)
	for name, previous := range loginCookies {
		require.NotEqual(t, previous.Value, refreshedCookies[name].Value, "%s was not rotated", name)
	}

	oldAccess := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, loginCookies, "", "",
	)
	require.Equal(t, http.StatusUnauthorized, oldAccess.Code, "old browser access status")
	currentAccess := performBrowserJSONRequest(
		helper.Handler(), http.MethodGet, "/api/v1/users/me",
		nil, refreshedCookies, "", "",
	)
	require.Equal(t, http.StatusOK, currentAccess.Code, "refreshed browser access status = %d: %s", currentAccess.Code, currentAccess.Body.String())

	logout := performBrowserJSONRequest(
		helper.Handler(), http.MethodPost, "/api/v1/auth/logout",
		nil, refreshedCookies, "",
		refreshedCookies[httpapi.BrowserCSRFCookieName].Value,
	)
	require.Equal(t, http.StatusNoContent, logout.Code, "browser logout status = %d: %s", logout.Code, logout.Body.String())
	cleared := cookieMap(logout.Result().Cookies())
	require.Len(t, cleared, 4)
	for _, cookie := range cleared {
		require.Negative(t, cookie.MaxAge)
	}
}

func TestSessionManagementIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	persistence := openAuthenticationStore(t, dataSource)
	seedInitialAuthenticationAccessPolicy(t, persistence)
	seedAuthenticationAuditInstitution(t, persistence)
	helper := testlib.Setup(t, testlib.WithStore(persistence))
	password := "correct horse battery staple"
	user, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username:    "session-user",
		Email:       "session-user@example.edu",
		DisplayName: "Session User",
	}, Password: password})
	require.NoError(t, appErr)

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
	require.Equal(t, http.StatusOK, list.Code, "list sessions status = %d: %s", list.Code, list.Body.String())
	var sessions []*model.Session
	sessions = decodeSessionList(t, list)
	require.Len(t, sessions, 2)
	for _, session := range sessions {
		require.Equal(t, user.ID, session.UserID)
		require.False(t, session.RevokedAt.Valid)
	}
	require.NotContains(t, list.Body.String(), "token_hash")
	require.NotContains(t, list.Body.String(), firstLogin.Tokens.AccessToken)
	require.NotContains(t, list.Body.String(), firstLogin.Tokens.RefreshToken)

	unknownField := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": firstLogin.Session.ID, "unknown": true},
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusBadRequest, unknownField.Code, "unknown revoke field status")

	revokeFirst := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": firstLogin.Session.ID},
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, revokeFirst.Code, "revoke session status = %d: %s", revokeFirst.Code, revokeFirst.Body.String())
	firstAfterRevoke := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		firstLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, firstAfterRevoke.Code, "revoked access status")
	secondStillActive := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, secondStillActive.Code, "unrelated session status")

	invalidID := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke",
		map[string]any{"session_id": "not-an-id"},
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusBadRequest, invalidID.Code, "invalid session id status")
	assertProblemCode(t, invalidID, "session.id.invalid")

	otherUser, appErr := helper.App.CreateLocalUser(context.Background(), app.CreateLocalUserCommand{User: &model.User{
		Username:    "other-session-user",
		Email:       "other-session-user@example.edu",
		DisplayName: "Other Session User",
	}, Password: password})
	require.NoError(t, appErr)
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
	require.Equal(t, http.StatusNotFound, crossUserRevoke.Code, "cross-user revoke status = %d: %s", crossUserRevoke.Code, crossUserRevoke.Body.String())
	assertProblemCode(t, crossUserRevoke, "session.not_found")
	otherStillActive := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		otherLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusOK, otherStillActive.Code, "cross-user session status")

	revokeAll := performJSONRequest(
		helper.Handler(),
		http.MethodPost,
		"/api/v1/users/me/sessions/revoke-all",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusNoContent, revokeAll.Code, "revoke-all status = %d: %s", revokeAll.Code, revokeAll.Body.String())
	secondAfterRevokeAll := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/users/me",
		nil,
		secondLogin.Tokens.AccessToken,
	)
	require.Equal(t, http.StatusUnauthorized, secondAfterRevokeAll.Code, "revoke-all access status")

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
	require.Equal(t, http.StatusOK, activeAfterRevokeAll.Code, "active sessions after revoke-all status")
	require.Len(t, sessions, 1, "active sessions after revoke-all status")
	require.Equal(t, thirdLogin.Session.ID, sessions[0].ID, "active sessions after revoke-all status")

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
		require.NotContains(t, logs, token, "raw session-management credential appeared in logs")
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
	require.Equal(t, http.StatusOK, response.Code, "login status = %d: %s", response.Code, response.Body.String())
	return decodeAuthenticationResponse(t, response)
}

type authenticationResponse struct {
	User    *model.User
	Session *model.Session
	Tokens  *model.AuthenticationTokens `json:"tokens"`
}

type wireUserProfileResponse struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ArchivedAt     time.Time `json:"archived_at"`
	Username       string    `json:"username"`
	Email          string    `json:"email"`
	EmailVerified  bool      `json:"email_verified"`
	DisplayName    string    `json:"display_name"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Locale         string    `json:"locale"`
	Timezone       string    `json:"timezone"`
	LastLoginAt    time.Time `json:"last_login_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	DisabledAt     time.Time `json:"disabled_at"`
}

func (w *wireUserProfileResponse) model() *model.User {
	if w == nil {
		return nil
	}
	return &model.User{
		ID: model.UserID(w.ID), CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt, ArchivedAt: integrationOptionalTime(w.ArchivedAt),
		Username: w.Username, Email: w.Email, EmailVerified: w.EmailVerified,
		DisplayName: w.DisplayName, FirstName: w.FirstName, LastName: w.LastName,
		Locale: w.Locale, Timezone: w.Timezone,
		LastLoginAt:    integrationOptionalTime(w.LastLoginAt),
		LastActivityAt: integrationOptionalTime(w.LastActivityAt),
		DisabledAt:     integrationOptionalTime(w.DisabledAt),
	}
}

type wireSessionResponse struct {
	ID                     string    `json:"id"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	ArchivedAt             time.Time `json:"archived_at"`
	UserID                 string    `json:"user_id"`
	ClientType             string    `json:"client_type"`
	DeviceID               string    `json:"device_id"`
	DeviceName             string    `json:"device_name"`
	AuthenticationMethod   string    `json:"authentication_method"`
	AuthenticationStrength string    `json:"authentication_strength"`
	AuthenticatedAt        time.Time `json:"authenticated_at"`
	MFACompletedAt         time.Time `json:"mfa_completed_at"`
	LastActivityAt         time.Time `json:"last_activity_at"`
	IdleExpiresAt          time.Time `json:"idle_expires_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	RevokedAt              time.Time `json:"revoked_at"`
	RevocationReasonCode   string    `json:"revocation_reason_code"`
	RevocationReason       string    `json:"revocation_reason"`
}

type wireAuthenticationTokensResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func (w *wireAuthenticationTokensResponse) model() *model.AuthenticationTokens {
	if w == nil {
		return nil
	}
	return &model.AuthenticationTokens{
		AccessToken:      w.AccessToken,
		RefreshToken:     w.RefreshToken,
		AccessExpiresAt:  w.AccessExpiresAt,
		RefreshExpiresAt: w.RefreshExpiresAt,
	}
}

func (w *wireSessionResponse) model() *model.Session {
	if w == nil {
		return nil
	}
	return &model.Session{
		ID: model.SessionID(w.ID), CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt, ArchivedAt: integrationOptionalTime(w.ArchivedAt),
		UserID: model.UserID(w.UserID), ClientType: model.SessionClientType(w.ClientType),
		DeviceID: w.DeviceID, DeviceName: w.DeviceName,
		AuthenticationMethod:   w.AuthenticationMethod,
		AuthenticationStrength: model.AuthenticationStrength(w.AuthenticationStrength),
		AuthenticatedAt:        w.AuthenticatedAt,
		MFACompletedAt:         integrationOptionalTime(w.MFACompletedAt),
		LastActivityAt:         w.LastActivityAt,
		IdleExpiresAt:          w.IdleExpiresAt, ExpiresAt: w.ExpiresAt,
		RevokedAt: integrationOptionalTime(w.RevokedAt), RevocationReason: model.SessionRevocationReason(w.RevocationReasonCode),
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
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
	decoded := authenticationResponse{User: wire.User.model(), Session: wire.Session.model(), Tokens: wire.Tokens.model()}
	require.NotNil(t, decoded.Session)
	require.NotNil(t, decoded.Tokens)
	return decoded
}

func decodeSessionList(t *testing.T, response *httptest.ResponseRecorder) []*model.Session {
	t.Helper()
	var wire []*wireSessionResponse
	require.NoError(t, json.Unmarshal(integrationListItems(t, response.Body.Bytes()), &wire))
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
		request.Header.Set(httpapi.BrowserCSRFHeader, csrf)
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
	require.Equal(t, "PROCTOR_ACCESS", httpapi.BrowserAccessCookieName, "browser cookie or CSRF public name changed")
	require.Equal(t, "PROCTOR_REFRESH", httpapi.BrowserRefreshCookieName, "browser cookie or CSRF public name changed")
	require.Equal(t, "PROCTOR_CSRF_BINDING", httpapi.BrowserCSRFBindingCookieName, "browser cookie or CSRF public name changed")
	require.Equal(t, "PROCTOR_CSRF", httpapi.BrowserCSRFCookieName, "browser cookie or CSRF public name changed")
	require.Equal(t, "X-Proctor-CSRF-Token", httpapi.BrowserCSRFHeader, "browser cookie or CSRF public name changed")
	require.Len(t, cookies, 4)
	access := cookies["PROCTOR_ACCESS"]
	refresh := cookies["PROCTOR_REFRESH"]
	binding := cookies["PROCTOR_CSRF_BINDING"]
	csrf := cookies["PROCTOR_CSRF"]
	require.NotNil(t, access)
	require.NotNil(t, refresh)
	require.NotNil(t, binding)
	require.NotNil(t, csrf)
	require.True(t, access.HttpOnly)
	require.True(t, refresh.HttpOnly)
	require.True(t, binding.HttpOnly)
	require.False(t, csrf.HttpOnly)
	require.True(t, access.Secure)
	require.True(t, refresh.Secure)
	require.True(t, binding.Secure)
	require.True(t, csrf.Secure)
	require.Equal(t, http.SameSiteLaxMode, access.SameSite)
	require.Equal(t, http.SameSiteLaxMode, refresh.SameSite)
	require.Equal(t, http.SameSiteLaxMode, binding.SameSite)
	require.Equal(t, http.SameSiteLaxMode, csrf.SameSite)
	require.Empty(t, access.Domain)
	require.Empty(t, refresh.Domain)
	require.Empty(t, binding.Domain)
	require.Empty(t, csrf.Domain)
	require.Equal(t, "/", access.Path)
	require.Equal(t, "/api/v1/auth/refresh", refresh.Path)
	require.Equal(t, "/", binding.Path)
	require.Equal(t, "/", csrf.Path)
}

func assertProblemCode(
	t *testing.T,
	response *httptest.ResponseRecorder,
	want string,
) {
	t.Helper()
	require.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	var problem httpapi.Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, want, problem.Code, "problem code")
}

func openAuthenticationStore(t *testing.T, dataSource string) *sqlstore.SQLStore {
	t.Helper()
	database := config.Default().Database
	database.DataSource = dataSource
	settings := sqlstore.SettingsFromConfig(database)
	migrator, err := sqlstore.NewMigrator(context.Background(), settings)
	require.NoError(t, err)
	if err := migrator.Up(); err != nil {
		_ = migrator.Close()
		require.NoError(t, err)
	}
	require.NoError(t, migrator.Close())
	persistence, err := sqlstore.New(context.Background(), settings)
	require.NoError(t, err)
	if _, err := persistence.GetMaster().Exec(context.Background(), `
		TRUNCATE TABLE
			mail_key_state, mail_fanout_bundles, mail_payload_keys, external_login_states, installation_states, access_policy_transitions, access_policies, audit_events, mfa_recovery_codes, mfa_credentials,
			user_tokens, personal_access_tokens, session_credentials, sessions,
			role_bindings, roles, class_members, academic_unit_members,
			affiliations, password_credentials, external_identities, users,
			classes, academic_periods, programme_levels, programmes,
			academic_units, institutions CASCADE`); err != nil {
		_ = persistence.Close()
		require.NoError(t, err)
	}
	if _, err := persistence.GetMaster().Exec(context.Background(), `INSERT INTO mail_key_state(singleton, required_primary_key_id, active_rekey_job_id, updated_at) VALUES (TRUE, NULL, NULL, clock_timestamp())`); err != nil {
		_ = persistence.Close()
		require.NoError(t, err)
	}
	return persistence
}

// seedInitialAuthenticationAccessPolicy is for older real-graph fixtures that
// deliberately build their Institution and Users directly instead of running
// the installation bootstrap aggregate. Authentication is still exercised
// against an authoritative PostgreSQL Access Policy in those fixtures.
func seedInitialAuthenticationAccessPolicy(t *testing.T, persistence *sqlstore.SQLStore) {
	seedAuthenticationAccessPolicy(t, persistence, nil)
}

func seedAuthenticationAccessPolicy(
	t *testing.T,
	persistence *sqlstore.SQLStore,
	providerAdmissions map[string]model.ProviderAdmissionMode,
) {
	t.Helper()
	if providerAdmissions == nil {
		providerAdmissions = map[string]model.ProviderAdmissionMode{}
	}
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), model.NowUTC())
	providers, err := json.Marshal(providerAdmissions)
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(context.Background(), `
		INSERT INTO access_policies (
			singleton, id, revision, created_at, updated_at,
			local_login_enabled, public_registration_enabled,
			invitation_admission_enabled, invitation_local_credential_enabled,
			desktop_authorization_enabled, provider_admissions
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb)`,
		policy.ID.String(), policy.Revision, policy.CreatedAt, policy.UpdatedAt,
		policy.LocalLoginEnabled, policy.PublicRegistrationEnabled,
		policy.InvitationAdmissionEnabled, policy.InvitationLocalCredentialEnabled,
		policy.DesktopAuthorizationEnabled, providers,
	)
	require.NoError(t, err)
}

// Legacy authentication fixtures create Users directly; self-session mutations
// still need the installation singleton that owns their durable audit scope.
func seedAuthenticationAuditInstitution(t *testing.T, persistence *sqlstore.SQLStore) {
	t.Helper()
	_, err := persistence.Institution().Save(context.Background(), &model.Institution{
		Name: "authentication-university", DisplayName: "Authentication University",
	})
	require.NoError(t, err)
}
