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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/httpapi"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/sqlstore"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestCASExternalAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	const (
		providerID           = "campus-cas"
		connectionProviderID = "research-cas"
		invitationProviderID = "invited-cas"
		ticket               = "ST-sensitive-ticket"
		linkedSubject        = "opaque-sensitive-subject"
		conflictingSubject   = "opaque-conflicting-subject"
	)
	subject, claimedUsername, claimedEmail := linkedSubject, "external.student", "external.student@example.edu"
	claimedFirstName, claimedLastName, claimedHomeOrganization := "External", "Student", "example.edu"
	var validatedService string
	casServer := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		assert.Equal(t, "/cas/p3/serviceValidate", request.URL.Path)
		assert.Equal(t, ticket, request.URL.Query().Get("ticket"))
		validatedService = request.URL.Query().Get("service")
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<?xml version="1.0"?>
<cas:serviceResponse xmlns:cas="urn:cas">
  <cas:authenticationSuccess>
    <cas:user>` + subject + `</cas:user>
    <cas:attributes>
      <cas:uid>` + claimedUsername + `</cas:uid>
      <cas:mail>` + claimedEmail + `</cas:mail>
      <cas:givenName>` + claimedFirstName + `</cas:givenName>
      <cas:sn>` + claimedLastName + `</cas:sn>
      <cas:schacHomeOrganization>` + claimedHomeOrganization + `</cas:schacHomeOrganization>
      <cas:authnContext>mfa</cas:authnContext>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`))
	}))
	defer casServer.Close()

	persistence := openAuthenticationStore(t, dataSource)
	seedAuthenticationAccessPolicy(t, persistence, map[string]model.ProviderAdmissionMode{
		providerID:           model.ProviderAdmissionAutoProvision,
		connectionProviderID: model.ProviderAdmissionLinkedOnly,
		invitationProviderID: model.ProviderAdmissionInvitationRequired,
	})
	casProvider := config.ExternalAuthenticationProvider{
		ID: providerID, Type: "cas", DisplayName: "Campus CAS",
		Enabled: true, AutoProvision: true,
		CAS: &config.CASProvider{
			BaseURL:          casServer.URL + "/cas",
			ValidationPath:   "/p3/serviceValidate",
			Timeout:          config.Duration{Duration: 5 * time.Second},
			MaxResponseBytes: 64 * 1024,
		},
		Claims: config.ExternalClaimMapping{
			Subject: "user", Username: "uid", Email: "mail",
			FirstName: "givenName", LastName: "sn",
			HomeOrganization:         "schacHomeOrganization",
			AllowedHomeOrganizations: []string{"example.edu"},
			TrustEmail:               true,
			MultiFactorAttribute:     "authnContext",
			MultiFactorValues:        []string{"mfa"},
		},
	}
	connectionProvider := casProvider
	connectionProvider.ID = connectionProviderID
	connectionProvider.DisplayName = "Research CAS"
	connectionProvider.AutoProvision = false
	invitationProvider := casProvider
	invitationProvider.ID = invitationProviderID
	invitationProvider.DisplayName = "Invited CAS"
	invitationProvider.AutoProvision = false
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.test"
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
			cfg.Authentication.External.Providers = []config.ExternalAuthenticationProvider{casProvider, connectionProvider, invitationProvider}
		}),
		testlib.WithStore(persistence),
	)
	institution, err := persistence.Institution().Save(
		context.Background(),
		&model.Institution{
			Name:        "external-auth-university",
			DisplayName: "External Authentication University",
		},
	)
	require.NoError(t, err)

	providers := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers",
		nil,
		"",
	)
	var providerList []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	}
	require.Equal(t, http.StatusOK, providers.Code, "provider discovery status")
	require.NoError(t, json.Unmarshal(integrationListItems(t, providers.Body.Bytes()), &providerList), "provider discovery status")
	require.Len(t, providerList, 3, "provider discovery status")
	require.NotContains(t, providers.Body.String(), casServer.URL, "provider discovery status")

	begin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?device_id=browser-1&return_to=%2Fafter-login",
		nil,
		"",
	)
	require.Equal(t, http.StatusSeeOther, begin.Code, "begin status = %d: %s", begin.Code, begin.Body.String())
	loginURL, err := url.Parse(begin.Header().Get("Location"))
	require.NoError(t, err)
	serviceURL := loginURL.Query().Get("service")
	callbackURL, err := url.Parse(serviceURL)
	require.NoError(t, err, "CAS service URL = %q", serviceURL)
	require.NotEmpty(t, callbackURL.Query().Get("state"), "CAS service URL = %q", serviceURL)
	var bindingCookie *http.Cookie
	for _, cookie := range begin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			bindingCookie = cookie
			break
		}
	}
	require.NotNil(t, bindingCookie)
	require.True(t, bindingCookie.HttpOnly)
	require.Equal(t, model.APIURLSuffix+"/auth/providers/", bindingCookie.Path)

	callbackQuery := callbackURL.Query()
	callbackQuery.Set("ticket", ticket)
	callbackPath := callbackURL.Path + "?" + callbackQuery.Encode()
	callbackRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	callbackRequest.RemoteAddr = "127.0.0.1:4321"
	callbackRequest.AddCookie(bindingCookie)
	callback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(callback, callbackRequest)
	require.Equal(t, http.StatusSeeOther, callback.Code, "callback status=%d location=%q service=%q want=%q body=%s", callback.Code, callback.Header().Get("Location"), validatedService, serviceURL, callback.Body.String())
	require.Equal(t, "/after-login", callback.Header().Get("Location"), "callback status=%d location=%q service=%q want=%q body=%s", callback.Code, callback.Header().Get("Location"), validatedService, serviceURL, callback.Body.String())
	require.Equal(t, serviceURL, validatedService, "callback status=%d location=%q service=%q want=%q body=%s", callback.Code, callback.Header().Get("Location"), validatedService, serviceURL, callback.Body.String())

	var accessCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == httpapi.BrowserAccessCookieName {
			accessCookie = cookie
		}
	}
	require.NotNil(t, accessCookie)
	require.True(t, accessCookie.HttpOnly)
	identity, err := persistence.ExternalIdentity().GetByProviderSubject(
		context.Background(),
		providerID,
		subject,
	)
	require.NoError(t, err)
	user, err := persistence.User().Get(context.Background(), identity.UserID)
	require.NoError(t, err, "provisioned user = %#v", user)
	require.Equal(t, "external.student", user.Username, "provisioned user = %#v", user)
	require.Equal(t, "external.student@example.edu", user.Email, "provisioned user = %#v", user)
	require.True(t, user.EmailVerified, "provisioned user = %#v", user)
	settings, err := persistence.UserSettings().Get(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, model.UserSettingsInitialSource, settings.Source)
	require.Equal(t, model.UserSettingsFormatVersion1, settings.FormatVersion)
	require.Equal(t, user.ID, settings.UserID)
	require.True(t, settings.Revision.IsValid())
	jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{
		Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, Limit: 200,
	})
	require.NoError(t, err)
	foundDefaultIntent := false
	for _, job := range jobs {
		foundDefaultIntent = foundDefaultIntent || job.DedupeKey == user.ID.String()
	}
	require.True(t, foundDefaultIntent, "external provision did not commit default-picture intent for %s", user.ID)
	sessions, err := persistence.Session().ListActiveByUser(
		context.Background(),
		user.ID,
		model.NowUTC(),
	)
	require.NoError(t, err, "external sessions")
	require.Len(t, sessions, 1, "external sessions")
	require.Equal(t, "cas", sessions[0].AuthenticationMethod, "external sessions")
	require.Equal(t, providerID, sessions[0].AuthenticationProviderID, "external sessions")
	require.Equal(t, identity.ID, sessions[0].ExternalIdentityID, "external sessions")
	require.Equal(t, model.AuthenticationMultiFactor, sessions[0].AuthenticationStrength, "external sessions")
	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{
			ActorID:    user.ID,
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		},
	)
	require.NoError(t, err, "external authentication audits")
	require.Len(t, audits, 2, "external authentication audits")
	for _, event := range audits {
		require.Equal(t, institution.ID.String(), event.ScopeID)
		require.False(t, event.Action != "authentication.external_provision" &&
			event.Action != "authentication.external_login")
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	meRequest.AddCookie(accessCookie)
	me := httptest.NewRecorder()
	helper.Handler().ServeHTTP(me, meRequest)
	require.Equal(t, http.StatusOK, me.Code, "cookie-authenticated current user = %d: %s", me.Code, me.Body.String())
	loginProvider := func(loginProviderID, deviceID string) *httptest.ResponseRecorder {
		begin := performJSONRequest(
			helper.Handler(), http.MethodGet,
			"/api/v1/auth/providers/"+loginProviderID+"/login?client_type=web&device_id="+deviceID,
			nil, "",
		)
		require.Equal(t, http.StatusSeeOther, begin.Code, "repeat begin for %s status = %d: %s", loginProviderID, begin.Code, begin.Body.String())
		location, parseErr := url.Parse(begin.Header().Get("Location"))
		require.NoError(t, parseErr)
		service, parseErr := url.Parse(location.Query().Get("service"))
		require.NoError(t, parseErr)
		query := service.Query()
		query.Set("ticket", ticket)
		request := httptest.NewRequest(http.MethodGet, service.Path+"?"+query.Encode(), nil)
		for _, cookie := range begin.Result().Cookies() {
			if cookie.Name == httpapi.BrowserExternalLoginCookieName {
				request.AddCookie(cookie)
			}
		}
		response := httptest.NewRecorder()
		helper.Handler().ServeHTTP(response, request)
		return response
	}

	unlinkedResponse := loginProvider(connectionProviderID, "unlinked-linked-only")
	require.Equal(t, http.StatusForbidden, unlinkedResponse.Code, "unlinked linked_only status")
	body := unlinkedResponse.Body.String()
	require.NotContains(t, body, claimedEmail)
	require.NotContains(t, body, user.ID.String())
	invitationResponse := loginProvider(invitationProviderID, "unclaimed-invitation")
	require.Equal(t, http.StatusForbidden, invitationResponse.Code, "unclaimed invitation_required status")
	body = invitationResponse.Body.String()
	require.NotContains(t, body, claimedEmail)
	require.NotContains(t, body, user.ID.String())

	invitedEmail := "claimed.external.student@example.edu"
	claim, pendingInvitation := seedExternalAdmissionInvitation(t, persistence, institution, user, invitedEmail)
	subject, claimedUsername, claimedEmail = "invited-opaque-subject", "claimed.external.student", invitedEmail
	disabledBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	require.Equal(t, http.StatusSeeOther, disabledBegin.Code, "pre-toggle invitation begin = %d body=%s", disabledBegin.Code, disabledBegin.Body.String())
	disabledLoginURL, err := url.Parse(disabledBegin.Header().Get("Location"))
	require.NoError(t, err)
	disabledServiceURL, err := url.Parse(disabledLoginURL.Query().Get("service"))
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET invitation_admission_enabled=FALSE, invitation_local_credential_enabled=FALSE,
		       revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1`)
	require.NoError(t, err)
	disabledQuery := disabledServiceURL.Query()
	disabledQuery.Set("ticket", ticket)
	disabledCallbackRequest := httptest.NewRequest(http.MethodGet,
		disabledServiceURL.Path+"?"+disabledQuery.Encode(), nil)
	for _, cookie := range disabledBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			disabledCallbackRequest.AddCookie(cookie)
		}
	}
	disabledCallback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(disabledCallback, disabledCallbackRequest)
	require.Equal(t, http.StatusUnauthorized, disabledCallback.Code, "globally disabled invitation callback = %d body=%s", disabledCallback.Code, disabledCallback.Body.String())
	body = disabledCallback.Body.String()
	require.NotContains(t, body, invitedEmail)
	require.NotContains(t, body, subject)
	require.NotContains(t, body, pendingInvitation.ID.String())
	_, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject)
	require.True(t, store.IsNotFound(err), "globally disabled invitation persisted identity")
	_, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET invitation_admission_enabled=TRUE, invitation_local_credential_enabled=TRUE,
		       revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1`)
	require.NoError(t, err)
	invitationBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	require.Equal(t, http.StatusSeeOther, invitationBegin.Code, "claimed invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	require.NotContains(t, invitationBegin.Header().Get("Location"), claim, "claimed invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	invitationLoginURL, err := url.Parse(invitationBegin.Header().Get("Location"))
	require.NoError(t, err)
	invitationServiceURL, err := url.Parse(invitationLoginURL.Query().Get("service"))
	require.NoError(t, err)
	invitationQuery := invitationServiceURL.Query()
	invitationQuery.Set("ticket", ticket)
	invitationCallbackRequest := httptest.NewRequest(http.MethodGet,
		invitationServiceURL.Path+"?"+invitationQuery.Encode(), nil)
	for _, cookie := range invitationBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			invitationCallbackRequest.AddCookie(cookie)
		}
	}
	invitationCallback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationCallback, invitationCallbackRequest)
	require.Equal(t, http.StatusSeeOther, invitationCallback.Code, "claimed invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	require.Equal(t, "/join", invitationCallback.Header().Get("Location"), "claimed invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	invitationIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject)
	require.NoError(t, err)
	invitationUser, err := persistence.User().Get(context.Background(), invitationIdentity.UserID)
	require.NoError(t, err, "invitation-admitted User = %#v", invitationUser)
	require.Equal(t, invitedEmail, invitationUser.Email, "invitation-admitted User = %#v", invitationUser)
	require.True(t, invitationUser.EmailVerified, "invitation-admitted User = %#v", invitationUser)
	acceptedInvitation, err := persistence.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	require.NoError(t, err, "admission terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, pendingInvitation.ID, acceptedInvitation.ID, "admission terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, model.InvitationAccepted, acceptedInvitation.State, "admission terminal Invitation = %#v", acceptedInvitation)
	require.Equal(t, invitationUser.ID, acceptedInvitation.AcceptedUserID, "admission terminal Invitation = %#v", acceptedInvitation)
	invitationAffiliations, _ := persistence.Affiliation().ListByUser(context.Background(), invitationUser.ID)
	invitationClassMembers, _ := persistence.ClassMember().ListByUser(context.Background(), invitationUser.ID)
	invitationBindings, _ := persistence.RoleBinding().ListByUser(context.Background(), invitationUser.ID)
	require.Len(t, invitationAffiliations, 1, "invitation admission package: affiliations")
	require.Equal(t, acceptedInvitation.AcceptedAffiliationID, invitationAffiliations[0].ID, "invitation admission package: affiliations")
	require.Len(t, invitationClassMembers, 1, "invitation admission package: affiliations")
	require.Equal(t, acceptedInvitation.AcceptedClassMemberID, invitationClassMembers[0].ID, "invitation admission package: affiliations")
	require.Empty(t, invitationBindings, "invitation admission package: affiliations")
	invitationSessions, err := persistence.Session().ListActiveByUser(context.Background(), invitationUser.ID, model.NowUTC())
	require.NoError(t, err, "Invitation proof created an ordinary Session")
	require.Empty(t, invitationSessions, "Invitation proof created an ordinary Session")
	invitationAudits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{ActorID: invitationUser.ID,
		Action: "invitation.accept", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	require.NoError(t, err, "CAS Invitation acceptance audits")
	require.Len(t, invitationAudits, 1, "CAS Invitation acceptance audits")
	require.Equal(t, config.ExternalAuthenticationTypeCAS, invitationAudits[0].AuthMethod, "CAS Invitation acceptance audits")
	invitationReplay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationReplay, invitationCallbackRequest.Clone(context.Background()))
	require.Equal(t, http.StatusUnauthorized, invitationReplay.Code, "invitation callback replay = %d body=%s", invitationReplay.Code, invitationReplay.Body.String())
	require.NotContains(t, helper.Logs.String(), claim, "raw Invitation claim appeared in CAS logs")
	subject, claimedUsername, claimedEmail = linkedSubject, "external.student", "external.student@example.edu"

	connectRequest := httptest.NewRequest(http.MethodPost,
		"/api/v1/authentication-methods/providers/"+connectionProviderID+"/connect",
		strings.NewReader(`{"return_to":"/connected"}`))
	connectRequest.Header.Set("Content-Type", "application/json")
	connectRequest.Header.Set("Authorization", "Bearer "+accessCookie.Value)
	connect := httptest.NewRecorder()
	helper.Handler().ServeHTTP(connect, connectRequest)
	require.Equal(t, http.StatusCreated, connect.Code, "provider connection begin = %d: %s", connect.Code, connect.Body.String())
	var connectionStart struct {
		RedirectURL string    `json:"redirect_url"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	err = json.Unmarshal(connect.Body.Bytes(), &connectionStart)
	require.NoError(t, err)
	connectionURL, err := url.Parse(connectionStart.RedirectURL)
	require.NoError(t, err)
	connectionService, err := url.Parse(connectionURL.Query().Get("service"))
	require.NoError(t, err)
	connectionStateToken := connectionService.Query().Get("state")
	connectionState, err := persistence.ExternalLoginState().GetByStateHash(
		context.Background(), model.HashToken(connectionStateToken),
	)
	require.NoError(t, err, "provider connection state = %#v response expiry=%s error", connectionState, connectionStart.ExpiresAt)
	require.Equal(t, 10*time.Minute, connectionState.ExpiresAt.Sub(connectionState.CreatedAt), "provider connection state = %#v response expiry=%s error", connectionState, connectionStart.ExpiresAt)
	require.True(t, connectionStart.ExpiresAt.Equal(time.UnixMilli(connectionState.ExpiresAt.UnixMilli())), "provider connection state = %#v response expiry=%s error", connectionState, connectionStart.ExpiresAt)
	connectionQuery := connectionService.Query()
	connectionQuery.Set("ticket", ticket)
	connectionCallbackRequest := httptest.NewRequest(http.MethodGet,
		connectionService.Path+"?"+connectionQuery.Encode(), nil)
	for _, cookie := range connect.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			connectionCallbackRequest.AddCookie(cookie)
		}
	}
	connectionCallback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(connectionCallback, connectionCallbackRequest)
	require.Equal(t, http.StatusSeeOther, connectionCallback.Code, "provider connection callback = %d location=%q body=%s", connectionCallback.Code, connectionCallback.Header().Get("Location"), connectionCallback.Body.String())
	require.Equal(t, "/connected", connectionCallback.Header().Get("Location"), "provider connection callback = %d location=%q body=%s", connectionCallback.Code, connectionCallback.Header().Get("Location"), connectionCallback.Body.String())
	connectedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), connectionProviderID, linkedSubject)
	require.NoError(t, err, "connected identity = %#v", connectedIdentity)
	require.Equal(t, user.ID, connectedIdentity.UserID, "connected identity = %#v", connectedIdentity)
	connectedSessions, err := persistence.Session().ListActiveByUser(context.Background(), user.ID, model.NowUTC())
	require.NoError(t, err, "provider connection created a Session")
	require.Len(t, connectedSessions, 1, "provider connection created a Session")
	require.Equal(t, http.StatusSeeOther, loginProvider(connectionProviderID, "linked-linked-only").Code, "existing linked_only identity login status")
	unlinkRequest := httptest.NewRequest(http.MethodDelete,
		"/api/v1/authentication-methods/providers/"+connectedIdentity.ID.String(), nil)
	unlinkRequest.Header.Set("Authorization", "Bearer "+accessCookie.Value)
	unlink := httptest.NewRecorder()
	helper.Handler().ServeHTTP(unlink, unlinkRequest)
	require.Equal(t, http.StatusNoContent, unlink.Code, "provider unlink = %d: %s", unlink.Code, unlink.Body.String())
	retainedSession, err := persistence.Session().Get(context.Background(), sessions[0].ID)
	require.NoError(t, err, "unrelated provider Session was revoked: %#v", retainedSession)
	require.False(t, retainedSession.RevokedAt.Valid, "unrelated provider Session was revoked: %#v", retainedSession)

	replayRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	replayRequest.AddCookie(bindingCookie)
	replay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(replay, replayRequest)
	require.Equal(t, http.StatusUnauthorized, replay.Code, "callback replay status = %d: %s", replay.Code, replay.Body.String())

	claimedUsername, claimedEmail = "changed.claim", "changed.claim@example.edu"
	claimedFirstName, claimedLastName = "Changed", "Profile"
	require.Equal(t, http.StatusSeeOther, loginProvider(providerID, "changed-profile").Code, "existing linked user login status")
	unchangedUser, err := persistence.User().Get(context.Background(), user.ID)
	require.NoError(t, err, "provider claim changes overwrote established User = %#v", unchangedUser)
	require.Equal(t, "external.student", unchangedUser.Username, "provider claim changes overwrote established User = %#v", unchangedUser)
	require.Equal(t, "external.student@example.edu", unchangedUser.Email, "provider claim changes overwrote established User = %#v", unchangedUser)
	require.Equal(t, "External", unchangedUser.FirstName, "provider claim changes overwrote established User = %#v", unchangedUser)
	require.Equal(t, "Student", unchangedUser.LastName, "provider claim changes overwrote established User = %#v", unchangedUser)

	subject = "ineligible-subject"
	claimedUsername, claimedEmail = "ineligible.user", "ineligible.user@example.edu"
	claimedHomeOrganization = "outside.example"
	require.Equal(t, http.StatusUnauthorized, loginProvider(providerID, "ineligible").Code, "ineligible auto_provision status")
	_, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject)
	require.True(t, store.IsNotFound(err), "ineligible subject persisted an identity")
	subject, claimedHomeOrganization = linkedSubject, "example.edu"
	claimedUsername, claimedEmail = "external.student", "external.student@example.edu"
	claimedFirstName, claimedLastName = "External", "Student"

	removalBegin := performJSONRequest(helper.Handler(), http.MethodGet,
		"/api/v1/auth/providers/"+providerID+"/login?client_type=web&device_id=provider-removal", nil, "")
	require.Equal(t, http.StatusSeeOther, removalBegin.Code, "provider-removal begin status = %d: %s", removalBegin.Code, removalBegin.Body.String())
	removalLoginURL, err := url.Parse(removalBegin.Header().Get("Location"))
	require.NoError(t, err)
	removalServiceURL, err := url.Parse(removalLoginURL.Query().Get("service"))
	require.NoError(t, err)
	removalQuery := removalServiceURL.Query()
	removalQuery.Set("ticket", ticket)
	removalCallbackPath := removalServiceURL.Path + "?" + removalQuery.Encode()
	var removalBinding *http.Cookie
	for _, cookie := range removalBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			removalBinding = cookie
		}
	}
	require.NotNil(t, removalBinding, "provider-removal binding cookie is missing")
	removedProviderNode := testlib.Setup(t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "https://proctor.example.test"
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
			cfg.Authentication.External.Providers = []config.ExternalAuthenticationProvider{connectionProvider, invitationProvider}
		}),
		testlib.WithStore(persistence),
	)
	removedRequest := httptest.NewRequest(http.MethodGet, removalCallbackPath, nil)
	removedRequest.AddCookie(removalBinding)
	removedResponse := httptest.NewRecorder()
	removedProviderNode.Handler().ServeHTTP(removedResponse, removedRequest)
	require.Equal(t, http.StatusNotFound, removedResponse.Code, "removed-provider callback status = %d: %s", removedResponse.Code, removedResponse.Body.String())
	preservedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, linkedSubject)
	require.NoError(t, err, "provider removal changed durable identity = %#v", preservedIdentity)
	require.Equal(t, identity.ID, preservedIdentity.ID, "provider removal changed durable identity = %#v", preservedIdentity)
	require.Equal(t, user.ID, preservedIdentity.UserID, "provider removal changed durable identity = %#v", preservedIdentity)
	restoredRequest := httptest.NewRequest(http.MethodGet, removalCallbackPath, nil)
	restoredRequest.AddCookie(removalBinding)
	restoredResponse := httptest.NewRecorder()
	helper.Handler().ServeHTTP(restoredResponse, restoredRequest)
	require.Equal(t, http.StatusSeeOther, restoredResponse.Code, "restored-provider callback status = %d: %s", restoredResponse.Code, restoredResponse.Body.String())
	currentUser, err := persistence.User().Get(context.Background(), user.ID)
	require.NoError(t, err)
	disabledAt := model.GetMillis()
	auditAttempt, err := persistence.Audit().Save(context.Background(), &model.AuditEvent{
		Action: string(model.ActionUserManage),
		Resource: model.Resource{
			Type: model.ResourceUser,
			ID:   user.ID.String(),
		},
		ScopeType: model.RoleScopeInstitution,
		ScopeID:   institution.ID.String(),
		Status:    model.AuditStatusAttempt,
		NodeID:    "external-authentication-integration",
	})
	require.NoError(t, err)
	_, err = persistence.User().SetDisabled(
		context.Background(),
		storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID:               user.ID,
			ExpectedRevision: currentUser.Revision,
			Disabled:         true,
			ChangedAt:        disabledAt,
			RevocationReason: model.SessionRevocationAccountDisabled,
			AuditEventID:     auditAttempt.ID,
			AuditAt:          disabledAt,
		}),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, loginProvider(providerID, "disabled-user").Code, "disabled linked user login status")
	subject = conflictingSubject
	conflictResponse := loginProvider(providerID, "conflicting-email")
	require.Equal(t, http.StatusConflict, conflictResponse.Code, "conflicting provision status")
	body = conflictResponse.Body.String()
	require.NotContains(t, body, conflictingSubject)
	require.NotContains(t, body, claimedEmail)
	require.NotContains(t, body, user.ID.String())
	for _, logs := range []string{helper.Logs.String(), removedProviderNode.Logs.String()} {
		for _, secret := range []string{ticket, linkedSubject, conflictingSubject, "ineligible-subject", bindingCookie.Value, removalBinding.Value} {
			require.NotContains(t, logs, secret, "external authentication secret appeared in logs")
		}
	}
}

func seedExternalAdmissionInvitation(
	t *testing.T,
	persistence *sqlstore.SQLStore,
	institution *model.Institution,
	inviter *model.User,
	targetEmail string,
) (string, *model.Invitation) {
	t.Helper()
	ctx := context.Background()
	at := model.NowUTC()
	unit := &model.AcademicUnit{InstitutionID: institution.ID, Name: "external-admission", DisplayName: "External Admission"}
	var err error
	unit, err = storetest.SaveAcademicUnit(ctx, persistence, unit)
	require.NoError(t, err)
	programme := &model.Programme{AcademicUnitID: unit.ID, Name: "external-admission", DisplayName: "External Admission"}
	programme, err = storetest.SaveProgramme(ctx, persistence, programme)
	require.NoError(t, err)
	level := &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "external-admission", DisplayName: "External Admission"}
	level, err = storetest.SaveProgrammeLevel(ctx, persistence, level)
	require.NoError(t, err)
	period := &model.AcademicPeriod{Owner: model.NewAcademicUnitAcademicPeriodOwner(unit.ID),
		Name: "external-admission", DisplayName: "External Admission", StartsAt: at.Add(-time.Hour), EndsAt: at.Add(24 * time.Hour)}
	period, err = storetest.SaveAcademicPeriod(ctx, persistence, period)
	require.NoError(t, err)
	class := &model.Class{ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "external-admission", DisplayName: "External Admission"}
	class, err = storetest.SaveClass(ctx, persistence, class)
	require.NoError(t, err)
	role, err := storetest.SaveRole(ctx, persistence, &model.Role{Name: "external-inviter-" + model.NewID(),
		DisplayName: "External Invitation Manager",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	require.NoError(t, err)
	_, err = storetest.SaveRoleBinding(ctx, persistence, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: at.Add(-time.Minute)})
	require.NoError(t, err)
	claim := model.NewCredentialToken()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: targetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: at, InviterUserID: inviter.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ClaimHash: model.HashInvitationClaim(claim), IssuedAt: at,
	})
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx, `
		INSERT INTO invitations (
			id, created_at, updated_at, revision, purpose, state, target_email, class_id, academic_period_id,
			academic_unit_id, role_id, role_actions, intended_start_at, intended_end_at,
			suggested_username, suggested_display_name, suggested_first_name, suggested_last_name, suggested_locale,
			inviter_user_id, scope_type, scope_id, claim_hash, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, '{}', ?, NULL, '', '', '', '', '', ?, ?, ?, ?, ?)`,
		invitation.ID.String(), invitation.CreatedAt, invitation.UpdatedAt, invitation.Revision,
		invitation.Purpose, invitation.State, invitation.TargetEmail, invitation.ClassID.String(), invitation.AcademicPeriodID.String(),
		invitation.IntendedStartsAt, invitation.InviterUserID.String(), invitation.ScopeType, invitation.ScopeID,
		invitation.ClaimHash, invitation.ExpiresAt)
	require.NoError(t, err)
	return claim, invitation
}
