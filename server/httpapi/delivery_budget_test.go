// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package httpapi

import (
	"context"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type deliveryBudgetHTTPFake struct {
	*examAttemptHTTPFake
	query application.DeliveryBudgetQuery
	stop  application.StopDeliveryDetailsCommand
	calls int
}

func (f *deliveryBudgetHTTPFake) DeliveryBudget(_ context.Context, _ application.Invocation, q application.DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error) {
	f.query = q
	f.calls++
	return testHTTPDeliveryBudget(q.ParticipationID), nil
}
func (f *deliveryBudgetHTTPFake) StopDeliveryDetails(_ context.Context, _ application.Invocation, c application.StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error) {
	f.stop = c
	f.calls++
	value := &model.StopDeliveryDetailsResult{Budget: *testHTTPDeliveryBudget(f.participation.ID), Browser: []model.BrowserSourceStatus{}}
	value.Budget.Browser.Participation.SummaryOnly = true
	reason := model.DeliveryStopLocalLossInventory
	value.Budget.Browser.Participation.StopReason = &reason
	return value, nil
}
func testHTTPDeliveryBudget(part model.AttemptParticipationID) *model.DeliveryBudgetSnapshot {
	return &model.DeliveryBudgetSnapshot{ParticipationID: part, Generation: 1, Native: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(true, false), Attempt: model.NewDeliveryQuotaUsage(true, true), PendingByteLimit: model.DeliveryPendingByteLimit}, Browser: model.DeliveryFamilyBudget{Participation: model.NewDeliveryQuotaUsage(false, false), Attempt: model.NewDeliveryQuotaUsage(false, true), PendingByteLimit: model.DeliveryPendingByteLimit}, ServerTime: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
}
func TestDeliveryBudgetHTTPRequiresOwnedSelectorAndLiveStopFences(t *testing.T) {
	f := &deliveryBudgetHTTPFake{examAttemptHTTPFake: newExamAttemptHTTPFake(t)}
	logger, _ := newTestLogger(t)
	api := newFocusedResourceAPI(t, logger, f, deliveryBudgetResource(f))
	base := "/api/v1/exam-attempts/" + f.attempt.ID.String() + "/delivery-limits"
	request := f.candidateRequest(http.MethodGet, base+"?participation_id="+f.participation.ID.String())
	request.Header.Del(candidateAttemptCredentialHeader)
	request.Header.Del(candidateAttemptConnectionHeader)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || f.query.ParticipationID != f.participation.ID {
		t.Fatalf("budget=%d %s", response.Code, response.Body.String())
	}
	for _, bad := range []string{"", "?participation_id=invalid", "?participation_id=" + f.participation.ID.String() + "&participation_id=" + f.participation.ID.String()} {
		before := f.calls
		request = f.candidateRequest(http.MethodGet, base+bad)
		response = httptest.NewRecorder()
		api.ServeHTTP(response, request)
		if response.Code != 400 || f.calls != before {
			t.Fatal("invalid budget selector reached application")
		}
	}
	body := `{"family":"browser","reason":"local_loss_inventory_exhausted"}`
	for _, test := range []struct {
		name, body      string
		credential, key bool
		want            int
	}{
		{"valid", body, true, true, 200}, {"no Connection", body, false, true, 400}, {"no idempotency", body, true, false, 400}, {"unknown family", `{"family":"all","reason":"local_loss_inventory_exhausted"}`, true, true, 400}, {"invented reason", `{"family":"browser","reason":"reset_quota"}`, true, true, 400}, {"client-selected generation", `{"family":"browser","reason":"local_loss_inventory_exhausted","generation":2}`, true, true, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := f.calls
			request := f.candidateRequest(http.MethodPost, base+"/stop-details")
			request.Body = io.NopCloser(strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.key {
				request.Header.Set("Idempotency-Key", "stop-once")
			}
			if !test.credential {
				request.Header.Del(candidateAttemptConnectionHeader)
			}
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != test.want || test.want != 200 && f.calls != before {
				t.Fatalf("stop=%d %s", response.Code, response.Body.String())
			}
			if test.want == 200 && (f.stop.Access.ConnectionID != f.connection.ID || f.stop.Access.ContinuityCredential != f.credential || f.stop.IdempotencyKey != "stop-once") {
				t.Fatal("live fences or idempotency lost")
			}
		})
	}
}
