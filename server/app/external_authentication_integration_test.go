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

	"github.com/sudosylabs/proctor/server/app/api"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestCASExternalAuthenticationIntegration(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	const (
		providerID         = "campus-cas"
		ticket             = "ST-sensitive-ticket"
		linkedSubject      = "opaque-sensitive-subject"
		conflictingSubject = "opaque-conflicting-subject"
	)
	subject := linkedSubject
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
      <cas:uid>external.student</cas:uid>
      <cas:mail>external.student@example.edu</cas:mail>
      <cas:givenName>External</cas:givenName>
      <cas:sn>Student</cas:sn>
      <cas:schacHomeOrganization>example.edu</cas:schacHomeOrganization>
      <cas:authnContext>mfa</cas:authnContext>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`))
	}))
	defer casServer.Close()

	persistence := openAuthenticationStore(t, dataSource)
	helper := testlib.Setup(
		t,
		testlib.WithConfig(func(cfg *config.Config) {
			cfg.Server.PublicURL = "http://proctor.example.test"
			cfg.Authentication.LoginRateLimit.MaximumSourceAttempts = 100
			cfg.Authentication.External.Providers = []config.ExternalAuthenticationProvider{{
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
			}}
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
		len(providerList) != 1 ||
		providerList[0].Id != providerID ||
		strings.Contains(providers.Body.String(), casServer.URL) {
		t.Fatalf("provider discovery status=%d body=%s", providers.Code, providers.Body.String())
	}

	begin := performJSONRequest(
		helper.Handler(),
		http.MethodGet,
		"/api/v1/auth/providers/"+providerID+
			"/login?client_type=desktop&device_id=electron-1&return_to=%2Fafter-login",
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
		if cookie.Name == api.BrowserExternalLoginCookieName {
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
		if cookie.Name == api.BrowserAccessCookieName {
			accessCookie = cookie
			break
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
		sessions[0].AuthenticationStrength != model.AuthenticationMultiFactor {
		t.Fatalf("external sessions = %#v, %v", sessions, err)
	}
	audits, err := persistence.Audit().List(
		context.Background(),
		store.AuditListOptions{
			ActorId: user.ID.String(),
			Limit:   10,
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

	replayRequest := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	replayRequest.AddCookie(bindingCookie)
	replay := httptest.NewRecorder()
	helper.Handler().ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("callback replay status = %d: %s", replay.Code, replay.Body.String())
	}

	loginAgain := func() int {
		begin := performJSONRequest(
			helper.Handler(), http.MethodGet,
			"/api/v1/auth/providers/"+providerID+"/login?client_type=desktop&device_id=electron-2",
			nil, "",
		)
		if begin.Code != http.StatusSeeOther {
			t.Fatalf("repeat begin status = %d: %s", begin.Code, begin.Body.String())
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
			if cookie.Name == api.BrowserExternalLoginCookieName {
				request.AddCookie(cookie)
			}
		}
		response := httptest.NewRecorder()
		helper.Handler().ServeHTTP(response, request)
		return response.Code
	}

	if status := loginAgain(); status != http.StatusSeeOther {
		t.Fatalf("existing linked user login status = %d", status)
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
		&store.UserDisabledStateChange{
			ID:               user.ID.String(),
			ExpectedRevision: currentUser.Revision,
			Disabled:         true,
			ChangedAt:        disabledAt,
			RevocationReason: "external authentication integration disabled account",
			AuditEventID:     auditAttempt.ID.String(),
			AuditAt:          disabledAt,
		},
	); err != nil {
		t.Fatal(err)
	}
	if status := loginAgain(); status != http.StatusUnauthorized {
		t.Fatalf("disabled linked user login status = %d", status)
	}
	subject = conflictingSubject
	if status := loginAgain(); status != http.StatusConflict {
		t.Fatalf("conflicting provision status = %d", status)
	}
	logs := helper.Logs.String()
	for _, secret := range []string{ticket, linkedSubject, conflictingSubject, bindingCookie.Value} {
		if strings.Contains(logs, secret) {
			t.Fatalf("external authentication secret appeared in logs")
		}
	}
}
