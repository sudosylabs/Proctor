// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"golang.org/x/oauth2"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

func TestProviderAuthorizationCodePKCEAndClaims(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const (
		keyID        = "test-key"
		clientID     = "proctor-client"
		clientSecret = "proctor-secret"
		code         = "sensitive-authorization-code"
	)
	var (
		serverURL string
		nonce     string
		mutex     sync.Mutex
	)
	oidcServer := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{
			PublicKey: privateKey.Public(),
			KeyID:     keyID,
			Algorithm: coreoidc.RS256,
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(
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
		username, password, basic := request.BasicAuth()
		validClient := basic &&
			username == clientID &&
			password == clientSecret
		if !validClient {
			validClient = request.Form.Get("client_id") == clientID &&
				request.Form.Get("client_secret") == clientSecret
		}
		if !validClient ||
			request.Form.Get("code") != code ||
			request.Form.Get("code_verifier") == "" {
			t.Errorf("invalid token request: %#v", request.Form)
		}
		mutex.Lock()
		tokenNonce := nonce
		mutex.Unlock()
		now := time.Now()
		claims := map[string]any{
			"iss": serverURL, "aud": clientID,
			"sub": "opaque-oidc-subject",
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(), "auth_time": now.Add(-time.Minute).Unix(),
			"nonce":                 tokenNonce,
			"preferred_username":    "oidc.student",
			"email":                 "oidc.student@example.edu",
			"email_verified":        true,
			"given_name":            "OIDC",
			"family_name":           "Student",
			"schacHomeOrganization": "example.edu",
			"eduPersonAffiliation":  []string{"student", "member"},
			"amr":                   []string{"pwd", "mfa"},
		}
		rawClaims, _ := json.Marshal(claims)
		idToken := oidctest.SignIDToken(
			privateKey,
			keyID,
			coreoidc.RS256,
			string(rawClaims),
		)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "opaque-access-token",
			"token_type":   "Bearer",
			"id_token":     idToken,
		})
	}))
	defer server.Close()
	serverURL = server.URL
	oidcServer.SetIssuer(server.URL)

	created, err := NewFactory().New(testSettings(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	provider := created.(*Provider)
	state := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	callbackURL := "https://proctor.example.edu/api/v1/auth/providers/campus-oidc/callback"
	challenge, err := provider.Begin(
		context.Background(),
		externalauth.BeginRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(challenge.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	nonce = authorizationURL.Query().Get("nonce")
	mutex.Unlock()
	if authorizationURL.Path != "/auth" ||
		authorizationURL.Query().Get("state") != state ||
		authorizationURL.Query().Get("redirect_uri") != callbackURL ||
		authorizationURL.Query().Get("code_challenge") !=
			oauth2.S256ChallengeFromVerifier(proof) ||
		authorizationURL.Query().Get("code_challenge_method") != "S256" ||
		nonce == "" {
		t.Fatalf("authorization challenge = %q", challenge.RedirectURL)
	}
	assertion, err := provider.Complete(
		context.Background(),
		externalauth.CompleteRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
			Callback: model.ExternalAuthenticationCallback{
				Values: map[string][]string{
					"state": {state},
					"code":  {code},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if assertion.ProviderId != "campus-oidc" ||
		assertion.Subject != "opaque-oidc-subject" ||
		assertion.Username != "oidc.student" ||
		assertion.Email != "oidc.student@example.edu" ||
		!assertion.EmailVerified ||
		assertion.HomeOrganization != "example.edu" ||
		assertion.AuthenticationStrength != model.AuthenticationMultiFactor ||
		len(assertion.Affiliations) != 2 {
		t.Fatalf("Complete() = %#v", assertion)
	}
}

func TestProviderRejectsCallbackAndRedactsExchangeFailure(
	t *testing.T,
) {
	const secretBody = "authorization-code-must-not-leak"
	oidcServer := &oidctest.Server{}
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/token" {
			http.Error(writer, secretBody, http.StatusBadRequest)
			return
		}
		oidcServer.ServeHTTP(writer, request)
	}))
	defer server.Close()
	oidcServer.SetIssuer(server.URL)
	created, err := NewFactory().New(testSettings(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	provider := created.(*Provider)
	state := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	callbackURL := "https://proctor.example.edu/callback"
	if _, err := provider.Begin(
		context.Background(),
		externalauth.BeginRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
		},
	); err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(
		context.Background(),
		externalauth.CompleteRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
			Callback: model.ExternalAuthenticationCallback{
				Values: map[string][]string{
					"state": {state},
					"code":  {secretBody},
				},
			},
		},
	)
	if !errors.Is(err, externalauth.ErrAuthenticationRejected) ||
		strings.Contains(err.Error(), secretBody) {
		t.Fatalf("Complete() error = %v", err)
	}
}

func testSettings(
	issuer string,
) config.ExternalAuthenticationProvider {
	return config.ExternalAuthenticationProvider{
		ID: "campus-oidc", Type: config.ExternalAuthenticationTypeOIDC,
		DisplayName: "Campus OIDC", Enabled: true, AutoProvision: true,
		OIDC: &config.OIDCProvider{
			Issuer: issuer, ClientID: "proctor-client",
			ClientSecret:     "proctor-secret",
			Scopes:           []string{"openid", "profile", "email"},
			Timeout:          config.Duration{Duration: 5 * time.Second},
			MaxResponseBytes: 64 * 1024,
		},
		Claims: config.ExternalClaimMapping{
			Subject: "sub", Username: "preferred_username",
			Email: "email", EmailVerifiedClaim: "email_verified",
			FirstName: "given_name", LastName: "family_name",
			HomeOrganization:         "schacHomeOrganization",
			Affiliation:              "eduPersonAffiliation",
			AllowedHomeOrganizations: []string{"example.edu"},
			MultiFactorAttribute:     "amr",
			MultiFactorValues:        []string{"mfa"},
		},
	}
}
