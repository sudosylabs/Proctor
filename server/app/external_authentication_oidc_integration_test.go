//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"golang.org/x/oauth2"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestOIDCExternalAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const (
		providerID = "campus-oidc"
		clientID   = "proctor-client"
		code       = "sensitive-oidc-code"
		subject    = "sensitive-oidc-subject"
	)
	var (
		issuer            string
		expectedNonce     string
		expectedChallenge string
		transactionMutex  sync.Mutex
	)
	oidcServer := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{
			PublicKey: privateKey.Public(),
			KeyID:     "test-key",
			Algorithm: coreoidc.RS256,
		}},
	}
	providerServer := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/token" {
			oidcServer.ServeHTTP(writer, request)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
		}
		transactionMutex.Lock()
		nonce := expectedNonce
		challenge := expectedChallenge
		transactionMutex.Unlock()
		if request.Form.Get("code") != code ||
			oauth2.S256ChallengeFromVerifier(
				request.Form.Get("code_verifier"),
			) != challenge {
			t.Errorf("invalid token exchange: %#v", request.Form)
		}
		now := time.Now()
		rawClaims, _ := json.Marshal(map[string]any{
			"iss": issuer, "aud": clientID, "sub": subject,
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(), "auth_time": now.Add(-time.Minute).Unix(),
			"nonce": nonce, "preferred_username": "oidc.student",
			"email": "oidc.student@example.edu", "email_verified": true,
			"given_name": "OIDC", "family_name": "Student",
			"schacHomeOrganization": "example.edu",
			"eduPersonAffiliation":  []string{"student"},
			"amr":                   []string{"pwd", "mfa"},
		})
		idToken := oidctest.SignIDToken(
			privateKey,
			"test-key",
			coreoidc.RS256,
			string(rawClaims),
		)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "provider-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	}))
	defer providerServer.Close()
	issuer = providerServer.URL
	oidcServer.SetIssuer(issuer)

	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "http://proctor.example.test"
			cfg.Authentication.External.Providers =
				[]config.ExternalAuthenticationProvider{{
					ID:          providerID,
					Type:        config.ExternalAuthenticationTypeOIDC,
					DisplayName: "Campus OIDC",
					Enabled:     true, AutoProvision: true,
					OIDC: &config.OIDCProvider{
						Issuer: issuer, ClientID: clientID,
						ClientSecret: "client-secret",
						Scopes:       []string{"openid", "profile", "email"},
						Timeout: config.Duration{
							Duration: 5 * time.Second,
						},
						MaxResponseBytes: 64 * 1024,
					},
					Claims: config.ExternalClaimMapping{
						Subject:            "sub",
						Username:           "preferred_username",
						Email:              "email",
						EmailVerifiedClaim: "email_verified",
						FirstName:          "given_name", LastName: "family_name",
						HomeOrganization:         "schacHomeOrganization",
						Affiliation:              "eduPersonAffiliation",
						AllowedHomeOrganizations: []string{"example.edu"},
						MultiFactorAttribute:     "amr",
						MultiFactorValues:        []string{"mfa"},
					},
				}}
		}),
		testlib.WithServerOptions(app.WithStore(persistence)),
	)
	if _, err := persistence.Institution().Save(
		context.Background(),
		&model.Institution{
			Name:        "oidc-auth-university",
			DisplayName: "OIDC Authentication University",
		},
	); err != nil {
		t.Fatal(err)
	}

	begin := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=desktop&return_to=%2Foidc-complete",
		nil,
		"",
	)
	if begin.Code != http.StatusSeeOther {
		t.Fatalf("begin status = %d: %s", begin.Code, begin.Body.String())
	}
	authorizationURL, err := url.Parse(begin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	transactionMutex.Lock()
	expectedNonce = authorizationURL.Query().Get("nonce")
	expectedChallenge = authorizationURL.Query().Get("code_challenge")
	transactionMutex.Unlock()
	state := authorizationURL.Query().Get("state")
	if state == "" || expectedNonce == "" || expectedChallenge == "" ||
		authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %q", authorizationURL.String())
	}
	var bindingCookie *http.Cookie
	for _, cookie := range begin.Result().Cookies() {
		if cookie.Name == api.BrowserExternalLoginCookieName {
			bindingCookie = cookie
		}
	}
	if bindingCookie == nil {
		t.Fatal("external login binding cookie is missing")
	}
	callbackPath := model.APIURLSuffix + "/auth/providers/" +
		providerID + "/callback?state=" + url.QueryEscape(state) +
		"&code=" + url.QueryEscape(code)
	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		callbackPath,
		nil,
	)
	callbackRequest.AddCookie(bindingCookie)
	callback := httptest.NewRecorder()
	helper.Server.Handler().ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusSeeOther ||
		callback.Header().Get("Location") != "/oidc-complete" {
		t.Fatalf(
			"callback status=%d location=%q body=%s",
			callback.Code,
			callback.Header().Get("Location"),
			callback.Body.String(),
		)
	}
	identity, err := persistence.ExternalIdentity().GetByProviderSubject(
		context.Background(),
		providerID,
		subject,
	)
	if err != nil {
		t.Fatal(err)
	}
	user, err := persistence.User().Get(
		context.Background(),
		identity.UserId,
	)
	if err != nil || user.Username != "oidc.student" ||
		user.Email != "oidc.student@example.edu" ||
		!user.EmailVerified {
		t.Fatalf("provisioned OIDC user = %#v, %v", user, err)
	}
	sessions, err := persistence.Session().ListActiveByUser(
		context.Background(),
		user.Id,
		model.GetMillis(),
	)
	if err != nil || len(sessions) != 1 ||
		sessions[0].AuthenticationMethod !=
			config.ExternalAuthenticationTypeOIDC ||
		sessions[0].AuthenticationStrength !=
			model.AuthenticationMultiFactor {
		t.Fatalf("OIDC session = %#v, %v", sessions, err)
	}

	deniedBegin := performJSONRequest(
		helper.Server.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=desktop",
		nil,
		"",
	)
	deniedAuthorizationURL, err := url.Parse(
		deniedBegin.Header().Get("Location"),
	)
	if err != nil || deniedBegin.Code != http.StatusSeeOther {
		t.Fatalf(
			"denied-flow begin status=%d location=%q error=%v",
			deniedBegin.Code,
			deniedBegin.Header().Get("Location"),
			err,
		)
	}
	var deniedBindingCookie *http.Cookie
	for _, cookie := range deniedBegin.Result().Cookies() {
		if cookie.Name == api.BrowserExternalLoginCookieName {
			deniedBindingCookie = cookie
		}
	}
	if deniedBindingCookie == nil {
		t.Fatal("denied-flow external login binding cookie is missing")
	}
	deniedCallbackRequest := httptest.NewRequest(
		http.MethodGet,
		model.APIURLSuffix+"/auth/providers/"+providerID+
			"/callback?state="+url.QueryEscape(
			deniedAuthorizationURL.Query().Get("state"),
		)+"&error=access_denied",
		nil,
	)
	deniedCallbackRequest.AddCookie(deniedBindingCookie)
	deniedCallback := httptest.NewRecorder()
	helper.Server.Handler().ServeHTTP(
		deniedCallback,
		deniedCallbackRequest,
	)
	if deniedCallback.Code != http.StatusUnauthorized {
		t.Fatalf(
			"denied-flow callback status=%d body=%s",
			deniedCallback.Code,
			deniedCallback.Body.String(),
		)
	}
	loginAudits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{
			Action: "authentication.external_login",
			Limit:  10,
		},
	)
	if err != nil || len(loginAudits) != 2 {
		t.Fatalf("OIDC login audits = %#v, %v", loginAudits, err)
	}
	for _, event := range loginAudits {
		if event.AuthMethod != config.ExternalAuthenticationTypeOIDC {
			t.Fatalf("OIDC audit authentication method = %#v", event)
		}
	}

	for _, secret := range []string{
		code,
		subject,
		bindingCookie.Value,
		"provider-access-token",
	} {
		if strings.Contains(helper.Logs.String(), secret) {
			t.Fatalf("OIDC secret appeared in logs")
		}
	}
}
