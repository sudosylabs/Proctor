// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamNativePolicyHTTPStrictBoundary(t *testing.T) {
	principal := testExamHTTPPrincipal()
	view := testExamHTTPView(t, principal.UserID)
	valid, err := json.Marshal(configureExamDraftNativePolicyRequest{ExpectedDraftRevision: 1, Native: model.DefaultNativeSecurityPolicy()})
	require.NoError(t, err)
	for name, body := range map[string]string{"valid": string(valid), "missing native": `{"expected_draft_revision":1}`, "null native": `{"expected_draft_revision":1,"native":null}`, "case alias": strings.Replace(string(valid), `"native":`, `"NATIVE":`, 1), "extra": strings.Replace(string(valid), `"native":`, `"extra":1,"native":`, 1), "missing boolean": strings.Replace(string(valid), `"observe_os_clipboard_changes":false,`, "", 1), "external capture enabled": strings.Replace(string(valid), `"id":"external_capture","mode":"disabled"`, `"id":"external_capture","mode":"enforce"`, 1)} {
		t.Run(name, func(t *testing.T) {
			logger, _ := newTestLogger(t)
			fake := &examHTTPApplication{principal: principal, view: view}
			api := newFocusedResourceAPI(t, logger, fake, examResource(fake))
			request := httptest.NewRequest(http.MethodPut, "/api/v1/exams/"+view.Exam.ID.String()+"/draft/policies/native", bytes.NewBufferString(body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "native-policy-key")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			want := http.StatusBadRequest
			if name == "valid" {
				want = http.StatusOK
			}
			require.Equal(t, want, response.Code, "status = %d: %s", response.Code, response.Body.String())
			require.Equal(t, name == "valid", fake.configureNativePolicy.ExamID.IsValid(), "invalid command crossed boundary")
		})
	}
}
