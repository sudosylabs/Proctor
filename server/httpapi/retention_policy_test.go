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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type retentionPolicyHTTPApplication struct {
	policy  *model.RetentionPolicy
	command app.ReplaceRetentionPolicyCommand
	calls   int
}

func (a *retentionPolicyHTTPApplication) GetRetentionPolicy(context.Context, invocation.Invocation) (*model.RetentionPolicy, error) {
	a.calls++
	return a.policy, nil
}

func (a *retentionPolicyHTTPApplication) ReplaceRetentionPolicy(_ context.Context, _ invocation.Invocation,
	command app.ReplaceRetentionPolicyCommand) (*model.RetentionPolicy, error) {
	a.calls++
	a.command = command
	return a.policy, nil
}

func TestRetentionPolicyHTTPRequiresCompleteSettingsAndNeverEnablesDeletion(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: time.Now(), MFACompletedAt: model.OptionalTimeFrom(time.Now()), ClientType: model.SessionClientWeb,
	}
	policy := model.NewInitialRetentionPolicy(model.NewInstitutionID(), time.Now())
	application := &retentionPolicyHTTPApplication{policy: policy}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: principal}, retentionPolicyResource(application))
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
	const valid = `{"expected_revision":1,"submission_retention_days":730,"integrity_retention_days":365,"browser_activity_retention_days":0,"security_operational_retention_days":0,"audit_retention_days":0,"export_retention_days":7,"deletion_grace_days":30}`
	response := serve(http.MethodPut, valid)
	require.Equal(t, http.StatusOK, response.Code, "replace: %d %s", response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "replace: %d %s", response.Code, response.Body.String())
	require.Equal(t, int64(1), application.command.ExpectedRevision)
	require.Equal(t, "retention-policy-1", application.command.IdempotencyKey)
	require.Equal(t, model.RetentionPolicySettings{SubmissionRetentionDays: 730, IntegrityRetentionDays: 365,
		ExportRetentionDays: 7, DeletionGraceDays: 30}, application.command.Settings)
	response = serve(http.MethodGet, "")
	var output map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &output))
	require.Equal(t, http.StatusOK, response.Code, "get: %d %s", response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "get: %d %s", response.Code, response.Body.String())
	require.Equal(t, false, output["automatic_deletion_enabled"], "get: %d %s", response.Code, response.Body.String())
	require.Equal(t, float64(1), output["revision"], "get: %d %s", response.Code, response.Body.String())
	for _, field := range []string{"created_at", "updated_at"} {
		value, ok := output[field].(string)
		require.True(t, ok, "missing time %s", field)
		_, err := time.Parse(time.RFC3339Nano, value)
		require.NoError(t, err)
	}
	for _, field := range []string{"submission_retention_days", "integrity_retention_days", "browser_activity_retention_days", "security_operational_retention_days", "audit_retention_days",
		"export_retention_days", "deletion_grace_days"} {
		for _, invalid := range []string{"missing", "null", "fraction", "string"} {
			t.Run(field+"/"+invalid, func(t *testing.T) {
				var body map[string]any
				require.NoError(t, json.Unmarshal([]byte(valid), &body))
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
				require.NoError(t, err)
				before := application.calls
				response := serve(http.MethodPut, string(encoded))
				require.Equal(t, http.StatusBadRequest, response.Code, "invalid settings reached application: %d %s", response.Code, response.Body.String())
				require.Equal(t, before, application.calls, "invalid settings reached application: %d %s", response.Code, response.Body.String())
			})
		}
	}
	before := application.calls
	response = serve(http.MethodPut, strings.TrimSuffix(valid, "}")+`,"automatic_deletion_enabled":true}`)
	require.Equal(t, http.StatusBadRequest, response.Code, "deletion enable attempt reached application: %d %s", response.Code, response.Body.String())
	require.Equal(t, before, application.calls, "deletion enable attempt reached application: %d %s", response.Code, response.Body.String())
}
