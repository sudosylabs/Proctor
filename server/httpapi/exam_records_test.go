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

type recordsHTTPApplication struct {
	ExamRecordsApplication
	calls    int
	complete application.CompleteExamSittingRecordsCommand
	create   application.CreateRetentionHoldCommand
	query    application.ListRetentionHoldsQuery
	page     *application.RetentionHoldPage
}

func (a *recordsHTTPApplication) CompleteExamSittingRecords(_ context.Context, call application.Invocation, command application.CompleteExamSittingRecordsCommand) (*model.ExamSittingRecordsCompletion, error) {
	a.calls++
	a.complete = command
	c := model.NewExamSittingRecordsCompletion(command.Scope.SittingID)
	_, err := c.Complete(command.ExpectedRevision, command.AcknowledgedEvidenceRevision, call.Principal().UserID, time.Now())
	return c, err
}
func (a *recordsHTTPApplication) CreateRetentionHold(_ context.Context, call application.Invocation, command application.CreateRetentionHoldCommand) (*model.RetentionHold, error) {
	a.calls++
	a.create = command
	return &model.RetentionHold{ID: model.NewRetentionHoldID(), Scope: command.Scope, Revision: 1, CreatedAt: time.Now(), CreatedByUserID: call.Principal().UserID,
		ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason, WorkRetiredSubmissionCount: 1, IntegrityRetiredSubmissionCount: 2}, nil
}
func (a *recordsHTTPApplication) ListRetentionHolds(_ context.Context, _ application.Invocation, query application.ListRetentionHoldsQuery) (*application.RetentionHoldPage, error) {
	a.calls++
	a.query = query
	return a.page, nil
}

func recordsHTTPPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: time.Now(), ClientType: model.SessionClientWeb}
}

func TestExamRecordsHTTPRequiresExplicitAcknowledgementAndIdempotency(t *testing.T) {
	logger, _ := newTestLogger(t)
	app := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(app))
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	path := "/api/v1/exams/" + examID.String() + "/sittings/" + sittingID.String() + "/records-completion"
	serve := func(body, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	for _, body := range []string{`{}`, `{"expected_revision":1}`, `{"expected_revision":1,"acknowledged_evidence_revision":null}`, `{"expected_revision":1,"acknowledged_evidence_revision":0,"acknowledged_evidence_revision":2}`, `{"expected_revision":1,"acknowledged_evidence_revision":-1}`, `{"expected_revision":1,"acknowledged_evidence_revision":0,"unknown":true}`} {
		response := serve(body, "completion-key")
		if response.Code != http.StatusBadRequest || app.calls != 0 {
			t.Fatalf("invalid completion %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	response := serve(`{"expected_revision":1,"acknowledged_evidence_revision":0}`, "")
	if response.Code != http.StatusBadRequest || app.calls != 0 {
		t.Fatalf("missing key = %d %s", response.Code, response.Body.String())
	}
	response = serve(`{"expected_revision":1,"acknowledged_evidence_revision":0}`, "completion-key")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || app.complete.Scope != (model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}) || app.complete.IdempotencyKey != "completion-key" {
		t.Fatalf("completion = %d %s, command=%#v", response.Code, response.Body.String(), app.complete)
	}
	var value examRecordsCompletionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if !value.Current || value.Revision != 2 || value.CompletedAt == "" || value.CompletedByUserID == "" {
		t.Fatalf("completion projection = %#v", value)
	}
}

func TestExamRecordsHTTPHoldScopeAndPartialRetirementDisclosure(t *testing.T) {
	logger, _ := newTestLogger(t)
	app := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(app))
	examID := model.NewExamID()
	path := "/api/v1/exams/" + examID.String() + "/retention-holds"
	serve := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer session")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "hold-key")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	for _, body := range []string{`{"exam_sitting_id":null,"reason_code":"other","private_reason":"Preserve."}`, `{"submission_id":"` + model.NewSubmissionID().String() + `","reason_code":"other","private_reason":"Preserve."}`, `{"reason_code":"john_doe","private_reason":"Preserve."}`, `{"reason_code":"other","private_reason":"Preserve.","private_reason":"Changed."}`} {
		response := serve(body)
		if response.Code != http.StatusBadRequest || app.calls != 0 {
			t.Fatalf("invalid hold = %d %s", response.Code, response.Body.String())
		}
	}
	response := serve(`{"reason_code":"institution_request","private_reason":"Preserve the current institutional record."}`)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "private, no-store" || app.create.Scope != (model.RetentionHoldScope{ExamID: examID}) {
		t.Fatalf("hold = %d %s", response.Code, response.Body.String())
	}
	var value retentionHoldResponse
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.WorkRetiredSubmissionCount != 1 || value.IntegrityRetiredSubmissionCount != 2 || value.PrivateReason != app.create.PrivateReason {
		t.Fatalf("partial retirement disclosure = %#v", value)
	}
}

func TestExamRecordsHTTPCursorBindsScopeAndReleaseFilter(t *testing.T) {
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	scope := model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}
	hold := model.RetentionHold{ID: model.NewRetentionHoldID(), Scope: scope, Revision: 1, CreatedAt: time.Now(), CreatedByUserID: model.NewUserID(), ReasonCode: "other", PrivateReason: "Preserve."}
	logger, _ := newTestLogger(t)
	app := &recordsHTTPApplication{page: &application.RetentionHoldPage{Holds: []model.RetentionHold{hold}, HasMore: true}}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(app))
	path := "/api/v1/exams/" + examID.String() + "/retention-holds?exam_sitting_id=" + sittingID.String() + "&limit=1"
	serve := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer session")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	response := serve(path)
	var page retentionHoldPageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || page.NextCursor == "" || len(page.Items) != 1 {
		t.Fatalf("page = %d %s", response.Code, response.Body.String())
	}
	app.page.HasMore = false
	response = serve(path + "&cursor=" + page.NextCursor)
	if response.Code != http.StatusOK || app.query.After != hold.ID {
		t.Fatalf("continuation = %d %s", response.Code, response.Body.String())
	}
	for _, changed := range []string{path + "&include_released=true&cursor=" + page.NextCursor, strings.Replace(path, sittingID.String(), model.NewExamSittingID().String(), 1) + "&cursor=" + page.NextCursor, path + "&limit=2", path + "&include_released=1", path + "&unexpected=true"} {
		calls := app.calls
		response = serve(changed)
		if response.Code != http.StatusBadRequest || app.calls != calls {
			t.Fatalf("changed scope/filter = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestExamRecordsHTTPReleaseRequiresStrongRecentSession(t *testing.T) {
	logger, _ := newTestLogger(t)
	app := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(app))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+model.NewExamID().String()+"/retention-holds/"+model.NewRetentionHoldID().String()+"/release", strings.NewReader(`{"expected_revision":1,"reason_code":"case_closed","private_reason":"Resolved."}`))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "release-key")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || app.calls != 0 || !strings.Contains(response.Body.String(), "authentication.strong_required") {
		t.Fatalf("single-factor release = %d %s", response.Code, response.Body.String())
	}
}
