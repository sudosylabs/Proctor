//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
		if request.URL.Path != "/cas/p3/serviceValidate" ||
			request.URL.Query().Get("ticket") != ticket {
			t.Errorf("CAS validation request = %s", request.URL.String())
		}
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
	if err != nil {
		t.Fatal(err)
	}

	providers := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers",
		nil,
		"",
	)
	var providerList []model.ExternalAuthenticationProvider
	if providers.Code != http.StatusOK ||
		json.Unmarshal(providers.Body.Bytes(), &providerList) != nil ||
		len(providerList) != 3 ||
		strings.Contains(providers.Body.String(), casServer.URL) {
		t.Fatalf("provider discovery status=%d body=%s", providers.Code, providers.Body.String())
	}

	begin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?device_id=browser-1&return_to=%2Fafter-login",
		nil,
		"",
	)
	if begin.Code != http.StatusSeeOther {
		t.Fatalf("begin status = %d: %s", begin.Code, begin.Body.String())
	}
	loginURL, err := url.Parse(begin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	serviceURL := loginURL.Query().Get("service")
	callbackURL, err := url.Parse(serviceURL)
	if err != nil || callbackURL.Query().Get("state") == "" {
		t.Fatalf("CAS service URL = %q, %v", serviceURL, err)
	}
	var bindingCookie *http.Cookie
	for _, cookie := range begin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			bindingCookie = cookie
			break
		}
	}
	if bindingCookie == nil || !bindingCookie.HttpOnly ||
		bindingCookie.Path != model.APIURLSuffix+"/auth/providers/" {
		t.Fatalf("external login binding cookie = %#v", bindingCookie)
	}

	callbackQuery := callbackURL.Query()
	callbackQuery.Set("ticket", ticket)
	callbackPath := callbackURL.Path + "?" + callbackQuery.Encode()
	callbackRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	callbackRequest.RemoteAddr = "127.0.0.1:4321"
	callbackRequest.AddCookie(bindingCookie)
	callback := httptest.NewRecorder()
	helper.Handler().ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusSeeOther ||
		callback.Header().Get("Location") != "/after-login" ||
		validatedService != serviceURL {
		t.Fatalf(
			"callback status=%d location=%q service=%q want=%q body=%s",
			callback.Code,
			callback.Header().Get("Location"),
			validatedService,
			serviceURL,
			callback.Body.String(),
		)
	}

	var accessCookie *http.Cookie
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == httpapi.BrowserAccessCookieName {
			accessCookie = cookie
		}
	}
	if accessCookie == nil || !accessCookie.HttpOnly {
		t.Fatalf("access cookie = %#v", accessCookie)
	}
	identity, err := persistence.ExternalIdentity().GetByProviderSubject(
		context.Background(),
		providerID,
		subject,
	)
	if err != nil {
		t.Fatal(err)
	}
	user, err := persistence.User().Get(context.Background(), identity.UserID.String())
	if err != nil || user.Username != "external.student" ||
		user.Email != "external.student@example.edu" ||
		!user.EmailVerified {
		t.Fatalf("provisioned user = %#v, %v", user, err)
	}
	settings, err := persistence.UserSettings().Get(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Source != model.UserSettingsInitialSource ||
		settings.FormatVersion != model.UserSettingsFormatVersion1 ||
		settings.UserID != user.ID || !settings.Revision.IsValid() {
		t.Fatalf("externally provisioned user settings = %#v", settings)
	}
	jobs, err := persistence.Job().List(context.Background(), store.JobListOptions{
		Types: []model.JobType{model.JobTypeProfilePictureGenerateDefault}, Limit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundDefaultIntent := false
	for _, job := range jobs {
		foundDefaultIntent = foundDefaultIntent || job.DedupeKey == user.ID.String()
	}
	if !foundDefaultIntent {
		t.Fatalf("external provision did not commit default-picture intent for %s", user.ID)
	}
	sessions, err := persistence.Session().ListActiveByUser(
		context.Background(),
		user.ID.String(),
		model.GetMillis(),
	)
	if err != nil || len(sessions) != 1 ||
		sessions[0].AuthenticationMethod != "cas" ||
		sessions[0].AuthenticationProviderID != providerID ||
		sessions[0].ExternalIdentityID != identity.ID ||
		sessions[0].AuthenticationStrength != model.AuthenticationMultiFactor {
		t.Fatalf("external sessions = %#v, %v", sessions, err)
	}
	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{
			ActorId:    user.ID.String(),
			Limit:      10,
			Visibility: store.AuditVisibilityScope{InstitutionWide: true},
		},
	)
	if err != nil || len(audits) != 2 {
		t.Fatalf("external authentication audits = %#v, %v", audits, err)
	}
	for _, event := range audits {
		if event.ScopeID != institution.ID.String() ||
			(event.Action != "authentication.external_provision" &&
				event.Action != "authentication.external_login") {
			t.Fatalf("unexpected external authentication audit = %#v", event)
		}
	}

	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	meRequest.AddCookie(accessCookie)
	me := httptest.NewRecorder()
	helper.Handler().ServeHTTP(me, meRequest)
	if me.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated current user = %d: %s", me.Code, me.Body.String())
	}
	loginProvider := func(loginProviderID, deviceID string) *httptest.ResponseRecorder {
		begin := performJSONRequest(
			helper.Handler(), http.MethodGet,
			"/api/v1/auth/providers/"+loginProviderID+"/login?client_type=web&device_id="+deviceID,
			nil, "",
		)
		if begin.Code != http.StatusSeeOther {
			t.Fatalf("repeat begin for %s status = %d: %s", loginProviderID, begin.Code, begin.Body.String())
		}
		location, parseErr := url.Parse(begin.Header().Get("Location"))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		service, parseErr := url.Parse(location.Query().Get("service"))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
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
	if unlinkedResponse.Code != http.StatusForbidden {
		t.Fatalf("unlinked linked_only status = %d", unlinkedResponse.Code)
	}
	if body := unlinkedResponse.Body.String(); strings.Contains(body, claimedEmail) || strings.Contains(body, user.ID.String()) {
		t.Fatalf("linked_only response disclosed an existing account: %s", body)
	}
	invitationResponse := loginProvider(invitationProviderID, "unclaimed-invitation")
	if invitationResponse.Code != http.StatusForbidden {
		t.Fatalf("unclaimed invitation_required status = %d", invitationResponse.Code)
	}
	if body := invitationResponse.Body.String(); strings.Contains(body, claimedEmail) || strings.Contains(body, user.ID.String()) {
		t.Fatalf("invitation_required response disclosed an existing account: %s", body)
	}

	invitedEmail := "claimed.external.student@example.edu"
	claim, pendingInvitation := seedExternalAdmissionInvitation(t, persistence, institution, user, invitedEmail)
	subject, claimedUsername, claimedEmail = "invited-opaque-subject", "claimed.external.student", invitedEmail
	disabledBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	if disabledBegin.Code != http.StatusSeeOther {
		t.Fatalf("pre-toggle invitation begin = %d body=%s", disabledBegin.Code, disabledBegin.Body.String())
	}
	disabledLoginURL, err := url.Parse(disabledBegin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	disabledServiceURL, err := url.Parse(disabledLoginURL.Query().Get("service"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET invitation_admission_enabled=FALSE, invitation_local_credential_enabled=FALSE,
		       revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
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
	if disabledCallback.Code != http.StatusUnauthorized {
		t.Fatalf("globally disabled invitation callback = %d body=%s", disabledCallback.Code, disabledCallback.Body.String())
	}
	if body := disabledCallback.Body.String(); strings.Contains(body, invitedEmail) ||
		strings.Contains(body, subject) || strings.Contains(body, pendingInvitation.ID.String()) {
		t.Fatalf("globally disabled invitation callback disclosed private state: %s", body)
	}
	if _, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject); !store.IsNotFound(err) {
		t.Fatalf("globally disabled invitation persisted identity: %v", err)
	}
	if _, err = persistence.GetMaster().Exec(context.Background(), `
		UPDATE access_policies
		   SET invitation_admission_enabled=TRUE, invitation_local_credential_enabled=TRUE,
		       revision=revision+1, updated_at=clock_timestamp()
		 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	invitationBegin := performJSONRequest(helper.Handler(), http.MethodPost,
		"/api/v1/auth/providers/"+invitationProviderID+"/login",
		map[string]any{"invitation_claim": claim, "return_to": "/join"}, "")
	if invitationBegin.Code != http.StatusSeeOther || strings.Contains(invitationBegin.Header().Get("Location"), claim) {
		t.Fatalf("claimed invitation begin = %d location=%q body=%s", invitationBegin.Code, invitationBegin.Header().Get("Location"), invitationBegin.Body.String())
	}
	invitationLoginURL, err := url.Parse(invitationBegin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	invitationServiceURL, err := url.Parse(invitationLoginURL.Query().Get("service"))
	if err != nil {
		t.Fatal(err)
	}
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
	if invitationCallback.Code != http.StatusSeeOther || invitationCallback.Header().Get("Location") != "/join" {
		t.Fatalf("claimed invitation callback = %d location=%q body=%s", invitationCallback.Code, invitationCallback.Header().Get("Location"), invitationCallback.Body.String())
	}
	invitationIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), invitationProviderID, subject)
	if err != nil {
		t.Fatal(err)
	}
	invitationUser, err := persistence.User().Get(context.Background(), invitationIdentity.UserID.String())
	if err != nil || invitationUser.Email != invitedEmail || !invitationUser.EmailVerified {
		t.Fatalf("invitation-admitted User = %#v, %v", invitationUser, err)
	}
	acceptedInvitation, err := persistence.Invitation().GetByClaimHash(context.Background(), model.HashInvitationClaim(claim))
	if err != nil || acceptedInvitation.ID != pendingInvitation.ID || acceptedInvitation.State != model.InvitationAccepted ||
		acceptedInvitation.AcceptedUserID != invitationUser.ID {
		t.Fatalf("admission terminal Invitation = %#v, %v", acceptedInvitation, err)
	}
	invitationAffiliations, _ := persistence.Affiliation().ListByUser(context.Background(), invitationUser.ID.String())
	invitationClassMembers, _ := persistence.ClassMember().ListByUser(context.Background(), invitationUser.ID.String())
	invitationBindings, _ := persistence.RoleBinding().ListByUser(context.Background(), invitationUser.ID.String())
	if len(invitationAffiliations) != 1 || invitationAffiliations[0].ID != acceptedInvitation.AcceptedAffiliationID ||
		len(invitationClassMembers) != 1 || invitationClassMembers[0].ID != acceptedInvitation.AcceptedClassMemberID ||
		len(invitationBindings) != 0 {
		t.Fatalf("invitation admission package: affiliations=%#v class_members=%#v bindings=%#v",
			invitationAffiliations, invitationClassMembers, invitationBindings)
	}
	invitationSessions, err := persistence.Session().ListActiveByUser(context.Background(), invitationUser.ID.String(), model.GetMillis())
	if err != nil || len(invitationSessions) != 0 {
		t.Fatalf("Invitation proof created an ordinary Session = %#v, %v", invitationSessions, err)
	}
	invitationAudits, err := persistence.Audit().List(context.Background(), store.AuditListOptions{ActorId: invitationUser.ID.String(),
		Action: "invitation.accept", Limit: 10, Visibility: store.AuditVisibilityScope{InstitutionWide: true}})
	if err != nil || len(invitationAudits) != 1 || invitationAudits[0].AuthMethod != config.ExternalAuthenticationTypeCAS {
		t.Fatalf("CAS Invitation acceptance audits = %#v, %v", invitationAudits, err)
	}
	invitationReplay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(invitationReplay, invitationCallbackRequest.Clone(context.Background()))
	if invitationReplay.Code != http.StatusUnauthorized {
		t.Fatalf("invitation callback replay = %d body=%s", invitationReplay.Code, invitationReplay.Body.String())
	}
	if strings.Contains(helper.Logs.String(), claim) {
		t.Fatal("raw Invitation claim appeared in CAS logs")
	}
	subject, claimedUsername, claimedEmail = linkedSubject, "external.student", "external.student@example.edu"

	connectRequest := httptest.NewRequest(http.MethodPost,
		"/api/v1/authentication-methods/providers/"+connectionProviderID+"/connect",
		strings.NewReader(`{"return_to":"/connected"}`))
	connectRequest.Header.Set("Content-Type", "application/json")
	connectRequest.Header.Set("Authorization", "Bearer "+accessCookie.Value)
	connect := httptest.NewRecorder()
	helper.Handler().ServeHTTP(connect, connectRequest)
	if connect.Code != http.StatusCreated {
		t.Fatalf("provider connection begin = %d: %s", connect.Code, connect.Body.String())
	}
	var connectionStart struct {
		RedirectURL string `json:"redirect_url"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err = json.Unmarshal(connect.Body.Bytes(), &connectionStart); err != nil {
		t.Fatal(err)
	}
	connectionURL, err := url.Parse(connectionStart.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	connectionService, err := url.Parse(connectionURL.Query().Get("service"))
	if err != nil {
		t.Fatal(err)
	}
	connectionStateToken := connectionService.Query().Get("state")
	connectionState, err := persistence.ExternalLoginState().GetByStateHash(
		context.Background(), model.HashToken(connectionStateToken),
	)
	if err != nil || connectionState.ExpiresAt.Sub(connectionState.CreatedAt) != 10*time.Minute ||
		connectionStart.ExpiresAt != connectionState.ExpiresAt.UnixMilli() {
		t.Fatalf("provider connection state = %#v response expiry=%d error=%v", connectionState, connectionStart.ExpiresAt, err)
	}
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
	if connectionCallback.Code != http.StatusSeeOther || connectionCallback.Header().Get("Location") != "/connected" {
		t.Fatalf("provider connection callback = %d location=%q body=%s", connectionCallback.Code,
			connectionCallback.Header().Get("Location"), connectionCallback.Body.String())
	}
	connectedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), connectionProviderID, linkedSubject)
	if err != nil || connectedIdentity.UserID != user.ID {
		t.Fatalf("connected identity = %#v, %v", connectedIdentity, err)
	}
	connectedSessions, err := persistence.Session().ListActiveByUser(context.Background(), user.ID.String(), model.GetMillis())
	if err != nil || len(connectedSessions) != 1 {
		t.Fatalf("provider connection created a Session: %#v, %v", connectedSessions, err)
	}
	if response := loginProvider(connectionProviderID, "linked-linked-only"); response.Code != http.StatusSeeOther {
		t.Fatalf("existing linked_only identity login status = %d", response.Code)
	}
	unlinkRequest := httptest.NewRequest(http.MethodDelete,
		"/api/v1/authentication-methods/providers/"+connectedIdentity.ID.String(), nil)
	unlinkRequest.Header.Set("Authorization", "Bearer "+accessCookie.Value)
	unlink := httptest.NewRecorder()
	helper.Handler().ServeHTTP(unlink, unlinkRequest)
	if unlink.Code != http.StatusNoContent {
		t.Fatalf("provider unlink = %d: %s", unlink.Code, unlink.Body.String())
	}
	retainedSession, err := persistence.Session().Get(context.Background(), sessions[0].ID.String())
	if err != nil || retainedSession.RevokedAt.Valid {
		t.Fatalf("unrelated provider Session was revoked: %#v, %v", retainedSession, err)
	}

	replayRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	replayRequest.AddCookie(bindingCookie)
	replay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("callback replay status = %d: %s", replay.Code, replay.Body.String())
	}

	claimedUsername, claimedEmail = "changed.claim", "changed.claim@example.edu"
	claimedFirstName, claimedLastName = "Changed", "Profile"
	if response := loginProvider(providerID, "changed-profile"); response.Code != http.StatusSeeOther {
		t.Fatalf("existing linked user login status = %d", response.Code)
	}
	unchangedUser, err := persistence.User().Get(context.Background(), user.ID.String())
	if err != nil || unchangedUser.Username != "external.student" ||
		unchangedUser.Email != "external.student@example.edu" || unchangedUser.FirstName != "External" || unchangedUser.LastName != "Student" {
		t.Fatalf("provider claim changes overwrote established User = %#v, %v", unchangedUser, err)
	}

	subject = "ineligible-subject"
	claimedUsername, claimedEmail = "ineligible.user", "ineligible.user@example.edu"
	claimedHomeOrganization = "outside.example"
	if response := loginProvider(providerID, "ineligible"); response.Code != http.StatusUnauthorized {
		t.Fatalf("ineligible auto_provision status = %d", response.Code)
	}
	if _, err = persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, subject); !store.IsNotFound(err) {
		t.Fatalf("ineligible subject persisted an identity: %v", err)
	}
	subject, claimedHomeOrganization = linkedSubject, "example.edu"
	claimedUsername, claimedEmail = "external.student", "external.student@example.edu"
	claimedFirstName, claimedLastName = "External", "Student"

	removalBegin := performJSONRequest(helper.Handler(), http.MethodGet,
		"/api/v1/auth/providers/"+providerID+"/login?client_type=web&device_id=provider-removal", nil, "")
	if removalBegin.Code != http.StatusSeeOther {
		t.Fatalf("provider-removal begin status = %d: %s", removalBegin.Code, removalBegin.Body.String())
	}
	removalLoginURL, err := url.Parse(removalBegin.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	removalServiceURL, err := url.Parse(removalLoginURL.Query().Get("service"))
	if err != nil {
		t.Fatal(err)
	}
	removalQuery := removalServiceURL.Query()
	removalQuery.Set("ticket", ticket)
	removalCallbackPath := removalServiceURL.Path + "?" + removalQuery.Encode()
	var removalBinding *http.Cookie
	for _, cookie := range removalBegin.Result().Cookies() {
		if cookie.Name == httpapi.BrowserExternalLoginCookieName {
			removalBinding = cookie
		}
	}
	if removalBinding == nil {
		t.Fatal("provider-removal binding cookie is missing")
	}
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
	if removedResponse.Code != http.StatusNotFound {
		t.Fatalf("removed-provider callback status = %d: %s", removedResponse.Code, removedResponse.Body.String())
	}
	preservedIdentity, err := persistence.ExternalIdentity().GetByProviderSubject(context.Background(), providerID, linkedSubject)
	if err != nil || preservedIdentity.ID != identity.ID || preservedIdentity.UserID != user.ID {
		t.Fatalf("provider removal changed durable identity = %#v, %v", preservedIdentity, err)
	}
	restoredRequest := httptest.NewRequest(http.MethodGet, removalCallbackPath, nil)
	restoredRequest.AddCookie(removalBinding)
	restoredResponse := httptest.NewRecorder()
	helper.Handler().ServeHTTP(restoredResponse, restoredRequest)
	if restoredResponse.Code != http.StatusSeeOther {
		t.Fatalf("restored-provider callback status = %d: %s", restoredResponse.Code, restoredResponse.Body.String())
	}
	currentUser, err := persistence.User().Get(context.Background(), user.ID.String())
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.User().SetDisabledWithAudit(
		context.Background(),
		storetest.UserDisabledStateChangeWithNotice(t, &store.UserDisabledStateChange{
			ID:               user.ID.String(),
			ExpectedRevision: currentUser.Revision,
			Disabled:         true,
			ChangedAt:        disabledAt,
			RevocationReason: model.SessionRevocationAccountDisabled,
			AuditEventID:     auditAttempt.ID.String(),
			AuditAt:          disabledAt,
		}),
	); err != nil {
		t.Fatal(err)
	}
	if response := loginProvider(providerID, "disabled-user"); response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled linked user login status = %d", response.Code)
	}
	subject = conflictingSubject
	conflictResponse := loginProvider(providerID, "conflicting-email")
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("conflicting provision status = %d", conflictResponse.Code)
	}
	if body := conflictResponse.Body.String(); strings.Contains(body, conflictingSubject) || strings.Contains(body, claimedEmail) || strings.Contains(body, user.ID.String()) {
		t.Fatalf("conflicting provision response disclosed account or provider claims: %s", body)
	}
	for _, logs := range []string{helper.Logs.String(), removedProviderNode.Logs.String()} {
		for _, secret := range []string{ticket, linkedSubject, conflictingSubject, "ineligible-subject", bindingCookie.Value, removalBinding.Value} {
			if strings.Contains(logs, secret) {
				t.Fatalf("external authentication secret appeared in logs")
			}
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
	unit, err = persistence.AcademicUnit().Save(ctx, unit)
	if err != nil {
		t.Fatal(err)
	}
	programme := &model.Programme{AcademicUnitID: unit.ID, Name: "external-admission", DisplayName: "External Admission"}
	programme, err = persistence.Programme().Save(ctx, programme)
	if err != nil {
		t.Fatal(err)
	}
	level := &model.ProgrammeLevel{ProgrammeID: programme.ID, Name: "external-admission", DisplayName: "External Admission"}
	level, err = persistence.ProgrammeLevel().Save(ctx, level)
	if err != nil {
		t.Fatal(err)
	}
	period := &model.AcademicPeriod{Owner: model.NewAcademicUnitAcademicPeriodOwner(unit.ID),
		Name: "external-admission", DisplayName: "External Admission", StartsAt: at.Add(-time.Hour), EndsAt: at.Add(24 * time.Hour)}
	period, err = persistence.AcademicPeriod().Save(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	class := &model.Class{ProgrammeLevelID: level.ID, AcademicPeriodID: period.ID,
		Name: "external-admission", DisplayName: "External Admission"}
	class, err = persistence.Class().Save(ctx, class)
	if err != nil {
		t.Fatal(err)
	}
	role, err := persistence.Role().Save(ctx, &model.Role{Name: "external-inviter-" + model.NewId(),
		DisplayName: "External Invitation Manager",
		Permissions: []string{string(model.ActionInvitationCreate), string(model.ActionClassMembersManage)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.RoleBinding().Save(ctx, &model.RoleBinding{UserID: inviter.ID, RoleID: role.ID,
		ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), StartsAt: at.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	claim := model.NewCredentialToken()
	invitation, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{
		ID: model.NewInvitationID(), TargetEmail: targetEmail, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: at, InviterUserID: inviter.ID, ScopeType: model.RoleScopeClass,
		ScopeID: class.ID.String(), ClaimHash: model.HashInvitationClaim(claim), IssuedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = persistence.GetMaster().Exec(ctx, `
		INSERT INTO invitations (
			id, created_at, updated_at, revision, purpose, state, target_email, class_id, academic_period_id,
			academic_unit_id, role_id, role_actions, intended_start_at, intended_end_at,
			suggested_username, suggested_display_name, suggested_first_name, suggested_last_name, suggested_locale,
			inviter_user_id, scope_type, scope_id, claim_hash, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, '{}', ?, NULL, '', '', '', '', '', ?, ?, ?, ?, ?)`,
		invitation.ID.String(), invitation.CreatedAt, invitation.UpdatedAt, invitation.Revision,
		invitation.Purpose, invitation.State, invitation.TargetEmail, invitation.ClassID.String(), invitation.AcademicPeriodID.String(),
		invitation.IntendedStartsAt, invitation.InviterUserID.String(), invitation.ScopeType, invitation.ScopeID,
		invitation.ClaimHash, invitation.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	return claim, invitation
}
