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
	if err != nil {
		t.Fatal(err)
	}

	begin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=web&return_to=%2Foidc-complete",
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
	helper.Handler().ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusSeeOther ||
		callback.Header().Get("Location") != "/oidc-complete" {
		t.Fatalf(
			"callback status=%d location=%q body=%s",
			callback.Code,
			callback.Header().Get("Location"),
			callback.Body.String(),
		)
	}
	replayRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	replayRequest.AddCookie(bindingCookie)
	replay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed callback status=%d body=%s", replay.Code, replay.Body.String())
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
		identity.UserID.String(),
	)
	if err != nil || user.Username != "oidc.student" ||
		user.Email != "oidc.student@example.edu" ||
		!user.EmailVerified {
		t.Fatalf("provisioned OIDC user = %#v, %v", user, err)
	}
	affiliations, err := persistence.Affiliation().ListByUser(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	academicMemberships, err := persistence.AcademicUnitMember().ListByUser(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	classMemberships, err := persistence.ClassMember().ListByUser(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := persistence.RoleBinding().ListByUser(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(affiliations) != 0 || len(academicMemberships) != 0 || len(classMemberships) != 0 || len(bindings) != 0 {
		t.Fatalf("OIDC claims created academic authority: affiliations=%#v academic_memberships=%#v class_memberships=%#v role_bindings=%#v",
			affiliations, academicMemberships, classMemberships, bindings)
	}
	sessions, err := persistence.Session().ListActiveByUser(
		context.Background(),
		user.ID.String(),
		model.GetMillis(),
	)
	if err != nil || len(sessions) != 1 ||
		sessions[0].AuthenticationMethod !=
			config.ExternalAuthenticationTypeOIDC ||
		sessions[0].AuthenticationProviderID != providerID ||
		sessions[0].ExternalIdentityID != identity.ID ||
		sessions[0].AuthenticationStrength !=
			model.AuthenticationMultiFactor {
		t.Fatalf("OIDC session = %#v, %v", sessions, err)
	}

	invitedEmail := "invited.oidc@example.edu"
	claim, pendingInvitation := seedExternalAdmissionInvitation(t, persistence, institution, user, invitedEmail)
	subject, claimedUsername, claimedEmail = "invited-oidc-subject", "invited.oidc", " Provider.OIDC@Example.EDU "
	invitationBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	if invitationBegin.Code != http.StatusSeeOther || strings.Contains(invitationBegin.Header().Get("Location"), claim) {
		t.Fatalf("OIDC invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	}
	invitationAuthorizationURL, err := url.Parse(invitationBegin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	transactionMutex.Lock()
	expectedNonce = invitationAuthorizationURL.Query().Get("nonce")
	expectedChallenge = invitationAuthorizationURL.Query().Get("code_challenge")
	transactionMutex.Unlock()
	var invitationBinding *http.Cookie
	for _, cookie := range invitationBegin.Result().Cookies() {
		if cookie.Name == api.BrowserExternalLoginCookieName {
			invitationBinding = cookie
		}
	}
	if invitationBinding == nil {
		t.Fatal("OIDC invitation binding cookie is missing")
	}
	invitationCallbackPath := model.APIURLSuffix + "/auth/providers/" + invitationProviderID +
		"/callback?state=" + url.QueryEscape(invitationAuthorizationURL.Query().Get("state")) + "&code=" + url.QueryEscape(code)
	invitationCallbackRequest := httptest.NewRequest(http.MethodGet, invitationCallbackPath, nil)
	invitationCallbackRequest.AddCookie(invitationBinding)
	invitationCallback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationCallback, invitationCallbackRequest)
	if invitationCallback.Code != http.StatusSeeOther || invitationCallback.Header().Get("Location") != "/join" {
		t.Fatalf("OIDC invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	}
	invitedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject)
	if err != nil {
		t.Fatal(err)
	}
	invitedUser, err := persistence.User().Get(context.Background(), invitedIdentity.UserID.String())
	if err != nil || invitedUser.Email != invitedEmail || !invitedUser.EmailVerified {
		t.Fatalf("OIDC invitation User = %#v, %v", invitedUser, err)
	}
	acceptedInvitation, err := persistence.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	if err != nil || acceptedInvitation.ID != pendingInvitation.ID || acceptedInvitation.State != model.InvitationAccepted ||
		acceptedInvitation.AcceptedUserID != invitedUser.ID {
		t.Fatalf("OIDC terminal Invitation = %#v, %v", acceptedInvitation, err)
	}
	invitedAffiliations, _ := persistence.Affiliation().ListByUser(context.Background(), invitedUser.ID.String())
	invitedMemberships, _ := persistence.ClassMember().ListByUser(context.Background(), invitedUser.ID.String())
	invitedBindings, _ := persistence.RoleBinding().ListByUser(context.Background(), invitedUser.ID.String())
	if len(invitedAffiliations) != 1 || invitedAffiliations[0].ID != acceptedInvitation.AcceptedAffiliationID ||
		len(invitedMemberships) != 1 || invitedMemberships[0].ID != acceptedInvitation.AcceptedClassMemberID ||
		len(invitedBindings) != 0 {
		t.Fatalf("OIDC invitation admission package: affiliations=%#v memberships=%#v bindings=%#v",
			invitedAffiliations, invitedMemberships, invitedBindings)
	}
	invitedSessions, err := persistence.Session().ListActiveByUser(context.Background(), invitedUser.ID.String(), model.GetMillis())
	if err != nil || len(invitedSessions) != 0 {
		t.Fatalf("OIDC Invitation proof created an ordinary Session = %#v, %v", invitedSessions, err)
	}
	invitationAudits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{ActorId: invitedUser.ID.String(),
		Action: "invitation.accept", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil || len(invitationAudits) != 1 || invitationAudits[0].AuthMethod != config.ExternalAuthenticationTypeOIDC {
		t.Fatalf("OIDC Invitation acceptance audits = %#v, %v", invitationAudits, err)
	}
	invitationReplayRequest := httptest.NewRequest(http.MethodGet, invitationCallbackPath, nil)
	invitationReplayRequest.AddCookie(invitationBinding)
	invitationReplay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationReplay, invitationReplayRequest)
	if invitationReplay.Code != http.StatusUnauthorized {
		t.Fatalf("OIDC invitation callback replay = %d body=%s", invitationReplay.Code, invitationReplay.Body.String())
	}
	if strings.Contains(helper.Logs.String(), claim) {
		t.Fatal("raw Invitation claim appeared in OIDC logs")
	}
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
	helper.Handler().ServeHTTP(
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
			Action:     "authentication.external_login",
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
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

	loginAgain := func() *httptest.ResponseRecorder {
		begin := performJSONRequest(helper.Handler(), http.MethodGet,
			"/api/v1/auth/providers/"+providerID+"/login?client_type=web", nil, "")
		if begin.Code != http.StatusSeeOther {
			t.Fatalf("OIDC repeat begin status=%d body=%s", begin.Code, begin.Body.String())
		}
		authorizationURL, parseErr := url.Parse(begin.Header().Get("Location"))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		transactionMutex.Lock()
		expectedNonce = authorizationURL.Query().Get("nonce")
		expectedChallenge = authorizationURL.Query().Get("code_challenge")
		transactionMutex.Unlock()
		var repeatBinding *http.Cookie
		for _, cookie := range begin.Result().Cookies() {
			if cookie.Name == api.BrowserExternalLoginCookieName {
				repeatBinding = cookie
			}
		}
		if repeatBinding == nil {
			t.Fatal("OIDC repeat binding cookie is missing")
		}
		request := httptest.NewRequest(http.MethodGet, model.APIURLSuffix+"/auth/providers/"+providerID+
			"/callback?state="+url.QueryEscape(authorizationURL.Query().Get("state"))+"&code="+url.QueryEscape(code), nil)
		request.AddCookie(repeatBinding)
		response := httptest.NewRecorder()
		helper.Handler().ServeHTTP(response, request)
		return response
	}

	claimedUsername, claimedEmail = "changed.oidc", "changed.oidc@example.edu"
	claimedFirstName, claimedLastName = "Changed", "OIDC Profile"
	if response := loginAgain(); response.Code != http.StatusSeeOther {
		t.Fatalf("OIDC changed-claim login status = %d", response.Code)
	}
	unchangedUser, err := persistence.User().Get(context.Background(), user.ID.String())
	if err != nil || unchangedUser.Username != "oidc.student" || unchangedUser.Email != "oidc.student@example.edu" ||
		unchangedUser.FirstName != "OIDC" || unchangedUser.LastName != "Student" {
		t.Fatalf("OIDC claims overwrote established User = %#v, %v", unchangedUser, err)
	}

	subject = "ineligible-oidc-subject"
	claimedUsername, claimedEmail = "ineligible.oidc", "ineligible.oidc@example.edu"
	claimedHomeOrg = "outside.example"
	if response := loginAgain(); response.Code != http.StatusUnauthorized {
		t.Fatalf("ineligible OIDC auto_provision status = %d", response.Code)
	}
	if _, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject); !store.IsNotFound(err) {
		t.Fatalf("ineligible OIDC subject persisted an identity: %v", err)
	}

	subject = "conflicting-oidc-subject"
	claimedUsername, claimedEmail, claimedHomeOrg = "conflicting.oidc", "oidc.student@example.edu", "example.edu"
	conflictResponse := loginAgain()
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("conflicting OIDC auto_provision status = %d", conflictResponse.Code)
	}
	if body := conflictResponse.Body.String(); strings.Contains(body, subject) || strings.Contains(body, claimedEmail) || strings.Contains(body, user.ID.String()) {
		t.Fatalf("conflicting OIDC response disclosed account or provider claims: %s", body)
	}
	if _, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject); !store.IsNotFound(err) {
		t.Fatalf("conflicting OIDC subject persisted an identity: %v", err)
	}

	for _, secret := range []string{
		code,
		linkedSubject,
		"ineligible-oidc-subject",
		"conflicting-oidc-subject",
		bindingCookie.Value,
		"provider-access-token",
	} {
		if strings.Contains(helper.Logs.String(), secret) {
			t.Fatalf("OIDC secret appeared in logs")
		}
	}
}
