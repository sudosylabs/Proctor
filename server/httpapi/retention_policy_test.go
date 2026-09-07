// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type retentionPolicyHTTPApplication struct {
	policy  *model.RetentionPolicy
	command application.ReplaceRetentionPolicyCommand
	calls   int
}

func (a *retentionPolicyHTTPApplication) GetRetentionPolicy(context.Context, application.Invocation) (*model.RetentionPolicy, error) {
	a.calls++
	return a.policy, nil
}

func (a *retentionPolicyHTTPApplication) ReplaceRetentionPolicy(_ context.Context, _ application.Invocation,
	command application.ReplaceRetentionPolicyCommand) (*model.RetentionPolicy, error) {
	a.calls++
	a.command = command
	return a.policy, nil
}

func TestRetentionPolicyHTTPRequiresCompleteSettingsAndNeverEnablesDeletion(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb,
	}
	policy := model.NewInitialRetentionPolicy(model.NewInstitutionID(), time.Now())
	app := &retentionPolicyHTTPApplication{policy: policy}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, retentionPolicyResource(app))
	serve := func(method, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v1/retention-policy", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		if method == http.MethodPut {
			request.Header.Set("Idempotency-Key", "retention-policy-1")
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	const valid = `{"expected_revision":1,"submission_retention_days":730,"integrity_retention_days":365,"audit_retention_days":0,"export_retention_days":7,"deletion_grace_days":30}`
	response := serve(http.MethodPut, valid)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("replace: %d %s", response.Code, response.Body.String())
	}
	if app.command.ExpectedRevision != 1 || app.command.IdempotencyKey != "retention-policy-1" ||
		app.command.Settings != (model.RetentionPolicySettings{SubmissionRetentionDays: 730, IntegrityRetentionDays: 365,
			ExportRetentionDays: 7, DeletionGraceDays: 30}) {
		t.Fatalf("command = %#v", app.command)
	}
	response = serve(http.MethodGet, "")
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" ||
		output["automatic_deletion_enabled"] != false || output["revision"] != float64(1) {
		t.Fatalf("get: %d %s", response.Code, response.Body.String())
	}
	for _, field := range []string{"created_at", "updated_at"} {
		value, ok := output[field].(string)
		if !ok {
			t.Fatalf("missing time %s", field)
		}
		if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"submission_retention_days", "integrity_retention_days", "audit_retention_days",
		"export_retention_days", "deletion_grace_days"} {
		for _, invalid := range []string{"missing", "null", "fraction", "string"} {
			t.Run(field+"/"+invalid, func(t *testing.T) {
				var body map[string]any
				if err := json.Unmarshal([]byte(valid), &body); err != nil {
					t.Fatal(err)
				}
				switch invalid {
				case "missing":
					delete(body, field)
				case "null":
					body[field] = nil
				case "fraction":
					body[field] = 1.5
				case "string":
					body[field] = "30"
				}
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				before := app.calls
				response := serve(http.MethodPut, string(encoded))
				if response.Code != http.StatusBadRequest || app.calls != before {
					t.Fatalf("invalid settings reached application: %d %s", response.Code, response.Body.String())
				}
			})
		}
	}
	before := app.calls
	response = serve(http.MethodPut, strings.TrimSuffix(valid, "}")+`,"automatic_deletion_enabled":true}`)
	if response.Code != http.StatusBadRequest || app.calls != before {
		t.Fatalf("deletion enable attempt reached application: %d %s", response.Code, response.Body.String())
	}
}
