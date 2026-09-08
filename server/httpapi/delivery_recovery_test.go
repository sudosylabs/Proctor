// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
)

func TestDeliveryRecoveryProblemConcealsInvalidAndUnrelatedState(t *testing.T) {
	for _, name := range []string{"current", "invalid", "unrelated"} {
		t.Run(name, func(t *testing.T) {
			code := "exam.delivery.pending_capacity"
			state := &model.DeliveryRecovery{Family: "native", Budget: *testHTTPDeliveryBudget(model.NewAttemptParticipationID())}
			if name == "invalid" {
				state.Budget.Generation = 0
			}
			if name == "unrelated" {
				code = "exam.attempt.not_found"
			}
			fault := &attempt.Fault{Code: code, Recovery: state, Cause: errors.New("private-secret-and-record-payload")}
			response := httptest.NewRecorder()
			WriteError(response, httptest.NewRequest("POST", "/api/v1/example", nil), app.NewError(code).Wrap(fault))
			var result map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			_, present := result["delivery"]
			if present != (name == "current") || strings.Contains(response.Body.String(), "private-secret") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsafe problem: %s", response.Body.String())
			}
			if code == "exam.delivery.pending_capacity" && (response.Code != 429 || response.Header().Get("Retry-After") != "1") {
				t.Fatal("retry contract missing")
			}
		})
	}
}

func TestControlRateLimitProblemHasRetryAdviceAndNoPrivateSnapshot(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, httptest.NewRequest("GET", "/api/v1/example", nil), app.NewError("exam.delivery.control_rate_limited"))
	if response.Code != 429 || response.Header().Get("Retry-After") != "1" || strings.Contains(response.Body.String(), `"delivery":`) {
		t.Fatalf("control retry response=%d %s", response.Code, response.Body.String())
	}
}
