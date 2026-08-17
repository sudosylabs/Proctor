//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/cluster"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAccessPolicyTwoNodePostgreSQLFenceAndSafeRevisionFanout(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	primaryCluster := &userSettingsRecordingCluster{nodeID: "access-policy-node-a"}
	secondaryCluster := &userSettingsRecordingCluster{nodeID: "access-policy-node-b"}
	baseConfig := func(cfg *config.Config) {
		cfg.Authentication.MFA.Enabled = true
		cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))
	}
	mfaConfig := func(cfg *config.Config) {
		baseConfig(cfg)
		cfg.Authentication.External.Providers = []config.ExternalAuthenticationProvider{{
			ID: "campus", Type: config.ExternalAuthenticationTypeCAS, DisplayName: "Campus",
			Enabled: true, AutoProvision: true,
			CAS: &config.CASProvider{BaseURL: "https://cas.example.edu/cas", ValidationPath: "/p3/serviceValidate",
				Timeout: config.Duration{Duration: 5 * time.Second}, MaxResponseBytes: 64 * 1024},
			Claims: config.ExternalClaimMapping{Subject: "user", Username: "uid", Email: "mail"},
		}}
	}
	primary := testlib.Setup(t, testlib.WithConfig(mfaConfig), testlib.WithStore(primaryStore), testlib.WithCluster(primaryCluster))
	secondary := testlib.Setup(t, testlib.WithConfig(mfaConfig), testlib.WithStore(secondaryStore), testlib.WithCluster(secondaryCluster))

	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "policy-two-node", "display_name": "Policy Two Node"},
		"administrator":    map[string]any{"username": "policy-admin", "email": "policy-admin@example.edu"},
		"password":         "correct horse battery staple",
	}, "")
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	login, err := primary.App.Login(context.Background(), application.Invocation{}, application.LoginCommand{
		LoginID: "policy-admin", Password: "correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "policy-cli", Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("login: %v; logs=%s", err, primary.Logs.String())
	}
	strengthenRoleAdministratorSession(t, primary.Handler(), login.Tokens.AccessToken)
	principal, err := primary.App.AuthenticateAccess(context.Background(), login.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	invocation := application.NewInvocation(*principal, model.RequestMetadata{RequestID: "access-policy-two-node"})
	current, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	if err != nil || current.Policy == nil || current.Policy.Revision != 1 {
		t.Fatalf("initial policy = %#v, %v", current, err)
	}

	type outcome struct {
		view application.AccessPolicyView
		err  error
	}
	outcomes := make(chan outcome, 2)
	for _, candidate := range []struct {
		app      *application.App
		settings model.AccessPolicySettings
		key      string
	}{
		{app: primary.App, settings: func() model.AccessPolicySettings {
			value := current.Policy.Settings()
			value.DesktopAuthorizationEnabled = false
			return value
		}(), key: "access-policy-a"},
		{app: secondary.App, settings: func() model.AccessPolicySettings {
			value := current.Policy.Settings()
			value.PublicRegistrationEnabled = true
			return value
		}(), key: "access-policy-b"},
	} {
		go func(candidate struct {
			app      *application.App
			settings model.AccessPolicySettings
			key      string
		}) {
			view, replaceErr := candidate.app.ReplaceAccessPolicy(context.Background(), invocation, application.ReplaceAccessPolicyCommand{
				ExpectedRevision: 1, Settings: candidate.settings, IdempotencyKey: candidate.key,
			})
			outcomes <- outcome{view: view, err: replaceErr}
		}(candidate)
	}
	successes, conflicts := 0, 0
	for range 2 {
		completed := <-outcomes
		switch {
		case completed.err == nil:
			successes++
			if completed.view.Policy == nil || completed.view.Policy.Revision != 2 {
				t.Fatalf("winning policy = %#v", completed.view)
			}
		case application.Is(completed.err, "access_policy.revision_conflict"):
			conflicts++
		default:
			t.Fatalf("replacement error = %v", completed.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("outcomes success/conflict = %d/%d", successes, conflicts)
	}

	primaryDiscovery, err := primary.App.DiscoverAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondaryDiscovery, err := secondary.App.DiscoverAccess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if primaryDiscovery.PolicyRevision != 2 || secondaryDiscovery.PolicyRevision != 2 ||
		primaryDiscovery.Capabilities != secondaryDiscovery.Capabilities {
		t.Fatalf("discovery diverged: primary=%#v secondary=%#v", primaryDiscovery, secondaryDiscovery)
	}

	events := append(accessPolicyClusterEvents(primaryCluster), accessPolicyClusterEvents(secondaryCluster)...)
	if len(events) != 1 || !bytes.Contains(events[0].Data, []byte(`"event":"access_policy_changed"`)) ||
		!bytes.Contains(events[0].Data, []byte(`"revision":2`)) {
		t.Fatalf("access policy events = %#v", events)
	}
	for _, forbidden := range []string{"provider_admissions", "local_login_enabled", "public_registration_enabled", "idempotency", "session_id"} {
		if bytes.Contains(events[0].Data, []byte(forbidden)) {
			t.Fatalf("access policy event exposed %q: %s", forbidden, events[0].Data)
		}
	}

	// Exercise lost-response replay through the real HTTP, application, and
	// PostgreSQL graph after the authoritative revision has advanced.
	authoritative, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	if err != nil || authoritative.Policy == nil {
		t.Fatalf("authoritative policy = %#v, %v", authoritative, err)
	}
	settings := authoritative.Policy.Settings()
	settings.DesktopAuthorizationEnabled = !settings.DesktopAuthorizationEnabled
	body := map[string]any{
		"expected_revision": authoritative.Policy.Revision, "revoke_existing_sessions": false,
		"local_login_enabled": settings.LocalLoginEnabled, "public_registration_enabled": settings.PublicRegistrationEnabled,
		"invitation_admission_enabled":        settings.InvitationAdmissionEnabled,
		"invitation_local_credential_enabled": settings.InvitationLocalCredentialEnabled,
		"desktop_authorization_enabled":       settings.DesktopAuthorizationEnabled, "provider_admissions": settings.ProviderAdmissions,
	}
	request := func(candidate map[string]any) *httptest.ResponseRecorder {
		encoded, encodeErr := json.Marshal(candidate)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+login.Tokens.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "access-policy-lost-response")
		response := httptest.NewRecorder()
		primary.Handler().ServeHTTP(response, req)
		return response
	}
	first, replay := request(body), request(body)
	if first.Code != http.StatusOK || replay.Code != http.StatusOK || first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	conflictingBody := make(map[string]any, len(body))
	for key, value := range body {
		conflictingBody[key] = value
	}
	conflictingBody["revoke_existing_sessions"] = true
	conflictResponse := request(conflictingBody)
	if conflictResponse.Code != http.StatusConflict || !bytes.Contains(conflictResponse.Body.Bytes(), []byte(`"code":"idempotency.conflict"`)) {
		t.Fatalf("conflicting reuse=%d %s", conflictResponse.Code, conflictResponse.Body.String())
	}

	audits, err := primaryStore.Audit().List(context.Background(), store.AuditListOptions{
		Action: string(model.ActionAccessPolicyManage), Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	statusCounts := map[model.AuditStatus]int{}
	failureCodes := map[string]bool{}
	foundReplay := false
	for _, event := range audits {
		statusCounts[event.Status]++
		failureCodes[event.ErrorCode] = true
		var terminal struct {
			IdempotencyReplayed  bool   `json:"idempotency_replayed"`
			OriginalAuditEventID string `json:"original_audit_event_id"`
		}
		if json.Unmarshal(event.Result, &terminal) == nil && terminal.IdempotencyReplayed && model.IsValidId(terminal.OriginalAuditEventID) {
			foundReplay = true
		}
	}
	if statusCounts[model.AuditStatusAttempt] != 0 || statusCounts[model.AuditStatusSuccess] < 3 ||
		statusCounts[model.AuditStatusFail] < 2 || !failureCodes["access_policy.revision_conflict"] ||
		!failureCodes["idempotency.conflict"] || !foundReplay {
		t.Fatalf("Access Policy audits statuses=%#v failures=%#v replay=%v", statusCounts, failureCodes, foundReplay)
	}

	// A policy change and later administrator mutations must use the same
	// durable definition of a usable login path. This local-only actor may keep
	// administering through its existing Session, but the externally linked
	// administrator is the only account that could start a new login.
	externalAdmin := createIntegrationUser(t, primary, "policy-external-admin", "another correct horse battery staple")
	administratorRole, err := primaryStore.Role().GetByName(context.Background(), model.SystemAdministratorRoleName)
	if err != nil {
		t.Fatal(err)
	}
	institution, err := primaryStore.Institution().GetSingleton(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	externalBinding, err := primaryStore.RoleBinding().Save(context.Background(), &model.RoleBinding{
		UserID: externalAdmin.ID, RoleID: administratorRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = primaryStore.ExternalIdentity().Save(context.Background(), &model.ExternalIdentity{
		UserID: externalAdmin.ID, Provider: "campus", Subject: "policy-external-admin-subject",
		LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	}); err != nil {
		t.Fatal(err)
	}
	removedProviderAdmin := createIntegrationUser(t, primary, "policy-removed-provider-admin", "yet another correct horse battery staple")
	removedProviderBinding, err := primaryStore.RoleBinding().Save(context.Background(), &model.RoleBinding{
		UserID: removedProviderAdmin.ID, RoleID: administratorRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeDisable, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	disableLocal := beforeDisable.Policy.Settings()
	disableLocal.LocalLoginEnabled = false
	disableLocal.InvitationLocalCredentialEnabled = false
	disableLocal.ProviderAdmissions = map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly}
	if _, err = primary.App.ReplaceAccessPolicy(context.Background(), invocation, application.ReplaceAccessPolicyCommand{
		ExpectedRevision: beforeDisable.Policy.Revision, Settings: disableLocal,
		IdempotencyKey: "access-policy-application-lockout",
	}); err != nil {
		t.Fatal(err)
	}
	removedProviderStore := openAdditionalUserSettingsStore(t, dataSource)
	removedProviderNode := testlib.Setup(t, testlib.WithConfig(baseConfig), testlib.WithStore(removedProviderStore))
	if _, err = removedProviderNode.App.EndRoleBinding(context.Background(), invocation, application.EndRoleBindingCommand{
		ID: removedProviderBinding.ID.String(),
	}); !application.Is(err, "role_binding.last_system_admin") {
		t.Fatalf("removed-provider node counted unavailable external administrator: %v", err)
	}
	if _, err = primary.App.EndRoleBinding(context.Background(), invocation, application.EndRoleBindingCommand{
		ID: removedProviderBinding.ID.String(),
	}); err != nil {
		t.Fatalf("configured-provider node rejected external administrator path: %v", err)
	}
	if _, err = primary.App.SetUserEnabled(context.Background(), invocation, application.SetUserEnabledCommand{
		ID: externalAdmin.ID.String(), Enabled: false,
	}); !application.Is(err, "user.last_system_admin") {
		t.Fatalf("disable only policy-usable administrator error = %v", err)
	}
	if _, err = primary.App.EndRoleBinding(context.Background(), invocation, application.EndRoleBindingCommand{
		ID: externalBinding.ID.String(),
	}); !application.Is(err, "role_binding.last_system_admin") {
		t.Fatalf("end only policy-usable administrator binding error = %v", err)
	}
}

func accessPolicyClusterEvents(recorder *userSettingsRecordingCluster) []*cluster.Message {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	result := []*cluster.Message{}
	for _, event := range recorder.events {
		if event.Event == cluster.Event("websocket.publish") && bytes.Contains(event.Data, []byte(`"event":"access_policy_changed"`)) {
			result = append(result, event.Clone())
		}
	}
	return result
}
