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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/platform/externalauth"
)

func TestProviderAuthorizationCodePKCEAndClaims(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
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
		assert.NoError(t, request.ParseForm())
		username, password, basic := request.BasicAuth()
		validClient := basic &&
			username == clientID &&
			password == clientSecret
		if !validClient {
			validClient = request.Form.Get("client_id") == clientID &&
				request.Form.Get("client_secret") == clientSecret
		}
		assert.True(t, validClient, "token request did not authenticate the client: %#v", request.Form)
		assert.Equal(t, code, request.Form.Get("code"), "token request code")
		assert.NotEmpty(t, request.Form.Get("code_verifier"), "token request has no PKCE verifier")
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
	require.NoError(t, err)
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
	require.NoError(t, err)
	authorizationURL, err := url.Parse(challenge.RedirectURL)
	require.NoError(t, err)
	mutex.Lock()
	nonce = authorizationURL.Query().Get("nonce")
	mutex.Unlock()
	require.Equal(t, "/auth", authorizationURL.Path)
	require.Equal(t, state, authorizationURL.Query().Get("state"))
	require.Equal(t, callbackURL, authorizationURL.Query().Get("redirect_uri"))
	require.Equal(t, oauth2.S256ChallengeFromVerifier(proof), authorizationURL.Query().Get("code_challenge"))
	require.Equal(t, "S256", authorizationURL.Query().Get("code_challenge_method"))
	require.NotEmpty(t, nonce)
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
	require.NoError(t, err)
	require.Equal(t, "campus-oidc", assertion.ProviderID)
	require.Equal(t, "opaque-oidc-subject", assertion.Subject)
	require.Equal(t, "oidc.student", assertion.Username)
	require.Equal(t, "oidc.student@example.edu", assertion.Email)
	require.True(t, assertion.EmailVerified)
	require.Equal(t, "example.edu", assertion.HomeOrganization)
	require.Equal(t, model.AuthenticationMultiFactor, assertion.AuthenticationStrength)
	require.Len(t, assertion.Affiliations, 2)
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
	require.NoError(t, err)
	provider := created.(*Provider)
	state := model.NewCredentialToken()
	proof := model.NewCredentialToken()
	callbackURL := "https://proctor.example.edu/callback"
	_, err = provider.Begin(
		context.Background(),
		externalauth.BeginRequest{
			CallbackURL: callbackURL, State: state, Proof: proof,
		},
	)
	require.NoError(t, err)
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
	require.ErrorIs(t, err, externalauth.ErrAuthenticationRejected)
	require.NotContains(t, err.Error(), secretBody)
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

func TestProviderFreshProofRequiresAuthenticationTimeAfterBoundFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		offset  time.Duration
		missing bool
		valid   bool
	}{
		{name: "fresh", valid: true}, {name: "SSO history", offset: -time.Minute}, {name: "missing auth_time", missing: true}, {name: "future", offset: time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			var issuer, nonce string
			discovery := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "fresh-key", Algorithm: coreoidc.RS256}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					discovery.ServeHTTP(w, r)
					return
				}
				claims := map[string]any{"iss": issuer, "aud": "proctor-client", "sub": "existing-subject", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": nonce, "email": "student@example.edu", "email_verified": true}
				if !test.missing {
					claims["auth_time"] = now.Add(test.offset).Unix()
				}
				encoded, _ := json.Marshal(claims)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "opaque-token", "token_type": "Bearer", "id_token": oidctest.SignIDToken(key, "fresh-key", coreoidc.RS256, string(encoded))})
			}))
			defer server.Close()
			issuer = server.URL
			discovery.SetIssuer(issuer)
			settings := testSettings(issuer)
			settings.AutoProvision = false
			settings.Claims.AllowedHomeOrganizations = nil
			created, err := NewFactory().New(settings)
			require.NoError(t, err)
			provider := created.(*Provider)
			provider.now = func() time.Time { return now.Add(500 * time.Millisecond) }
			state, proof := model.NewCredentialToken(), model.NewCredentialToken()
			request := externalauth.BeginRequest{CallbackURL: "https://proctor.example.edu/callback", State: state, Proof: proof, FreshAuthentication: true}
			start, err := provider.Begin(context.Background(), request)
			require.NoError(t, err)
			u, err := url.Parse(start.RedirectURL)
			require.NoError(t, err)
			nonce = u.Query().Get("nonce")
			require.Equal(t, "login", u.Query().Get("prompt"), "fresh OIDC request lacks authenticated freshness constraints")
			require.Equal(t, "0", u.Query().Get("max_age"), "fresh OIDC request lacks authenticated freshness constraints")
			require.NotEmpty(t, nonce, "fresh OIDC request lacks authenticated freshness constraints")
			assertion, err := provider.Complete(context.Background(), externalauth.CompleteRequest{CallbackURL: request.CallbackURL, State: state, Proof: proof, AuthenticationStartedAt: now.Add(100 * time.Millisecond), Callback: model.ExternalAuthenticationCallback{Values: map[string][]string{"state": {state}, "code": {"fresh-code"}}}})
			if test.valid {
				require.NoError(t, err)
				require.Equal(t, now.UnixMilli(), assertion.AuthenticatedAt)
			} else {
				require.Error(t, err, "unverified primary freshness was accepted")
				require.Nil(t, assertion, "unverified primary freshness was accepted")
			}
		})
	}
}
