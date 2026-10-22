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
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestAccessPolicyTwoNodePostgreSQLFenceAndSafeRevisionFanout(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
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
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap = %d: %s", bootstrap.Code, bootstrap.Body.String())
	login, err := primary.App.Login(context.Background(), invocation.Invocation{}, app.LoginCommand{
		LoginID: "policy-admin", Password: "correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "policy-cli", Source: "127.0.0.1:1",
	})
	require.NoError(t, err, "login: %v; logs=%s", err, primary.Logs.String())
	strengthenRoleAdministratorSession(t, primary.Handler(), login.Tokens.AccessToken)
	principal, err := primary.App.AuthenticateAccess(context.Background(), login.Tokens.AccessToken)
	require.NoError(t, err)
	invocation := invocation.New(*principal, model.RequestMetadata{RequestID: "access-policy-two-node"})
	current, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	require.NoError(t, err, "initial policy = %#v", current)
	require.NotNil(t, current.Policy, "initial policy = %#v", current)
	require.Equal(t, int64(1), current.Policy.Revision, "initial policy = %#v", current)

	type outcome struct {
		view app.AccessPolicyView
		err  error
	}
	outcomes := make(chan outcome, 2)
	for _, candidate := range []struct {
		app      *app.App
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
			app      *app.App
			settings model.AccessPolicySettings
			key      string
		}) {
			view, replaceErr := candidate.app.ReplaceAccessPolicy(context.Background(), invocation, app.ReplaceAccessPolicyCommand{
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
			require.NotNil(t, completed.view.Policy)
			require.Equal(t, int64(2), completed.view.Policy.Revision)
		case apperror.Is(completed.err, "access_policy.revision_conflict"):
			conflicts++
		default:
			require.FailNow(t, fmt.Sprintf("replacement error = %v", completed.err))
		}
	}
	require.Equal(t, 1, successes, "outcomes success/conflict")
	require.Equal(t, 1, conflicts, "outcomes success/conflict")

	primaryDiscovery, err := primary.App.DiscoverAccess(context.Background())
	require.NoError(t, err)
	secondaryDiscovery, err := secondary.App.DiscoverAccess(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), primaryDiscovery.PolicyRevision, "discovery diverged: primary=%#v secondary=%#v", primaryDiscovery, secondaryDiscovery)
	require.Equal(t, int64(2), secondaryDiscovery.PolicyRevision, "discovery diverged: primary=%#v secondary=%#v", primaryDiscovery, secondaryDiscovery)
	require.Equal(t, secondaryDiscovery.Capabilities, primaryDiscovery.Capabilities, "discovery diverged: primary=%#v secondary=%#v", primaryDiscovery, secondaryDiscovery)

	events := append(accessPolicyClusterEvents(primaryCluster), accessPolicyClusterEvents(secondaryCluster)...)
	require.Len(t, events, 1)
	require.True(t, bytes.Contains(events[0].Data, []byte(`"event":"access_policy_changed"`)))
	require.True(t, bytes.Contains(events[0].Data, []byte(`"revision":2`)))
	for _, forbidden := range []string{"provider_admissions", "local_login_enabled", "public_registration_enabled", "idempotency", "session_id"} {
		require.False(t, bytes.Contains(events[0].Data, []byte(forbidden)), "access policy event exposed")
	}

	// Exercise lost-response replay through the real HTTP, application, and
	// PostgreSQL graph after the authoritative revision has advanced.
	authoritative, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	require.NoError(t, err, "authoritative policy = %#v", authoritative)
	require.NotNil(t, authoritative.Policy, "authoritative policy = %#v", authoritative)
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
		require.NoError(t, encodeErr)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/access-policy", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+login.Tokens.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "access-policy-lost-response")
		response := httptest.NewRecorder()
		primary.Handler().ServeHTTP(response, req)
		return response
	}
	first, replay := request(body), request(body)
	require.Equal(t, http.StatusOK, first.Code, "first")
	require.Equal(t, http.StatusOK, replay.Code, "first")
	require.Equal(t, replay.Body.String(), first.Body.String(), "first")
	conflictingBody := make(map[string]any, len(body))
	for key, value := range body {
		conflictingBody[key] = value
	}
	conflictingBody["revoke_existing_sessions"] = true
	conflictResponse := request(conflictingBody)
	require.Equal(t, http.StatusConflict, conflictResponse.Code, "conflicting reuse=%d %s", conflictResponse.Code, conflictResponse.Body.String())
	require.True(t, bytes.Contains(conflictResponse.Body.Bytes(), []byte(`"code":"idempotency.conflict"`)), "conflicting reuse=%d %s", conflictResponse.Code, conflictResponse.Body.String())

	audits, err := primaryStore.Audit().List(context.Background(), store.AuditListOptions{
		Action: string(model.ActionAccessPolicyManage), Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
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
		if json.Unmarshal(event.Result, &terminal) == nil && terminal.IdempotencyReplayed && model.AuditEventID(terminal.OriginalAuditEventID).IsValid() {
			foundReplay = true
		}
	}
	require.Equal(t, 0, statusCounts[model.AuditStatusAttempt], "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)
	require.GreaterOrEqual(t, statusCounts[model.AuditStatusSuccess], 3, "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)
	require.GreaterOrEqual(t, statusCounts[model.AuditStatusFail], 2, "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)
	require.True(t, failureCodes["access_policy.revision_conflict"], "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)
	require.True(t, failureCodes["idempotency.conflict"], "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)
	require.True(t, foundReplay, "Access Policy audits statuses=%#v failures=%#v replay", statusCounts, failureCodes)

	// A policy change and later administrator mutations must use the same
	// durable definition of a usable login path. This local-only actor may keep
	// administering through its existing Session, but the externally linked
	// administrator is the only account that could start a new login.
	externalAdmin := createIntegrationUser(t, primary, "policy-external-admin", "another correct horse battery staple")
	administratorRole, err := primaryStore.Role().GetByName(context.Background(), model.SystemAdministratorRoleName)
	require.NoError(t, err)
	institution, err := primaryStore.Institution().GetSingleton(context.Background())
	require.NoError(t, err)
	externalBinding, err := storetest.SaveRoleBinding(context.Background(), primaryStore, &model.RoleBinding{
		UserID: externalAdmin.ID, RoleID: administratorRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	require.NoError(t, err)
	_, err = primaryStore.ExternalIdentity().Save(context.Background(), &model.ExternalIdentity{
		UserID: externalAdmin.ID, Provider: "campus", Subject: "policy-external-admin-subject",
		LastSeenAt: model.OptionalTimeFrom(model.NowUTC()),
	})
	require.NoError(t, err)
	removedProviderAdmin := createIntegrationUser(t, primary, "policy-removed-provider-admin", "yet another correct horse battery staple")
	removedProviderBinding, err := storetest.SaveRoleBinding(context.Background(), primaryStore, &model.RoleBinding{
		UserID: removedProviderAdmin.ID, RoleID: administratorRole.ID,
		ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
		StartsAt: model.TimeFromMillis(model.GetMillis() - 100),
	})
	require.NoError(t, err)
	beforeDisable, err := primary.App.GetAccessPolicy(context.Background(), invocation)
	require.NoError(t, err)
	disableLocal := beforeDisable.Policy.Settings()
	disableLocal.LocalLoginEnabled = false
	// Either racing policy may win; public registration requires local login.
	disableLocal.PublicRegistrationEnabled = false
	disableLocal.InvitationLocalCredentialEnabled = false
	disableLocal.ProviderAdmissions = map[string]model.ProviderAdmissionMode{"campus": model.ProviderAdmissionLinkedOnly}
	_, err = primary.App.ReplaceAccessPolicy(context.Background(), invocation, app.ReplaceAccessPolicyCommand{
		ExpectedRevision: beforeDisable.Policy.Revision, Settings: disableLocal,
		IdempotencyKey: "access-policy-application-lockout",
	})
	require.NoError(t, err)
	removedProviderStore := openAdditionalUserSettingsStore(t, dataSource)
	removedProviderNode := testlib.Setup(t, testlib.WithConfig(baseConfig), testlib.WithStore(removedProviderStore))
	_, err = removedProviderNode.App.EndRoleBinding(context.Background(), invocation, app.EndRoleBindingCommand{
		ID: removedProviderBinding.ID,
	})
	require.True(t, apperror.Is(err, "role_binding.last_system_admin"), "removed-provider node counted unavailable external administrator: %v", err)
	_, err = primary.App.EndRoleBinding(context.Background(), invocation, app.EndRoleBindingCommand{
		ID: removedProviderBinding.ID,
	})
	require.NoError(t, err)
	_, err = primary.App.SetUserEnabled(context.Background(), invocation, app.SetUserEnabledCommand{
		ID: externalAdmin.ID, Enabled: false,
	})
	require.True(t, apperror.Is(err, "user.last_system_admin"), "disable only policy-usable administrator error = %v", err)
	_, err = primary.App.EndRoleBinding(context.Background(), invocation, app.EndRoleBindingCommand{
		ID: externalBinding.ID,
	})
	require.True(t, apperror.Is(err, "role_binding.last_system_admin"), "end only policy-usable administrator binding error = %v", err)
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
