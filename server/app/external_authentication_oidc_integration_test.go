//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	"sync"
	"testing"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestOIDCExternalAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	const (
		providerID           = "campus-oidc"
		invitationProviderID = "invited-oidc"
		clientID             = "proctor-client"
		code                 = "sensitive-oidc-code"
		linkedSubject        = "sensitive-oidc-subject"
	)
	var (
		issuer            string
		expectedNonce     string
		expectedChallenge string
		transactionMutex  sync.Mutex
		subject           = linkedSubject
		claimedUsername   = "oidc.student"
		claimedEmail      = "oidc.student@example.edu"
		claimedFirstName  = "OIDC"
		claimedLastName   = "Student"
		claimedHomeOrg    = "example.edu"
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
		assert.NoError(t, request.ParseForm())
		transactionMutex.Lock()
		nonce := expectedNonce
		challenge := expectedChallenge
		transactionMutex.Unlock()
		assert.Equal(t, code, request.Form.Get("code"))
		assert.Equal(t, challenge, oauth2.S256ChallengeFromVerifier(
			request.Form.Get("code_verifier"),
		))
		now := time.Now()
		rawClaims, _ := json.Marshal(map[string]any{
			"iss": issuer, "aud": clientID, "sub": subject,
			"exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(), "auth_time": now.Add(-time.Minute).Unix(),
			"nonce": nonce, "preferred_username": claimedUsername,
			"email": claimedEmail, "email_verified": true,
			"given_name": claimedFirstName, "family_name": claimedLastName,
			"schacHomeOrganization": claimedHomeOrg,
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
	seedAuthenticationAccessPolicy(t, persistence, map[string]model.ProviderAdmissionMode{
		providerID:           model.ProviderAdmissionAutoProvision,
		invitationProviderID: model.ProviderAdmissionInvitationRequired,
	})
	oidcProvider := config.ExternalAuthenticationProvider{
		ID: providerID, Type: config.ExternalAuthenticationTypeOIDC, DisplayName: "Campus OIDC",
		Enabled: true, AutoProvision: true,
		OIDC: &config.OIDCProvider{Issuer: issuer, ClientID: clientID, ClientSecret: "client-secret",
			Scopes: []string{"openid", "profile", "email"}, Timeout: config.Duration{Duration: 5 * time.Second}, MaxResponseBytes: 64 * 1024},
		Claims: config.ExternalClaimMapping{
			Subject: "sub", Username: "preferred_username", Email: "email", EmailVerifiedClaim: "email_verified",
			FirstName: "given_name", LastName: "family_name", HomeOrganization: "schacHomeOrganization",
			Affiliation: "eduPersonAffiliation", AllowedHomeOrganizations: []string{"example.edu"},
			MultiFactorAttribute: "amr", MultiFactorValues: []string{"mfa"},
		},
	}
	invitationProvider := oidcProvider
	invitationProvider.ID = invitationProviderID
	invitationProvider.DisplayName = "Invited OIDC"
	invitationProvider.AutoProvision = false
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.test"
			cfg.Authentication.External.Providers = []config.ExternalAuthenticationProvider{oidcProvider, invitationProvider}
		}),
		testlib.WithStore(persistence),
	)
	institution, err := persistence.Institution().Save(
		context.Background(),
		&model.Institution{
			Name:        "oidc-auth-university",
			DisplayName: "OIDC Authentication University",
		},
	)
	require.NoError(t, err)

	begin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=web&return_to=%2Foidc-complete",
		nil,
		"",
	)
	require.Equal(t, http.StatusSeeOther, begin.Code, "begin status = %d: %s", begin.Code, begin.Body.String())
	authorizationURL, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)
	transactionMutex.Lock()
	expectedNonce = authorizationURL.Query().Get("nonce")
	expectedChallenge = authorizationURL.Query().Get("code_challenge")
	transactionMutex.Unlock()
	state := authorizationURL.Query().Get("state")
	require.NotEmpty(t, state)
	require.NotEmpty(t, expectedNonce)
	require.NotEmpty(t, expectedChallenge)
	require.Equal(t, "S256", authorizationURL.Query().Get("code_challenge_method"))
	var bindingCookie *http.Cookie
	for _, cookie := range begin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			bindingCookie = cookie
		}
	}
	require.NotNil(t, bindingCookie, "external login binding cookie is missing")
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
	helper.Handler().ServeHTTP(callback, callbackRequest)
	require.Equal(t, http.StatusSeeOther, callback.Code, "callback status=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	require.Equal(t, "/oidc-complete", callback.Header().Get("Location"), "callback status=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	replayRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	replayRequest.AddCookie(bindingCookie)
	replay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(replay, replayRequest)
	require.Equal(t, http.StatusUnauthorized, replay.Code, "replayed callback status=%d body=%s", replay.Code, replay.Body.String())
	identity, err := persistence.ExternalIdentity().GetByProviderSubject(
		context.Background(),
		providerID,
		subject,
	)
	require.NoError(t, err)
	user, err := persistence.User().Get(
		context.Background(),
		identity.UserID,
	)
	require.NoError(t, err, "provisioned OIDC user = %#v", user)
	require.Equal(t, "oidc.student", user.Username, "provisioned OIDC user = %#v", user)
	require.Equal(t, "oidc.student@example.edu", user.Email, "provisioned OIDC user = %#v", user)
	require.True(t, user.EmailVerified, "provisioned OIDC user = %#v", user)
	affiliations, err := persistence.Affiliation().ListByUser(context.Background(), user.ID)
	require.NoError(t, err)
	academicMemberships, err := persistence.AcademicUnitMember().ListByUser(context.Background(), user.ID)
	require.NoError(t, err)
	classMemberships, err := persistence.ClassMember().ListByUser(context.Background(), user.ID)
	require.NoError(t, err)
	bindings, err := persistence.RoleBinding().ListByUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Empty(t, affiliations, "OIDC claims created academic authority: affiliations")
	require.Empty(t, academicMemberships, "OIDC claims created academic authority: affiliations")
	require.Empty(t, classMemberships, "OIDC claims created academic authority: affiliations")
	require.Empty(t, bindings, "OIDC claims created academic authority: affiliations")
	sessions, err := persistence.Session().ListActiveByUser(
		context.Background(),
		user.ID,
		model.NowUTC(),
	)
	require.NoError(t, err, "OIDC session")
	require.Len(t, sessions, 1, "OIDC session")
	require.Equal(t, config.ExternalAuthenticationTypeOIDC, sessions[0].AuthenticationMethod, "OIDC session")
	require.Equal(t, providerID, sessions[0].AuthenticationProviderID, "OIDC session")
	require.Equal(t, identity.ID, sessions[0].ExternalIdentityID, "OIDC session")
	require.Equal(t, model.AuthenticationMultiFactor, sessions[0].AuthenticationStrength, "OIDC session")

	invitedEmail := "invited.oidc@example.edu"
	claim, pendingInvitation := seedExternalAdmissionInvitation(t, persistence, institution, user, invitedEmail)
	subject, claimedUsername, claimedEmail = "invited-oidc-subject", "invited.oidc", " Provider.OIDC@Example.EDU "
	invitationBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	require.Equal(t, http.StatusSeeOther, invitationBegin.Code, "OIDC invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	require.NotContains(t, invitationBegin.Header().Get("Location"), claim, "OIDC invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	invitationAuthorizationURL, err := url.Parse(invitationBegin.Header().Get("Location"))
	require.NoError(t, err)
	transactionMutex.Lock()
	expectedNonce = invitationAuthorizationURL.Query().Get("nonce")
	expectedChallenge = invitationAuthorizationURL.Query().Get("code_challenge")
	transactionMutex.Unlock()
	var invitationBinding *http.Cookie
	for _, cookie := range invitationBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			invitationBinding = cookie
		}
	}
	require.NotNil(t, invitationBinding, "OIDC invitation binding cookie is missing")
	invitationCallbackPath := model.APIURLSuffix + "/auth/providers/" + invitationProviderID +
		"/callback?state=" + url.QueryEscape(invitationAuthorizationURL.Query().Get("state")) + "&code=" + url.QueryEscape(code)
	invitationCallbackRequest := httptest.NewRequest(http.MethodGet, invitationCallbackPath, nil)
	invitationCallbackRequest.AddCookie(invitationBinding)
	invitationCallback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationCallback, invitationCallbackRequest)
	require.Equal(t, http.StatusSeeOther, invitationCallback.Code, "OIDC invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	require.Equal(t, "/join", invitationCallback.Header().Get("Location"), "OIDC invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	invitedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject)
	require.NoError(t, err)
	invitedUser, err := persistence.User().Get(context.Background(), invitedIdentity.UserID)
	require.NoError(t, err, "OIDC invitation User = %#v", invitedUser)
	require.Equal(t, invitedEmail, invitedUser.Email, "OIDC invitation User = %#v", invitedUser)
	require.True(t, invitedUser.EmailVerified, "OIDC invitation User = %#v", invitedUser)
	acceptedInvitation, err := persistence.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	require.NoError(t, err, "OIDC terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, pendingInvitation.ID, acceptedInvitation.ID, "OIDC terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, model.InvitationAccepted, acceptedInvitation.State, "OIDC terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, invitedUser.ID, acceptedInvitation.AcceptedUserID, "OIDC terminal Invitation = %#v", acceptedInvitation)
	invitedAffiliations, _ := persistence.Affiliation().ListByUser(context.Background(), invitedUser.ID)
	invitedMemberships, _ := persistence.ClassMember().ListByUser(context.Background(), invitedUser.ID)
	invitedBindings, _ := persistence.RoleBinding().ListByUser(context.Background(), invitedUser.ID)
	require.Len(t, invitedAffiliations, 1, "OIDC invitation admission package: affiliations")
	require.Equal(t, acceptedInvitation.AcceptedAffiliationID, invitedAffiliations[0].ID, "OIDC invitation admission package: affiliations")
	require.Len(t, invitedMemberships, 1, "OIDC invitation admission package: affiliations")
	require.Equal(t, acceptedInvitation.AcceptedClassMemberID, invitedMemberships[0].ID, "OIDC invitation admission package: affiliations")
	require.Empty(t, invitedBindings, "OIDC invitation admission package: affiliations")
	invitedSessions, err := persistence.Session().ListActiveByUser(context.Background(), invitedUser.ID, model.NowUTC())
	require.NoError(t, err, "OIDC Invitation proof created an ordinary Session")
	require.Empty(t, invitedSessions, "OIDC Invitation proof created an ordinary Session")
	invitationAudits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{ActorID: invitedUser.ID,
		Action: "invitation.accept", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err, "OIDC Invitation acceptance audits")
	require.Len(t, invitationAudits, 1, "OIDC Invitation acceptance audits")
	require.Equal(t, config.ExternalAuthenticationTypeOIDC, invitationAudits[0].AuthMethod, "OIDC Invitation acceptance audits")
	invitationReplayRequest := httptest.NewRequest(http.MethodGet, invitationCallbackPath, nil)
	invitationReplayRequest.AddCookie(invitationBinding)
	invitationReplay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationReplay, invitationReplayRequest)
	require.Equal(t, http.StatusUnauthorized, invitationReplay.Code, "OIDC invitation callback replay = %d body=%s", invitationReplay.Code, invitationReplay.Body.String())
	require.NotContains(t, helper.Logs.String(), claim, "raw Invitation claim appeared in OIDC logs")
	subject, claimedUsername, claimedEmail = linkedSubject, "oidc.student", "oidc.student@example.edu"

	deniedBegin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=web",
		nil,
		"",
	)
	deniedAuthorizationURL, err := url.Parse(
		deniedBegin.Header().Get("Location"),
	)
	require.NoError(t, err, "denied-flow begin status=%d location=%q error", deniedBegin.Code, deniedBegin.Header().Get("Location"))
	require.Equal(t, http.StatusSeeOther, deniedBegin.Code, "denied-flow begin status=%d location=%q error", deniedBegin.Code, deniedBegin.Header().Get("Location"))
	var deniedBindingCookie *http.Cookie
	for _, cookie := range deniedBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			deniedBindingCookie = cookie
		}
	}
	require.NotNil(t, deniedBindingCookie, "denied-flow external login binding cookie is missing")
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
	helper.Handler().ServeHTTP(
		deniedCallback,
		deniedCallbackRequest,
	)
	require.Equal(t, http.StatusUnauthorized, deniedCallback.Code, "denied-flow callback status=%d body=%s", deniedCallback.Code, deniedCallback.Body.String())
	loginAudits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{
			Action:     "authentication.external_login",
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		},
	)
	require.NoError(t, err, "OIDC login audits")
	require.Len(t, loginAudits, 2, "OIDC login audits")
	for _, event := range loginAudits {
		require.Equal(t, config.ExternalAuthenticationTypeOIDC, event.AuthMethod)
	}

	loginAgain := func() *httptest.ResponseRecorder {
		begin := performJSONRequest(helper.Handler(), http.MethodGet,
			"/api/v1/auth/providers/"+providerID+"/login?client_type=web", nil, "")
		require.Equal(t, http.StatusSeeOther, begin.Code, "OIDC repeat begin status=%d body=%s", begin.Code, begin.Body.String())
		authorizationURL, parseErr := url.Parse(begin.Header().Get("Location"))
		require.NoError(t, parseErr)
		transactionMutex.Lock()
		expectedNonce = authorizationURL.Query().Get("nonce")
		expectedChallenge = authorizationURL.Query().Get("code_challenge")
		transactionMutex.Unlock()
		var repeatBinding *http.Cookie
		for _, cookie := range begin.Result().Cookies() {
			if cookie.Name == httpapi.BrowserExternalLoginCookieName {
				repeatBinding = cookie
			}
		}
		require.NotNil(t, repeatBinding, "OIDC repeat binding cookie is missing")
		request := httptest.NewRequest(http.MethodGet, model.APIURLSuffix+"/auth/providers/"+providerID+
			"/callback?state="+url.QueryEscape(authorizationURL.Query().Get("state"))+"&code="+url.QueryEscape(code), nil)
		request.AddCookie(repeatBinding)
		response := httptest.NewRecorder()
		helper.Handler().ServeHTTP(response, request)
		return response
	}

	claimedUsername, claimedEmail = "changed.oidc", "changed.oidc@example.edu"
	claimedFirstName, claimedLastName = "Changed", "OIDC Profile"
	require.Equal(t, http.StatusSeeOther, loginAgain().Code, "OIDC changed-claim login status")
	unchangedUser, err := persistence.User().Get(context.Background(), user.ID)
	require.NoError(t, err, "OIDC claims overwrote established User = %#v", unchangedUser)
	require.Equal(t, "oidc.student", unchangedUser.Username, "OIDC claims overwrote established User = %#v", unchangedUser)
	require.Equal(t, "oidc.student@example.edu", unchangedUser.Email, "OIDC claims overwrote established User = %#v", unchangedUser)
	require.Equal(t, "OIDC", unchangedUser.FirstName, "OIDC claims overwrote established User = %#v", unchangedUser)
	require.Equal(t, "Student", unchangedUser.LastName, "OIDC claims overwrote established User = %#v", unchangedUser)

	subject = "ineligible-oidc-subject"
	claimedUsername, claimedEmail = "ineligible.oidc", "ineligible.oidc@example.edu"
	claimedHomeOrg = "outside.example"
	require.Equal(t, http.StatusUnauthorized, loginAgain().Code, "ineligible OIDC auto_provision status")
	_, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject)
	require.True(t, store.IsNotFound(err), "ineligible OIDC subject persisted an identity")

	subject = "conflicting-oidc-subject"
	claimedUsername, claimedEmail, claimedHomeOrg = "conflicting.oidc", "oidc.student@example.edu", "example.edu"
	conflictResponse := loginAgain()
	require.Equal(t, http.StatusConflict, conflictResponse.Code, "conflicting OIDC auto_provision status")
	body := conflictResponse.Body.String()
	require.NotContains(t, body, subject)
	require.NotContains(t, body, claimedEmail)
	require.NotContains(t, body, user.ID.String())
	_, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject)
	require.True(t, store.IsNotFound(err), "conflicting OIDC subject persisted an identity")

	for _, secret := range []string{
		code,
		linkedSubject,
		"ineligible-oidc-subject",
		"conflicting-oidc-subject",
		bindingCookie.Value,
		"provider-access-token",
	} {
		require.NotContains(t, helper.Logs.String(), secret, "OIDC secret appeared in logs")
	}
}
