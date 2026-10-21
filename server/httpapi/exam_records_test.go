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
	"github.com/sudosylabs/proctor/server/model"
)

type recordsHTTPApplication struct {
	ExamRecordsApplication
	calls    int
	complete app.CompleteExamSittingRecordsCommand
	create   app.CreateRetentionHoldCommand
	query    app.ListRetentionHoldsQuery
	page     *app.RetentionHoldPage
}

func (a *recordsHTTPApplication) CompleteExamSittingRecords(_ context.Context, call app.Invocation, command app.CompleteExamSittingRecordsCommand) (*model.ExamSittingRecordsCompletion, error) {
	a.calls++
	a.complete = command
	c := model.NewExamSittingRecordsCompletion(command.Scope.SittingID)
	_, err := c.Complete(command.ExpectedRevision, command.AcknowledgedEvidenceRevision, call.Principal().UserID, time.Now())
	return c, err
}
func (a *recordsHTTPApplication) CreateRetentionHold(_ context.Context, call app.Invocation, command app.CreateRetentionHoldCommand) (*model.RetentionHold, error) {
	a.calls++
	a.create = command
	return &model.RetentionHold{ID: model.NewRetentionHoldID(), Scope: command.Scope, Revision: 1, CreatedAt: time.Now(), CreatedByUserID: call.Principal().UserID,
		ReasonCode: command.ReasonCode, PrivateReason: command.PrivateReason, WorkRetiredSubmissionCount: 1, IntegrityRetiredSubmissionCount: 2}, nil
}
func (a *recordsHTTPApplication) ListRetentionHolds(_ context.Context, _ app.Invocation, query app.ListRetentionHoldsQuery) (*app.RetentionHoldPage, error) {
	a.calls++
	a.query = query
	return a.page, nil
}

func recordsHTTPPrincipal() model.Principal {
	return model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()), CredentialType: model.CredentialSessionAccess,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor, AuthenticatedAt: time.Now(), ClientType: model.SessionClientWeb}
}

func TestExamRecordsHTTPRequiresExplicitAcknowledgementAndIdempotency(t *testing.T) {
	logger, _ := newTestLogger(t)
	application := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(application))
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
		require.Equal(t, http.StatusBadRequest, response.Code, "invalid completion %s = %d %s", body, response.Code, response.Body.String())
		require.Equal(t, 0, application.calls, "invalid completion %s = %d %s", body, response.Code, response.Body.String())
	}
	response := serve(`{"expected_revision":1,"acknowledged_evidence_revision":0}`, "")
	require.Equal(t, http.StatusBadRequest, response.Code, "missing key = %d %s", response.Code, response.Body.String())
	require.Equal(t, 0, application.calls, "missing key = %d %s", response.Code, response.Body.String())
	response = serve(`{"expected_revision":1,"acknowledged_evidence_revision":0}`, "completion-key")
	require.Equal(t, http.StatusOK, response.Code, "completion = %d %s, command=%#v", response.Code, response.Body.String(), application.complete)
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "completion = %d %s, command=%#v", response.Code, response.Body.String(), application.complete)
	require.Equal(t, model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}, application.complete.Scope, "completion = %d %s, command=%#v", response.Code, response.Body.String(), application.complete)
	require.Equal(t, "completion-key", application.complete.IdempotencyKey, "completion = %d %s, command=%#v", response.Code, response.Body.String(), application.complete)
	var value examRecordsCompletionResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &value))
	require.True(t, value.Current)
	require.Equal(t, int64(2), value.Revision)
	require.NotEmpty(t, value.CompletedAt)
	require.NotEmpty(t, value.CompletedByUserID)
}

func TestExamRecordsHTTPHoldScopeAndPartialRetirementDisclosure(t *testing.T) {
	logger, _ := newTestLogger(t)
	application := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(application))
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
		require.Equal(t, http.StatusBadRequest, response.Code, "invalid hold = %d %s", response.Code, response.Body.String())
		require.Equal(t, 0, application.calls, "invalid hold = %d %s", response.Code, response.Body.String())
	}
	response := serve(`{"reason_code":"institution_request","private_reason":"Preserve the current institutional record."}`)
	require.Equal(t, http.StatusCreated, response.Code, "hold = %d %s", response.Code, response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "hold = %d %s", response.Code, response.Body.String())
	require.Equal(t, model.RetentionHoldScope{ExamID: examID}, application.create.Scope, "hold = %d %s", response.Code, response.Body.String())
	var value retentionHoldResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &value))
	require.Equal(t, int64(1), value.WorkRetiredSubmissionCount)
	require.Equal(t, int64(2), value.IntegrityRetiredSubmissionCount)
	require.Equal(t, application.create.PrivateReason, value.PrivateReason)
}

func TestExamRecordsHTTPCursorBindsScopeAndReleaseFilter(t *testing.T) {
	examID, sittingID := model.NewExamID(), model.NewExamSittingID()
	scope := model.RetentionHoldScope{ExamID: examID, SittingID: sittingID}
	hold := model.RetentionHold{ID: model.NewRetentionHoldID(), Scope: scope, Revision: 1, CreatedAt: time.Now(), CreatedByUserID: model.NewUserID(), ReasonCode: "other", PrivateReason: "Preserve."}
	logger, _ := newTestLogger(t)
	application := &recordsHTTPApplication{page: &app.RetentionHoldPage{Holds: []model.RetentionHold{hold}, HasMore: true}}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(application))
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
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Equal(t, http.StatusOK, response.Code, "page = %d %s", response.Code, response.Body.String())
	require.NotEmpty(t, page.NextCursor, "page = %d %s", response.Code, response.Body.String())
	require.Len(t, page.Items, 1, "page = %d %s", response.Code, response.Body.String())
	application.page.HasMore = false
	response = serve(path + "&cursor=" + page.NextCursor)
	require.Equal(t, http.StatusOK, response.Code, "continuation = %d %s", response.Code, response.Body.String())
	require.Equal(t, hold.ID, application.query.After, "continuation = %d %s", response.Code, response.Body.String())
	for _, changed := range []string{path + "&include_released=true&cursor=" + page.NextCursor, strings.Replace(path, sittingID.String(), model.NewExamSittingID().String(), 1) + "&cursor=" + page.NextCursor, path + "&limit=2", path + "&include_released=1", path + "&unexpected=true"} {
		calls := application.calls
		response = serve(changed)
		require.Equal(t, http.StatusBadRequest, response.Code, "changed scope/filter = %d %s", response.Code, response.Body.String())
		require.Equal(t, calls, application.calls, "changed scope/filter = %d %s", response.Code, response.Body.String())
	}
}

func TestExamRecordsHTTPReleaseRequiresStrongRecentSession(t *testing.T) {
	logger, _ := newTestLogger(t)
	application := &recordsHTTPApplication{}
	api := newFocusedResourceAPI(t, logger, classRouteAuthenticator{principal: recordsHTTPPrincipal()}, examRecordsResource(application))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+model.NewExamID().String()+"/retention-holds/"+model.NewRetentionHoldID().String()+"/release", strings.NewReader(`{"expected_revision":1,"reason_code":"case_closed","private_reason":"Resolved."}`))
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "release-key")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code, "single-factor release")
	require.Equal(t, 0, application.calls, "single-factor release")
	require.Contains(t, response.Body.String(), "authentication.strong_required", "single-factor release")
}
