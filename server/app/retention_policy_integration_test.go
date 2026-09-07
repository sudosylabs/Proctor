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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib"
)

func TestRetentionPolicyHTTPAcrossNodesPreservesAuthorizationAndRetry(t *testing.T) {
	dataSource := os.Getenv("PROCTOR_TEST_DATABASE_URL")
	if dataSource == "" {
		t.Fatal("PROCTOR_TEST_DATABASE_URL is not set")
	}
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
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", bootstrap.Code, bootstrap.Body.String())
	}
	login, err := primary.App.Login(context.Background(), application.Invocation{}, application.LoginCommand{
		LoginID: "retention-admin", Password: "correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "retention-cli", Source: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"expected_revision":1,"submission_retention_days":730,"integrity_retention_days":365,"audit_retention_days":0,"export_retention_days":7,"deletion_grace_days":30}`
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
	if weak.Code != http.StatusForbidden || !bytes.Contains(weak.Body.Bytes(), []byte(`"code":"authentication.strong_required"`)) {
		t.Fatalf("weak replacement: %d %s", weak.Code, weak.Body.String())
	}
	strengthenRoleAdministratorSession(t, primary.Handler(), login.Tokens.AccessToken)
	first := request(primary.Handler(), http.MethodPut, "retention-change", login.Tokens.AccessToken)
	replay := request(secondary.Handler(), http.MethodPut, "retention-change", login.Tokens.AccessToken)
	read := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	for _, response := range []*httptest.ResponseRecorder{first, replay, read} {
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("policy response: %d %s", response.Code, response.Body.String())
		}
		var policy map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &policy); err != nil {
			t.Fatal(err)
		}
		if policy["revision"] != float64(2) || policy["submission_retention_days"] != float64(730) ||
			policy["automatic_deletion_enabled"] != false {
			t.Fatalf("policy: %#v", policy)
		}
	}
	if first.Body.String() != replay.Body.String() || first.Body.String() != read.Body.String() {
		t.Fatal("policy diverged across nodes or lost-response replay")
	}
	stale := request(secondary.Handler(), http.MethodPut, "stale-editor", login.Tokens.AccessToken)
	if stale.Code != http.StatusConflict || !bytes.Contains(stale.Body.Bytes(), []byte(`"code":"retention_policy.revision_conflict"`)) {
		t.Fatalf("stale replacement: %d %s", stale.Code, stale.Body.String())
	}
	ordinary := createIntegrationUser(t, primary, "retention-ordinary", "another correct horse battery staple")
	ordinaryLogin, err := secondary.App.Login(context.Background(), application.Invocation{}, application.LoginCommand{
		LoginID: ordinary.Username, Password: "another correct horse battery staple", ClientType: model.SessionClientCLI,
		DeviceID: "ordinary-cli", Source: "127.0.0.1:2",
	})
	if err != nil {
		t.Fatal(err)
	}
	denied := request(secondary.Handler(), http.MethodGet, "", ordinaryLogin.Tokens.AccessToken)
	if denied.Code != http.StatusForbidden || !bytes.Contains(denied.Body.Bytes(), []byte(`"code":"authorization.denied"`)) {
		t.Fatalf("ordinary read: %d %s", denied.Code, denied.Body.String())
	}
	audits, err := primaryStore.Audit().List(context.Background(), store.AuditListOptions{
		Action: string(model.ActionRetentionPolicyManage), Limit: 20,
		Visibility: store.AuditVisibilityScope{InstitutionWide: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	success, failed, replayAudited := 0, 0, false
	for _, event := range audits {
		switch event.Status {
		case model.AuditStatusSuccess:
			success++
		case model.AuditStatusFail:
			failed++
		case model.AuditStatusAttempt:
			t.Fatal("policy left an incomplete audit attempt")
		}
		var result struct {
			Replayed        bool   `json:"idempotency_replayed"`
			OriginalAuditID string `json:"original_audit_event_id"`
		}
		if json.Unmarshal(event.Result, &result) == nil && result.Replayed && model.IsValidId(result.OriginalAuditID) {
			replayAudited = true
		}
	}
	if success < 2 || failed < 1 || !replayAudited {
		t.Fatalf("policy audit success/failure/replay = %d/%d/%v", success, failed, replayAudited)
	}

	// Drive the separate reviewed cleanup workflow through both real HTTP graphs.
	commandRequest := func(handler http.Handler, method, path, key, token string, body any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
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
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("control: %d %s", response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	preview := commandRequest(primary.Handler(), http.MethodPost, "/api/v1/retention/previews", "review-retention", login.Tokens.AccessToken, map[string]any{"expected_policy_revision": 2})
	if preview.Code != http.StatusCreated {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var previewBody struct {
		ID             string `json:"id"`
		PolicyRevision int64  `json:"policy_revision"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil {
		t.Fatal(err)
	}
	if !model.IsValidId(previewBody.ID) || previewBody.PolicyRevision != 2 {
		t.Fatal("preview lost exact policy")
	}
	previewRead := commandRequest(secondary.Handler(), http.MethodGet, "/api/v1/retention/previews/"+previewBody.ID, "", login.Tokens.AccessToken, nil)
	if previewRead.Code != 200 || previewRead.Body.String() != preview.Body.String() {
		t.Fatal("review was not visible on peer")
	}
	controlBefore := decodeControl(commandRequest(secondary.Handler(), http.MethodGet, "/api/v1/retention/control", "", login.Tokens.AccessToken, nil))
	enableBody := map[string]any{"expected_revision": controlBefore["revision"], "expected_policy_revision": 2, "preview_id": previewBody.ID, "state": "enabled"}
	enabled := commandRequest(primary.Handler(), http.MethodPut, "/api/v1/retention/control", "approve-retention", login.Tokens.AccessToken, enableBody)
	enabledBody := decodeControl(enabled)
	replayEnabled := commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention/control", "approve-retention", login.Tokens.AccessToken, enableBody)
	if replayEnabled.Code != 200 || enabled.Body.String() != replayEnabled.Body.String() {
		t.Fatal("cleanup replay changed reviewed approval")
	}
	activePolicy := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	if activePolicy.Code != 200 || !bytes.Contains(activePolicy.Body.Bytes(), []byte(`"automatic_deletion_enabled":true`)) {
		t.Fatalf("approval projection=%d %s", activePolicy.Code, activePolicy.Body.String())
	}
	for _, days := range []int{-1, 8, 36500} {
		invalid := commandRequest(primary.Handler(), http.MethodPut, "/api/v1/retention-policy", "invalid-export-"+strconv.Itoa(days), login.Tokens.AccessToken,
			map[string]any{"expected_revision": 2, "submission_retention_days": 730, "integrity_retention_days": 365,
				"audit_retention_days": 0, "export_retention_days": days, "deletion_grace_days": 30})
		if invalid.Code != http.StatusBadRequest || !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"retention_policy.invalid"`)) {
			t.Fatalf("invalid export period %d: %d %s", days, invalid.Code, invalid.Body.String())
		}
		unchanged := request(secondary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
		if unchanged.Code != http.StatusOK || unchanged.Body.String() != activePolicy.Body.String() {
			t.Fatalf("invalid export period changed policy or approval: %d %s", unchanged.Code, unchanged.Body.String())
		}
	}
	paused := decodeControl(commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention/control", "pause-retention", login.Tokens.AccessToken, map[string]any{"expected_revision": enabledBody["revision"], "expected_policy_revision": 2, "state": "paused"}))
	if paused["state"] != "paused" {
		t.Fatal("pause did not commit")
	}
	pausedPolicy := request(primary.Handler(), http.MethodGet, "", login.Tokens.AccessToken)
	if pausedPolicy.Code != 200 || !bytes.Contains(pausedPolicy.Body.Bytes(), []byte(`"automatic_deletion_enabled":false`)) {
		t.Fatal("peer retained cleanup approval after pause")
	}
	forbiddenPreview := commandRequest(secondary.Handler(), http.MethodPost, "/api/v1/retention/previews", "ordinary-review", ordinaryLogin.Tokens.AccessToken, map[string]any{"expected_policy_revision": 2})
	if forbiddenPreview.Code != 403 {
		t.Fatal("ordinary account could preview institution records")
	}
	ownNotices := commandRequest(primary.Handler(), http.MethodGet, "/api/v1/users/me/retention-notices", "", ordinaryLogin.Tokens.AccessToken, nil)
	if ownNotices.Code != 200 || !bytes.Contains(ownNotices.Body.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("own notices depend on administrator authority: %d %s", ownNotices.Code, ownNotices.Body.String())
	}
	for index, days := range []int{0, 1, 7} {
		accepted := commandRequest(secondary.Handler(), http.MethodPut, "/api/v1/retention-policy", "export-boundary-"+strconv.Itoa(days), login.Tokens.AccessToken,
			map[string]any{"expected_revision": 2 + index, "submission_retention_days": 730, "integrity_retention_days": 365,
				"audit_retention_days": 0, "export_retention_days": days, "deletion_grace_days": 30})
		if accepted.Code != http.StatusOK {
			t.Fatalf("valid export period %d: %d %s", days, accepted.Code, accepted.Body.String())
		}
		var policy struct {
			ExportRetentionDays int `json:"export_retention_days"`
		}
		if err := json.Unmarshal(accepted.Body.Bytes(), &policy); err != nil || policy.ExportRetentionDays != days {
			t.Fatalf("valid export period changed: %#v, %v", policy, err)
		}
	}
}
