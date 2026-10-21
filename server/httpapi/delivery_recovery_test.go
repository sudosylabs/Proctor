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
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

// testDeliveryRecoveryCause mirrors the Attempt module's private recovery
// carrier: transports discover recovery state only through the cause chain.
type testDeliveryRecoveryCause struct {
	recovery *model.DeliveryRecovery
	cause    error
}

func (c testDeliveryRecoveryCause) Error() string                             { return c.cause.Error() }
func (c testDeliveryRecoveryCause) Unwrap() error                             { return c.cause }
func (c testDeliveryRecoveryCause) DeliveryRecovery() *model.DeliveryRecovery { return c.recovery }

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
			fault := testDeliveryRecoveryCause{recovery: state, cause: errors.New("private-secret-and-record-payload")}
			response := httptest.NewRecorder()
			WriteError(response, httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/example", nil), apperror.New(apperror.Code(code)).Wrap(fault))
			var result map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			_, present := result["delivery"]
			require.Equal(t, name == "current", present)
			require.NotContains(t, response.Body.String(), "private-secret")
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			if code == "exam.delivery.pending_capacity" {
				require.Equal(t, 429, response.Code, "retry contract missing")
				require.Equal(t, "1", response.Header().Get("Retry-After"), "retry contract missing")
			}
		})
	}
}

func TestControlRateLimitProblemHasRetryAdviceAndNoPrivateSnapshot(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/example", nil), apperror.New("exam.delivery.control_rate_limited"))
	require.Equal(t, 429, response.Code, "control retry response")
	require.Equal(t, "1", response.Header().Get("Retry-After"), "control retry response")
	require.NotContains(t, response.Body.String(), `"delivery":`, "control retry response")
}
