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
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestRetentionPolicyHTTPAcrossNodesPreservesAuthorizationAndRetry(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	require.NotEmpty(t, dataSource, "PROCTOR_TEST_DATABASE_URL is not set")
	primaryStore := openAuthenticationStore(t, dataSource)
	secondaryStore := openAdditionalUserSettingsStore(t, dataSource)
	mfaConfig := func(cfg *config.Config) {
		cfg.Authentication.MFA.Enabled = true
		cfg.Authentication.MFA.EncryptionKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))
	}
	primary := testlib.Setup(t, testlib.WithConfig(mfaConfig), testlib.WithStore(primaryStore))
	secondary := testlib.Setup(t, testlib.WithConfig(mfaConfig), testlib.WithStore(secondaryStore))
	bootstrap := performJSONRequest(primary.Handler(), http.MethodPost, "/api/v1/bootstrap", map[string]any{
		"bootstrap_secret": testlib.BootstrapSecret,
		"institution":      map[string]any{"name": "retention-institution", "display_name": "Retention Institution"},
		"administrator":    map[string]any{"username": "retention-admin", "email": "retention-admin@example.edu"},
		"password":         "correct horse battery staple",
	}, "")
	require.Equal(t, http.StatusCreated, bootstrap.Code, "bootstrap: %d %s", bootstrap.Code, bootstrap.Body.String())
	login, err := primary.App.Login(context.Background(), app.Invocation{}, app.LoginCommand{
		LoginID: "retention-admin", Password: "correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "retention-cli", Source: "127.0.0.1:1",
	})
	require.NoError(t, err)
	const body = `{"expected_revision":1,"submission_retention_days":730,"integrity_retention_days":365,"browser_activity_retention_days":0,"security_operational_retention_days":0,"audit_retention_days":0,"export_retention_days":7,"deletion_grace_days":30}`
	request := func(handler http.Handler, method, key, token string) *httptest.ResponseRecorder {
		content := ""
		if method == http.MethodPut {
			content = body
		}
		req := httptest.NewRequest(method, "/api/v1/retention-policy", bytes.NewBufferString(content))
		req.Header.Set("Authorization", "Bearer "+token)
		if method == http.MethodPut {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	weak := request(primary.Handler(), http.MethodPut, "retention-change", login.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, weak.Code, "weak replacement: %d %s", weak.Code, weak.Body.String())
	require.True(t, bytes.Contains(weak.Body.Bytes(), []byte(`"code":"authentication.strong_required"`)), "weak replacement: %d %s", weak.Code, weak.Body.String())
	strengthenRoleAdministratorSession(t, primary.Handler(), login.Tokens.AccessToken)
	first := request(primary.Handler(), http.MethodPut, "retention-change", login.Tokens.AccessToken)
	replay := request(secondary.Handler(), http.MethodPut, "retention-change", login.Tokens.AccessToken)
	read := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	for _, response := range []*httptest.ResponseRecorder{first, replay, read} {
		require.Equal(t, http.StatusOK, response.Code, "policy response: %d %s", response.Code, response.Body.String())
		require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "policy response: %d %s", response.Code, response.Body.String())
		var policy map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &policy))
		require.Equal(t, float64(2), policy["revision"])
		require.Equal(t, float64(730), policy["submission_retention_days"])
		require.Equal(t, false, policy["automatic_deletion_enabled"])
	}
	require.Equal(t, replay.Body.String(), first.Body.String(), "policy diverged across nodes or lost-response replay")
	require.Equal(t, read.Body.String(), first.Body.String(), "policy diverged across nodes or lost-response replay")
	stale := request(secondary.Handler(), http.MethodPut, "stale-editor", login.Tokens.AccessToken)
	require.Equal(t, http.StatusConflict, stale.Code, "stale replacement: %d %s", stale.Code, stale.Body.String())
	require.True(t, bytes.Contains(stale.Body.Bytes(), []byte(`"code":"retention_policy.revision_conflict"`)), "stale replacement: %d %s", stale.Code, stale.Body.String())
	ordinary := createIntegrationUser(t, primary, "retention-ordinary", "another correct horse battery staple")
	ordinaryLogin, err := secondary.App.Login(context.Background(), app.Invocation{}, app.LoginCommand{
		LoginID: ordinary.Username, Password: "another correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "ordinary-cli", Source: "127.0.0.1:2",
	})
	require.NoError(t, err)
	denied := request(secondary.Handler(), http.MethodGet, "", ordinaryLogin.Tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, denied.Code, "ordinary read: %d %s", denied.Code, denied.Body.String())
	require.True(t, bytes.Contains(denied.Body.Bytes(), []byte(`"code":"authorization.denied"`)), "ordinary read: %d %s", denied.Code, denied.Body.String())
	audits, err := primaryStore.Audit().List(context.Background(), store.AuditListOptions{
		Action: string(model.ActionRetentionPolicyManage), Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	require.NoError(t, err)
	success, failed, replayAudited := 0, 0, false
	for _, event := range audits {
		switch event.Status {
		case model.AuditStatusSuccess:
			success++
		case model.AuditStatusFail:
			failed++
		case model.AuditStatusAttempt:
			require.FailNow(t, "policy left an incomplete audit attempt")
		}
		var result struct {
			Replayed        bool   `json:"idempotency_replayed"`
			OriginalAuditID string `json:"original_audit_event_id"`
		}
		if json.Unmarshal(event.Result, &result) == nil && result.Replayed && model.AuditEventID(result.OriginalAuditID).IsValid() {
			replayAudited = true
		}
	}
	require.GreaterOrEqual(t, success, 2, "policy audit success/failure/replay")
	require.GreaterOrEqual(t, failed, 1, "policy audit success/failure/replay")
	require.True(t, replayAudited, "policy audit success/failure/replay")

	// Drive the separate reviewed cleanup workflow through both real HTTP graphs.
	commandRequest := func(handler http.Handler, method, path, key, token string, body any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	decodeControl := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		require.Equal(t, http.StatusOK, response.Code, "control: %d %s", response.Code, response.Body.String())
		require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "control: %d %s", response.Code, response.Body.String())
		var result map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		return result
	}
	preview := commandRequest(primary.Handler(), http.MethodPost, "/api/v1/retention/previews", "review-retention", login.Tokens.AccessToken, map[string]any{"expected_revision": 2})
	require.Equal(t, http.StatusCreated, preview.Code, "preview: %d %s", preview.Code, preview.Body.String())
	var previewBody struct {
		ID             string `json:"id"`
		PolicyRevision int64  `json:"policy_revision"`
	}
	require.NoError(t, json.Unmarshal(preview.Body.Bytes(), &previewBody))
	require.True(t, model.RetentionPreviewID(previewBody.ID).IsValid(), "preview lost exact policy")
	require.Equal(t, int64(2), previewBody.PolicyRevision, "preview lost exact policy")
	previewRead := commandRequest(secondary.Handler(), http.MethodGet, "/api/v1/retention/previews/"+previewBody.ID, "", login.Tokens.AccessToken, nil)
	require.Equal(t, 200, previewRead.Code, "review was not visible on peer")
	require.Equal(t, preview.Body.String(), previewRead.Body.String(), "review was not visible on peer")
	controlBefore := decodeControl(commandRequest(secondary.Handler(), http.MethodGet, "/api/v1/retention/control", "", login.Tokens.AccessToken, nil))
	enableBody := map[string]any{"expected_revision": controlBefore["revision"], "expected_policy_revision": 2, "preview_id": previewBody.ID, "state": "enabled"}
	enabled := commandRequest(primary.Handler(), http.MethodPut, "/api/v1/retention/control", "approve-retention", login.Tokens.AccessToken, enableBody)
	enabledBody := decodeControl(enabled)
	replayEnabled := commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention/control", "approve-retention", login.Tokens.AccessToken, enableBody)
	require.Equal(t, 200, replayEnabled.Code, "cleanup replay changed reviewed approval")
	require.Equal(t, replayEnabled.Body.String(), enabled.Body.String(), "cleanup replay changed reviewed approval")
	activePolicy := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	require.Equal(t, 200, activePolicy.Code, "approval projection=%d %s", activePolicy.Code, activePolicy.Body.String())
	require.True(t, bytes.Contains(activePolicy.Body.Bytes(), []byte(`"automatic_deletion_enabled":true`)), "approval projection=%d %s", activePolicy.Code, activePolicy.Body.String())
	for _, days := range []int{-1, 8, 36500} {
		invalid := commandRequest(primary.Handler(), http.MethodPut, "/api/v1/retention-policy", "invalid-export-"+strconv.Itoa(days), login.Tokens.AccessToken,
			map[string]any{"expected_revision": 2, "submission_retention_days": 730, "integrity_retention_days": 365, "browser_activity_retention_days": 0, "security_operational_retention_days": 0,
				"audit_retention_days": 0, "export_retention_days": days, "deletion_grace_days": 30})
		require.Equal(t, http.StatusBadRequest, invalid.Code, "invalid export period %d: %d %s", days, invalid.Code, invalid.Body.String())
		require.True(t, bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"retention_policy.invalid"`)), "invalid export period %d: %d %s", days, invalid.Code, invalid.Body.String())
		unchanged := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
		require.Equal(t, http.StatusOK, unchanged.Code, "invalid export period changed policy or approval")
		require.Equal(t, activePolicy.Body.String(), unchanged.Body.String(), "invalid export period changed policy or approval")
	}
	paused := decodeControl(commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention/control", "pause-retention", login.Tokens.AccessToken, map[string]any{"expected_revision": enabledBody["revision"], "expected_policy_revision": 2, "state": "paused"}))
	require.Equal(t, "paused", paused["state"], "pause did not commit")
	pausedPolicy := request(primary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	require.Equal(t, 200, pausedPolicy.Code, "peer retained cleanup approval after pause")
	require.True(t, bytes.Contains(pausedPolicy.Body.Bytes(), []byte(`"automatic_deletion_enabled":false`)), "peer retained cleanup approval after pause")
	forbiddenPreview := commandRequest(secondary.Handler(), http.MethodPost, "/api/v1/retention/previews", "ordinary-review", ordinaryLogin.Tokens.AccessToken, map[string]any{"expected_revision": 2})
	require.Equal(t, 403, forbiddenPreview.Code, "ordinary account could preview institution records")
	ownNotices := commandRequest(primary.Handler(), http.MethodGet, "/api/v1/users/me/retention-notices", "", ordinaryLogin.Tokens.AccessToken, nil)
	require.Equal(t, 200, ownNotices.Code, "own notices depend on administrator authority: %d %s", ownNotices.Code, ownNotices.Body.String())
	require.True(t, bytes.Contains(ownNotices.Body.Bytes(), []byte(`"items":[]`)), "own notices depend on administrator authority: %d %s", ownNotices.Code, ownNotices.Body.String())
	for index, days := range []int{0, 1, 7} {
		accepted := commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention-policy", "export-boundary-"+strconv.Itoa(days), login.Tokens.AccessToken,
			map[string]any{"expected_revision": 2 + index, "submission_retention_days": 730, "integrity_retention_days": 365, "browser_activity_retention_days": 0, "security_operational_retention_days": 0,
				"audit_retention_days": 0, "export_retention_days": days, "deletion_grace_days": 30})
		require.Equal(t, http.StatusOK, accepted.Code, "valid export period %d: %d %s", days, accepted.Code, accepted.Body.String())
		var policy struct {
			ExportRetentionDays int `json:"export_retention_days"`
		}
		require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &policy), "valid export period changed: %#v", policy)
		require.Equal(t, days, policy.ExportRetentionDays, "valid export period changed: %#v", policy)
	}
}
