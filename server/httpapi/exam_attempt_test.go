// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCandidateExamPresentationRequiresBoundHeadersAndNeverEchoesSecrets(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	request := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/presentation")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status/cache = %d/%q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/cache = %d/%q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, fake.attempt.ID, fake.presentationAccess.AttemptID)
	require.Equal(t, fake.connection.ID, fake.presentationAccess.ConnectionID)
	require.Equal(t, fake.credential, fake.presentationAccess.ContinuityCredential)
	require.NotContains(t, response.Body.String(), fake.credential)
	require.NotContains(t, response.Body.String(), "credential")
	require.NotContains(t, response.Body.String(), "file_revision")
	require.NotContains(t, response.Body.String(), "object_id")
	var payload map[string]json.RawMessage
	var capabilities candidateRuntimeCapabilitiesResponse
	var capacity examCapacityPolicyResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "presentation shape")
	require.Len(t, payload, 9, "presentation shape")
	require.NoError(t, json.Unmarshal(payload["capacity"], &capacity), "presentation capacity")
	require.Equal(t, examCapacityPolicyResponseFromModel(fake.presentation.Capacity), capacity, "presentation capacity")
	require.NoError(t, json.Unmarshal(payload["candidate_runtime_capabilities"], &capabilities), "presentation capabilities = %#v", capabilities)
	require.True(t, capabilities.FocusLossCollectionEnabled, "presentation capabilities = %#v", capabilities)
	require.Equal(t, 1, capabilities.SchemaVersion, "presentation capabilities = %#v", capabilities)
	require.False(t, capabilities.Departure.Allowed, "presentation capabilities = %#v", capabilities)
	require.Equal(t, "attempt_in_progress", capabilities.Departure.Reason, "presentation capabilities = %#v", capabilities)
}

func TestCandidateExamHTTPRejectsMissingOrDuplicateProtectionHeadersBeforeApplication(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	for _, mutate := range []func(*http.Request){
		func(request *http.Request) { request.Header.Del(candidateAttemptCredentialHeader) },
		func(request *http.Request) {
			request.Header.Add(candidateAttemptConnectionHeader, fake.connection.ID.String())
		},
	} {
		request := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/presentation")
		mutate(request)
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, "status/calls/cache = %d/%d/%q: %s", response.Code, fake.presentationCalls, response.Header().Get("Cache-Control"), response.Body.String())
		require.Equal(t, 0, fake.presentationCalls, "status/calls/cache = %d/%d/%q: %s", response.Code, fake.presentationCalls, response.Header().Get("Cache-Control"), response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/calls/cache = %d/%d/%q: %s", response.Code, fake.presentationCalls, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func TestCandidateExamWorkspaceUsesBoundedOpaqueCursor(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	fake.workspacePage.HasMore = true
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	request := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/workspace?limit=1")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status/query/cache = %d/%#v/%q: %s", response.Code, fake.workspaceQuery, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, 1, fake.workspaceQuery.Limit, "status/query/cache = %d/%#v/%q: %s", response.Code, fake.workspaceQuery, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/query/cache = %d/%#v/%q: %s", response.Code, fake.workspaceQuery, response.Header().Get("Cache-Control"), response.Body.String())
	var page candidateExamWorkspaceListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "Workspace page = %#v", page)
	require.Len(t, page.Items, 1, "Workspace page = %#v", page)
	require.NotEmpty(t, page.NextCursor, "Workspace page = %#v", page)
	require.NotNil(t, page.Items[0].Size, "Workspace page = %#v", page)
	cursor, err := decodeCandidateWorkspaceCursor(page.NextCursor)
	require.NoError(t, err, "Workspace cursor = %#v", cursor)
	require.Equal(t, fake.workspacePage.Cursor, cursor.ExpectedCursor, "Workspace cursor = %#v", cursor)
	require.Equal(t, fake.workspacePage.Items[0].EntryID, cursor.ID, "Workspace cursor = %#v", cursor)
	require.Equal(t, int64(-1), fake.workspaceQuery.ExpectedCursor, "Workspace cursor = %#v", cursor)
	fake.workspacePage.HasMore = false
	second := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/workspace?cursor="+url.QueryEscape(page.NextCursor))
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	require.Equal(t, http.StatusOK, secondResponse.Code, "Workspace cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.workspaceQuery, secondResponse.Body.String())
	require.Equal(t, cursor.ExpectedCursor, fake.workspaceQuery.ExpectedCursor, "Workspace cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.workspaceQuery, secondResponse.Body.String())
	require.Equal(t, cursor.ID, fake.workspaceQuery.AfterEntryID, "Workspace cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.workspaceQuery, secondResponse.Body.String())
	malformed := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/workspace?cursor=not-a-cursor")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
}

func TestCandidateSubmitExamAttemptUsesStrictCausalSelectorsAndReturnsSafeReceipt(t *testing.T) {
	t.Parallel()

	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	body := `{"participation_id":"` + fake.participation.ID.String() + `","generation":1,"expected_current_revision_id":"` +
		fake.submissionReceipt.ExamRevisionID.String() + `","expected_workspace_cursor":4,"final_focus_loss_sequence":0}`
	path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/submissions"
	request := fake.candidateRequest(http.MethodPost, path)
	request.Body = io.NopCloser(strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "submit-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, fake.attempt.ID, fake.submit.Access.AttemptID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, fake.participation.ID, fake.submit.Access.ParticipationID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, int64(1), fake.submit.Access.Generation, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, fake.submissionReceipt.ExamRevisionID, fake.submit.ExpectedCurrentRevisionID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, int64(4), fake.submit.ExpectedWorkspaceCursor, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, int64(0), fake.submit.FinalFocusLossSequence, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	require.Equal(t, "submit-once", fake.submit.IdempotencyKey, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "receipt")
	require.Len(t, payload, 8, "receipt")
	require.Equal(t, `"`+fake.submissionReceipt.ExamRevisionID.String()+`"`, string(payload["exam_revision_id"]), "receipt")
	require.Equal(t, `"submitted"`, string(payload["state"]), "receipt")
	require.Equal(t, "4", string(payload["workspace_cursor"]), "receipt")
	assertNoForbiddenJSONTerms(t, response.Body.Bytes(),
		"participation", "generation", "connection", "credential", "integrity", "gap", "path", "content", "evidence")
}

func TestCandidateAcknowledgesExactCorrectionWithBoundSessionFences(t *testing.T) {
	t.Parallel()

	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	correctionID := model.NewExamRevisionID()
	currentRevisionID := model.NewExamRevisionID()
	path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/corrections/" + correctionID.String() + "/acknowledgement"
	body := `{"participation_id":"` + fake.participation.ID.String() + `","generation":1,"expected_current_revision_id":"` + currentRevisionID.String() + `"}`
	request := fake.candidateRequest(http.MethodPut, path)
	request.Body = io.NopCloser(strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "acknowledge-correction-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	command := fake.correctionAcknowledgement
	require.Equal(t, http.StatusOK, response.Code, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, fake.attempt.ID, command.Access.AttemptID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, fake.connection.ID, command.Access.ConnectionID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, fake.credential, command.Access.ContinuityCredential, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, fake.participation.ID, command.Access.ParticipationID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, int64(1), command.Access.Generation, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, correctionID, command.CorrectionRevisionID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, currentRevisionID, command.ExpectedCurrentRevisionID, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	require.Equal(t, "acknowledge-correction-once", command.IdempotencyKey, "status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "acknowledgement response")
	require.Len(t, payload, 5, "acknowledgement response")
	require.Equal(t, `"`+correctionID.String()+`"`, string(payload["revision_id"]), "acknowledgement response")
	require.Equal(t, `"acknowledged"`, string(payload["acknowledgement_state"]), "acknowledgement response")
	assertNoForbiddenJSONTerms(t, response.Body.Bytes(),
		"credential", "connection", "participation", "generation", "audit", "private")

	invalidFake := newExamAttemptHTTPFake(t)
	invalidAPI := newExamAttemptFocusedAPI(t, invalidFake)
	invalid := invalidFake.candidateRequest(http.MethodPut, "/api/v1/exam-attempts/"+invalidFake.attempt.ID.String()+
		"/corrections/"+model.NewExamRevisionID().String()+"/acknowledgement")
	invalid.Body = io.NopCloser(strings.NewReader(`{"participation_id":"` + invalidFake.participation.ID.String() +
		`","generation":1,"expected_current_revision_id":"` + model.NewExamRevisionID().String() + `","accepted":true}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("Idempotency-Key", "invalid-acknowledgement")
	invalidResponse := httptest.NewRecorder()
	invalidAPI.ServeHTTP(invalidResponse, invalid)
	assertHTTPProblem(t, invalidResponse, http.StatusBadRequest, "request.invalid")
	require.False(t, invalidFake.correctionAcknowledgement.CorrectionRevisionID.IsValid())
}

func TestSubmissionManifestCursorIsUnversionedStrictAndPathFree(t *testing.T) {
	t.Parallel()

	entryID := model.NewAttemptWorkspaceEntryID()
	raw, err := encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entryID})
	require.NoError(t, err)
	decoded, err := decodeSubmissionManifestCursor(raw)
	require.NoError(t, err, "decoded cursor = %#v", decoded)
	require.Equal(t, entryID, decoded.EntryID, "decoded cursor = %#v", decoded)
	require.NotContains(t, raw, "src")
	require.NotContains(t, raw, "main.go")
	versioned := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"after_entry_id":"` + entryID.String() + `"}`))
	_, err = decodeSubmissionManifestCursor(versioned)
	require.Error(t, err, "versioned cursor was accepted")
	unknown := base64.RawURLEncoding.EncodeToString([]byte(`{"after_entry_id":"` + entryID.String() + `","path":"src/main.go"}`))
	_, err = decodeSubmissionManifestCursor(unknown)
	require.Error(t, err, "cursor containing a path field was accepted")
}

func TestCandidateExamContentIsInlinePrivateNoStoreAndConditional(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/resources/" + fake.resourceID.String() + "/content"
	request := fake.candidateRequest(http.MethodGet, path)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	wantETag := `"` + fake.content.SHA256 + `"`
	require.Equal(t, http.StatusOK, response.Code, "content response = %d %#v", response.Code, response.Header())
	require.Equal(t, "protected", response.Body.String(), "content response = %d %#v", response.Code, response.Header())
	require.Empty(t, response.Header().Get("Content-Disposition"), "content response = %d %#v", response.Code, response.Header())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "content response = %d %#v", response.Code, response.Header())
	require.Equal(t, wantETag, response.Header().Get("ETag"), "content response = %d %#v", response.Code, response.Header())

	request = fake.candidateRequest(http.MethodGet, path)
	request.Header.Set("If-None-Match", wantETag)
	response = httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusNotModified, response.Code, "conditional response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	require.Equal(t, 0, response.Body.Len(), "conditional response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "conditional response = %d %#v %q", response.Code, response.Header(), response.Body.String())
}

func TestCandidateWorkspaceContentUsesContentVersionETagAndExactEntry(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	entryID := fake.workspacePage.Items[0].EntryID
	path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/workspace/files/" + entryID.String() + "/content"
	request := fake.candidateRequest(http.MethodGet, path)
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
	require.Equal(t, entryID, fake.workspaceOpen.EntryID, "Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
	require.True(t, fake.workspaceOpen.Access.ConnectionID.IsValid(), "Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
	require.Equal(t, `"`+fake.workspaceContentVersion.String()+`"`, response.Header().Get("ETag"), "Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
}

func TestManagedExamAttemptListIsBoundedSafeAndOpaque(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	fake.managerPage.HasMore = true
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() + "/attempts?limit=1&state=active"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "manager response/query = %d/%#v/%q: %s", response.Code, fake.managerList, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, 1, fake.managerList.Limit, "manager response/query = %d/%#v/%q: %s", response.Code, fake.managerList, response.Header().Get("Cache-Control"), response.Body.String())
	require.Len(t, fake.managerList.States, 1, "manager response/query = %d/%#v/%q: %s", response.Code, fake.managerList, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "manager response/query = %d/%#v/%q: %s", response.Code, fake.managerList, response.Header().Get("Cache-Control"), response.Body.String())
	require.NotContains(t, response.Body.String(), "credential")
	require.NotContains(t, response.Body.String(), "session_id")
	require.NotContains(t, response.Body.String(), "hash")
	var page examAttemptManagerListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "manager page = %#v", page)
	require.Len(t, page.Items, 1, "manager page = %#v", page)
	require.NotEmpty(t, page.NextCursor, "manager page = %#v", page)
	cursor, err := decodeExamAttemptManagerCursor(page.NextCursor)
	require.NoError(t, err)
	fake.managerPage.HasMore = false
	second := httptest.NewRequest(http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	require.Equal(t, http.StatusOK, secondResponse.Code, "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	require.True(t, fake.managerList.BeforeCreatedAt.Equal(cursor.CreatedAt), "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	require.Equal(t, cursor.ID, fake.managerList.BeforeAttemptID, "manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	malformed := httptest.NewRequest(http.MethodGet, path+"&cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")

	member := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() + "/attempts/" + fake.attempt.ID.String()
	request = httptest.NewRequest(http.MethodGet, member, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response = httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
	require.Equal(t, fake.attempt.ExamID, fake.managerGet.ExamID, "manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
	require.Equal(t, fake.attempt.SittingID, fake.managerGet.SittingID, "manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
	require.Equal(t, fake.attempt.ID, fake.managerGet.AttemptID, "manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
}

func TestManagedBrowserActivityIsNestedMinimizedNoStoreAndKeysetPaginated(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	fake.browserActivityPage.HasMore = true
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/browser-activity?limit=1"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "Browser Activity response/query = %d/%#v/%q: %s", response.Code, fake.browserActivityQuery, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, 1, fake.browserActivityQuery.Limit, "Browser Activity response/query = %d/%#v/%q: %s", response.Code, fake.browserActivityQuery, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "Browser Activity response/query = %d/%#v/%q: %s", response.Code, fake.browserActivityQuery, response.Header().Get("Cache-Control"), response.Body.String())
	var page browserActivityListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page), "Browser Activity page = %#v", page)
	require.Len(t, page.Items, 1, "Browser Activity page = %#v", page)
	require.NotEmpty(t, page.NextCursor, "Browser Activity page = %#v", page)
	require.Nil(t, page.Items[0].Location, "Browser Activity page = %#v", page)
	for _, forbidden := range []string{"participation_id", "connection_id", "registration_id", "credential", "review", "location_scheme"} {
		require.NotContains(t, response.Body.String(), forbidden, "Browser Activity response contains")
	}
	cursor, err := decodeBrowserActivityCursor(page.NextCursor)
	require.NoError(t, err)
	fake.browserActivityPage.HasMore = false
	second := httptest.NewRequest(http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	require.Equal(t, http.StatusOK, secondResponse.Code, "Browser Activity cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.browserActivityQuery, secondResponse.Body.String())
	require.True(t, fake.browserActivityQuery.AfterReceivedAt.Equal(cursor.ReceivedAt), "Browser Activity cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.browserActivityQuery, secondResponse.Body.String())
	require.Equal(t, cursor.SourceSessionID, fake.browserActivityQuery.AfterSourceID, "Browser Activity cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.browserActivityQuery, secondResponse.Body.String())
	require.Equal(t, cursor.Sequence, fake.browserActivityQuery.AfterSequence, "Browser Activity cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.browserActivityQuery, secondResponse.Body.String())
}

func TestManagerSubmissionReadManifestAndFileArePurposeSpecificAndProtected(t *testing.T) {
	t.Parallel()

	fake := newExamAttemptHTTPFake(t)
	fake.submissionManifest.HasMore = true
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	base := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/submissions/" + fake.submissionView.Submission.ID.String()
	for _, path := range []string{base, base + "/manifest?limit=1"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer credential")
		response := httptest.NewRecorder()
		httpAPI.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, "GET %s = %d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "GET %s = %d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		for _, forbidden := range []string{"starter_object", "attempt_object", "storage_origin", "vfs", "url"} {
			require.NotContains(t, strings.ToLower(response.Body.String()), forbidden, "GET %s exposed", path)
		}
	}
	require.Equal(t, fake.submissionView.Submission.ID, fake.submissionGet.SubmissionID, "get=%#v manifest=%#v", fake.submissionGet, fake.submissionManifestQuery)
	require.Equal(t, 1, fake.submissionManifestQuery.Limit, "get=%#v manifest=%#v", fake.submissionGet, fake.submissionManifestQuery)
	require.Equal(t, fake.submissionView.Submission.ID, fake.submissionManifestQuery.SubmissionID, "get=%#v manifest=%#v", fake.submissionGet, fake.submissionManifestQuery)
	entry := fake.submissionManifest.Items[0]
	cursor, err := encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entry.EntryID})
	require.NoError(t, err)
	fake.submissionManifest.HasMore = false
	cursorRequest := httptest.NewRequest(http.MethodGet, base+"/manifest?cursor="+url.QueryEscape(cursor), nil)
	cursorRequest.Header.Set("Authorization", "Bearer credential")
	cursorResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(cursorResponse, cursorRequest)
	require.Equal(t, http.StatusOK, cursorResponse.Code, "manifest cursor forwarding = %d query=%#v body=%s", cursorResponse.Code, fake.submissionManifestQuery, cursorResponse.Body.String())
	require.Equal(t, entry.EntryID, fake.submissionManifestQuery.AfterEntryID, "manifest cursor forwarding = %d query=%#v body=%s", cursorResponse.Code, fake.submissionManifestQuery, cursorResponse.Body.String())
	malformed := httptest.NewRequest(http.MethodGet, base+"/manifest?cursor=not-a-cursor", nil)
	malformed.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformed)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")

	contentPath := base + "/files/" + entry.EntryID.String() + "/content"
	request := httptest.NewRequest(http.MethodGet, contentPath, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
	require.Equal(t, "protected", response.Body.String(), "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
	require.Equal(t, `"`+entry.SHA256+`"`, response.Header().Get("ETag"), "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
	require.Empty(t, response.Header().Get("Content-Disposition"), "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
	require.Contains(t, response.Header().Values("Content-Security-Policy"), "sandbox", "authored content renders with an opaque origin")
	require.Equal(t, entry.EntryID, fake.submissionOpen.EntryID, "content=%d headers=%v query=%#v body", response.Code, response.Header(), fake.submissionOpen)
}

func TestManagerSubmissionIntegrityRetirementOmitsFormerCounters(t *testing.T) {
	t.Parallel()
	for _, retired := range []bool{false, true} {
		name := "retained zero counters"
		if retired {
			name = "retired integrity"
		}
		t.Run(name, func(t *testing.T) {
			fake := newExamAttemptHTTPFake(t)
			submission := &fake.submissionView.Submission
			submission.FinalFocusLossSequence = 0
			submission.UnresolvedIntegrityCount = 0
			submission.IntegrityState = model.SubmissionIntegritySettled
			if retired {
				submission.IntegrityState = model.SubmissionIntegrityRetired
				submission.IntegrityRetiredAt = model.OptionalTimeFrom(submission.SubmittedAt.Add(time.Hour))
				submission.BrowserActivity = model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}
			}
			httpAPI := newExamAttemptFocusedAPI(t, fake)
			path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
				"/attempts/" + fake.attempt.ID.String() + "/submissions/" + submission.ID.String()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer credential")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, "header read = %d: %s", response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "header read = %d: %s", response.Code, response.Body.String())
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
			for _, counter := range []string{"final_focus_loss_sequence", "unresolved_integrity_count"} {
				value, exists := fields[counter]
				require.NotEqual(t, retired, exists)
				require.False(t, exists && string(value) != "0")
			}
			_, hasRetirement := fields["integrity_retired_at"]
			require.Equal(t, retired, hasRetirement)
			require.Equal(t, `"`+string(submission.IntegrityState)+`"`, string(fields["integrity_state"]))
			require.Equal(t, `"`+submission.ManifestDigest+`"`, string(fields["manifest_digest"]))
			require.NotEmpty(t, fields["workspace_cursor"])
		})
	}
}

func TestCandidateAttemptHeadersRequireOneCanonicalSensitiveCredentialAndConnection(t *testing.T) {
	t.Parallel()
	credential := model.NewCredentialToken()
	connectionID := model.NewAttemptConnectionID()
	valid := httptest.NewRequest(http.MethodGet, "/", nil)
	valid.Header.Set(candidateAttemptCredentialHeader, credential)
	valid.Header.Set(candidateAttemptConnectionHeader, connectionID.String())
	access, err := candidateAttemptAccessHeaders(valid)
	require.NoError(t, err, "valid headers = %#v", access)
	require.Equal(t, credential, access.ContinuityCredential, "valid headers = %#v", access)
	require.Equal(t, connectionID, access.ConnectionID, "valid headers = %#v", access)

	tests := []struct {
		name string
		set  func(http.Header)
	}{
		{name: "missing credential", set: func(header http.Header) { header.Set(candidateAttemptConnectionHeader, connectionID.String()) }},
		{name: "invalid credential", set: func(header http.Header) {
			header.Set(candidateAttemptCredentialHeader, "short")
			header.Set(candidateAttemptConnectionHeader, connectionID.String())
		}},
		{name: "whitespace credential", set: func(header http.Header) {
			header.Set(candidateAttemptCredentialHeader, " "+credential)
			header.Set(candidateAttemptConnectionHeader, connectionID.String())
		}},
		{name: "duplicate credential", set: func(header http.Header) {
			header.Add(candidateAttemptCredentialHeader, credential)
			header.Add(candidateAttemptCredentialHeader, credential)
			header.Set(candidateAttemptConnectionHeader, connectionID.String())
		}},
		{name: "missing connection", set: func(header http.Header) { header.Set(candidateAttemptCredentialHeader, credential) }},
		{name: "invalid connection", set: func(header http.Header) {
			header.Set(candidateAttemptCredentialHeader, credential)
			header.Set(candidateAttemptConnectionHeader, "bad")
		}},
		{name: "duplicate connection", set: func(header http.Header) {
			header.Set(candidateAttemptCredentialHeader, credential)
			header.Add(candidateAttemptConnectionHeader, connectionID.String())
			header.Add(candidateAttemptConnectionHeader, connectionID.String())
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			test.set(request.Header)
			_, headerErr := candidateAttemptAccessHeaders(request)
			require.Error(t, headerErr, "invalid candidate Attempt headers were accepted")
		})
	}
}

func TestExamAttemptManagerCursorIsOpaqueStrictAndVersioned(t *testing.T) {
	t.Parallel()
	want := examAttemptManagerCursor{CreatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 123000000, time.UTC), ID: model.NewExamAttemptID()}
	encoded, err := encodeExamAttemptManagerCursor(want)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
	require.False(t, strings.ContainsAny(encoded, "+/="))
	got, err := decodeExamAttemptManagerCursor(encoded)
	require.NoError(t, err, "round trip")
	require.Equal(t, want, got, "round trip")
	for _, invalid := range []string{"", "%%%", encoded + "trailing"} {
		_, decodeErr := decodeExamAttemptManagerCursor(invalid)
		require.Error(t, decodeErr, "invalid cursor %q was accepted", invalid)
	}
}

func TestCandidateWorkspaceCursorContainsNoReversiblePath(t *testing.T) {
	t.Parallel()
	want := candidateWorkspaceCursor{ExpectedCursor: 7, ID: model.NewAttemptWorkspaceEntryID()}
	encoded, err := encodeCandidateWorkspaceCursor(want)
	require.NoError(t, err)
	got, err := decodeCandidateWorkspaceCursor(encoded)
	require.NoError(t, err, "round trip")
	require.Equal(t, want, got, "round trip")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	require.NoError(t, err, "cursor exposes Workspace path")
	require.NotContains(t, string(decoded), "cmd/main.go", "cursor exposes Workspace path")
	require.NotContains(t, string(decoded), `"path"`, "cursor exposes Workspace path")
	_, encodeErr := encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: -1, ID: want.ID})
	require.Error(t, encodeErr, "negative Workspace cursor encoded successfully")
	withoutEntry, err := encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: want.ExpectedCursor})
	require.NoError(t, err)
	decodedWithoutEntry, err := decodeCandidateWorkspaceCursor(withoutEntry)
	require.NoError(t, err, "snapshot-only Workspace cursor = %#v", decodedWithoutEntry)
	require.Equal(t, want.ExpectedCursor, decodedWithoutEntry.ExpectedCursor, "snapshot-only Workspace cursor = %#v", decodedWithoutEntry)
	require.False(t, decodedWithoutEntry.ID.IsValid(), "snapshot-only Workspace cursor = %#v", decodedWithoutEntry)
	for _, invalid := range []string{"", "%%%"} {
		_, decodeErr := decodeCandidateWorkspaceCursor(invalid)
		require.Error(t, decodeErr, "invalid cursor %q was accepted", invalid)
	}
}

func TestManagerReallowExamAttemptUsesExactSuspensionAndNeverReturnsPrivateReason(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	suspensionID := model.NewAttemptSuspensionID()
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/reallow"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"suspension_id":"`+suspensionID.String()+`","expected_revision":2,"reason":"manager verified connectivity"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "reallow-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, suspensionID, fake.reallow.SuspensionID)
	require.Equal(t, int64(2), fake.reallow.ExpectedAttemptRevision)
	require.Equal(t, "manager verified connectivity", fake.reallow.PrivateReason)
	require.Equal(t, "reallow-once", fake.reallow.IdempotencyKey)
	require.NotContains(t, response.Body.String(), "manager verified connectivity")
	require.NotContains(t, response.Body.String(), "private")
	require.NotContains(t, response.Body.String(), "replayed")
}

func TestManagerSuspendExamAttemptUsesRevisionFenceAndNeverReturnsPrivateReason(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/suspend"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":2,"reason":"candidate left the room"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "kick-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, fake.attempt.ID, fake.suspend.AttemptID)
	require.Equal(t, int64(2), fake.suspend.ExpectedAttemptRevision)
	require.Equal(t, "candidate left the room", fake.suspend.PrivateReason)
	require.Equal(t, "kick-once", fake.suspend.IdempotencyKey)
	body := response.Body.String()
	require.NotContains(t, body, "candidate left the room")
	require.NotContains(t, body, "private")
	require.NotContains(t, body, "replayed")
	require.Contains(t, body, `"source":"manual"`)
	require.Contains(t, body, `"candidate_reason":"manager_suspended"`)
	require.Contains(t, body, `"connection_closed":true`)
	missing := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":0,"reason":"x"}`))
	missing.Header.Set("Authorization", "Bearer credential")
	missing.Header.Set("Idempotency-Key", "kick-invalid")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, missing)
	require.Equal(t, http.StatusBadRequest, invalid.Code, "invalid revision status = %d body=%s", invalid.Code, invalid.Body.String())
}

func TestManagerEndExamAttemptUsesRevisionFenceAndReturnsOnlySafeReceipt(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/end"
	privateReason := "candidate requested an assisted early end"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":1,"reason":"`+privateReason+`"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "manager-end-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), "status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	require.Equal(t, fake.attempt.ExamID, fake.managerEnd.ExamID)
	require.Equal(t, fake.attempt.SittingID, fake.managerEnd.SittingID)
	require.Equal(t, fake.attempt.ID, fake.managerEnd.AttemptID)
	require.Equal(t, int64(1), fake.managerEnd.ExpectedAttemptRevision)
	require.Equal(t, privateReason, fake.managerEnd.PrivateReason)
	require.Equal(t, "manager-end-once", fake.managerEnd.IdempotencyKey)
	require.NotContains(t, response.Body.String(), privateReason)
	require.NotContains(t, response.Body.String(), "private")
	require.Contains(t, response.Body.String(), fake.submissionReceipt.SubmissionID.String())

	invalid := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_revision":1,"reason":"valid","extra":true}`))
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Idempotency-Key", "manager-end-invalid")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	require.Equal(t, http.StatusBadRequest, invalidResponse.Code, "unknown member status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
}

func TestManagerAttemptRefetchExposesPrivateFreeActiveSuspensionIdentity(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	fake.attempt.State = model.ExamAttemptSuspended
	suspension := &store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: fake.attempt.ID,
		ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 2,
		State: model.AttemptSuspensionActive, Source: model.AttemptSuspensionSourcePolicy,
		CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: fake.attempt.UpdatedAt}
	fake.managerPage.Items[0].ActiveSuspension = suspension
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() + "/attempts/" + fake.attempt.ID.String()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "manager suspension response")
	require.Contains(t, response.Body.String(), suspension.ID.String(), "manager suspension response")
	require.Contains(t, response.Body.String(), `"candidate_reason":"secure_connectivity_lost"`, "manager suspension response")
	require.NotContains(t, response.Body.String(), "private_reason", "manager suspension response")
}

func TestCandidateWorkspaceJournalAndAllHTTPMutationsMapExactProtectedCommands(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	base := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/workspace"
	participationID := fake.participation.ID.String()

	journalRequest := fake.candidateRequest(http.MethodGet, base+"/changes?workspace_cursor=3&limit=20")
	journalResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, journalRequest)
	require.Equal(t, http.StatusOK, journalResponse.Code, "journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	require.Equal(t, int64(3), fake.journalQuery.AfterCursor, "journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	require.Equal(t, 20, fake.journalQuery.Limit, "journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	require.Contains(t, journalResponse.Body.String(), `"current_cursor":5`, "journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	require.Contains(t, journalResponse.Header().Get("Cache-Control"), "no-store", "journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	fake.journalPage.Entries[0].Operation = model.AttemptWorkspaceMutationDeleteEntry
	fake.journalPage.Entries[0].EntryKind = model.StarterWorkspaceEntryDirectory
	fake.journalPage.Entries[0].OldPath = "src"
	fake.journalPage.Entries[0].NewPath, fake.journalPage.Entries[0].ContentVersion = "", ""
	fake.journalPage.Entries[0].Recursive = true
	journalResponse = httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, fake.candidateRequest(http.MethodGet, base+"/changes?workspace_cursor=3&limit=20"))
	require.Equal(t, http.StatusOK, journalResponse.Code, "recursive journal")
	require.Contains(t, journalResponse.Body.String(), `"recursive":true`, "recursive journal")
	require.Contains(t, journalResponse.Body.String(), `"old_path":"src"`, "recursive journal")
	fake.journalPage.HasMore = true
	journalResponse = httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, fake.candidateRequest(http.MethodGet, base+"/changes?workspace_cursor=3&limit=1"))
	require.Equal(t, http.StatusOK, journalResponse.Code, "paged journal")
	var journal candidateWorkspaceJournalResponse
	require.NoError(t, json.Unmarshal(journalResponse.Body.Bytes(), &journal))
	next, err := decodeOpaqueCursor(journal.NextCursor, candidateWorkspaceJournalCursorSpec())
	require.NoError(t, err, "next_cursor")
	require.Equal(t, fake.journalPage.Entries[len(fake.journalPage.Entries)-1].Cursor, next.After, "continuation after the last change")
	journalResponse = httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, fake.candidateRequest(http.MethodGet, base+"/changes?cursor="+journal.NextCursor))
	require.Equal(t, http.StatusOK, journalResponse.Code, "continued journal")
	require.Equal(t, next.After, fake.journalQuery.AfterCursor, "continued journal")
	journalResponse = httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, fake.candidateRequest(http.MethodGet, base+"/changes?workspace_cursor=3&cursor="+journal.NextCursor))
	require.Equal(t, http.StatusBadRequest, journalResponse.Code, "a continuation cannot restart at another Workspace Cursor")
	fake.journalPage.HasMore = false

	directoryBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"path":"src"}`)
	directoryRequest := candidateWorkspaceRequest(fake, http.MethodPost, base+"/directories", directoryBody, "application/json", "mkdir-once")
	directoryResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(directoryResponse, directoryRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, directoryResponse, http.StatusCreated)
	require.Equal(t, "src", fake.workspaceDirectory.Path)
	require.Equal(t, fake.participation.ID, fake.workspaceDirectory.Access.ParticipationID)
	require.Equal(t, int64(1), fake.workspaceDirectory.Access.Generation)
	require.Equal(t, "mkdir-once", fake.workspaceDirectory.IdempotencyKey)

	fileMetadata := map[string]any{"participation_id": participationID, "generation": 1, "path": "empty.txt",
		"media_type": "text/plain", "size": 0, "sha256": strings.Repeat("e", 64)}
	fileBody, fileType := candidateWorkspaceMultipart(t, fileMetadata, nil)
	fileRequest := candidateWorkspaceRequest(fake, http.MethodPost, base+"/files", fileBody, fileType, "file-once")
	fileResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(fileResponse, fileRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, fileResponse, http.StatusCreated)
	require.Equal(t, int64(0), fake.workspaceFile.Size, "file command=%#v uploaded", fake.workspaceFile)
	require.Equal(t, "empty.txt", fake.workspaceFile.Path, "file command=%#v uploaded", fake.workspaceFile)
	require.Empty(t, fake.uploaded, "file command=%#v uploaded", fake.workspaceFile)
	require.Equal(t, "file-once", fake.workspaceFile.IdempotencyKey, "file command=%#v uploaded", fake.workspaceFile)

	entryID := fake.workspacePage.Items[0].EntryID
	moveBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"expected_path":"cmd/main.go","destination_path":"src/main.go"}`)
	moveRequest := candidateWorkspaceRequest(fake, http.MethodPatch, base+"/entries/"+entryID.String(), moveBody, "application/json", "move-once")
	moveResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(moveResponse, moveRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, moveResponse, http.StatusOK)
	require.Equal(t, entryID, fake.workspaceMove.EntryID)
	require.Equal(t, "cmd/main.go", fake.workspaceMove.ExpectedPath)
	require.Equal(t, "src/main.go", fake.workspaceMove.DestinationPath)

	version := fake.workspacePage.Items[0].ContentVersion
	replaceMetadata := map[string]any{"participation_id": participationID, "generation": 1, "expected_path": "cmd/main.go",
		"expected_content_version": version.String(), "media_type": "text/plain", "size": 3, "sha256": strings.Repeat("f", 64)}
	replaceBody, replaceType := candidateWorkspaceMultipart(t, replaceMetadata, []byte("new"))
	replaceRequest := candidateWorkspaceRequest(fake, http.MethodPut, base+"/files/"+entryID.String()+"/content", replaceBody, replaceType, "replace-once")
	replaceResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(replaceResponse, replaceRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, replaceResponse, http.StatusOK)
	require.Equal(t, entryID, fake.workspaceReplace.EntryID, "replace command=%#v uploaded", fake.workspaceReplace)
	require.Equal(t, version, fake.workspaceReplace.ExpectedContentVersion, "replace command=%#v uploaded", fake.workspaceReplace)
	require.Equal(t, "new", string(fake.uploaded), "replace command=%#v uploaded", fake.workspaceReplace)

	deleteBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"expected_path":"cmd/main.go","expected_content_version":"` + version.String() + `"}`)
	deleteRequest := candidateWorkspaceRequest(fake, http.MethodDelete, base+"/entries/"+entryID.String(), deleteBody, "application/json", "delete-once")
	deleteResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(deleteResponse, deleteRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, deleteResponse, http.StatusOK)
	require.Equal(t, entryID, fake.workspaceDelete.EntryID)
	require.Equal(t, "cmd/main.go", fake.workspaceDelete.ExpectedPath)
	require.Equal(t, version, fake.workspaceDelete.ExpectedContentVersion)
}

func TestCandidateWorkspaceMutationsRejectMissingIdempotencyDuplicateJSONAndMissingMultipartSize(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	base := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/workspace"
	participationID := fake.participation.ID.String()
	for _, test := range []struct {
		name, path, contentType, key string
		body                         io.Reader
	}{
		{name: "missing idempotency", path: base + "/directories", contentType: "application/json",
			body: strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"path":"src"}`)},
		{name: "duplicate JSON", path: base + "/directories", contentType: "application/json", key: "dup-once",
			body: strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"generation":2,"path":"src"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := candidateWorkspaceRequest(fake, http.MethodPost, test.path, test.body, test.contentType, test.key)
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "response=%d %s", response.Code, response.Body.String())
		})
	}
	metadata := map[string]any{"participation_id": participationID, "generation": 1, "path": "x", "media_type": "text/plain", "sha256": strings.Repeat("a", 64)}
	body, contentType := candidateWorkspaceMultipart(t, metadata, []byte("x"))
	request := candidateWorkspaceRequest(fake, http.MethodPost, base+"/files", body, contentType, "missing-size")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "missing size=%d %s", response.Code, response.Body.String())
}

func TestCandidateRecursiveWorkspaceDeletionRequiresSnapshot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, fields string
		status       int
	}{
		{"zero cursor", `,"recursive":true,"expected_workspace_cursor":0`, http.StatusOK},
		{"missing cursor", `,"recursive":true`, http.StatusBadRequest},
		{"null cursor", `,"recursive":true,"expected_workspace_cursor":null`, http.StatusBadRequest},
		{"negative cursor", `,"recursive":true,"expected_workspace_cursor":-1`, http.StatusBadRequest},
		{"cursor without recursion", `,"expected_workspace_cursor":0`, http.StatusBadRequest},
		{"null cursor without recursion", `,"expected_workspace_cursor":null`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newExamAttemptHTTPFake(t)
			api := newExamAttemptFocusedAPI(t, fake)
			entryID := model.NewAttemptWorkspaceEntryID()
			path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/workspace/entries/" + entryID.String()
			body := strings.NewReader(`{"participation_id":"` + fake.participation.ID.String() + `","generation":1,"expected_path":"src"` + test.fields + `}`)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, candidateWorkspaceRequest(fake, http.MethodDelete, path, body, "application/json", "recursive"))
			require.Equal(t, test.status, response.Code, "status=%d body=%s", response.Code, response.Body.String())
			if test.status == http.StatusOK {
				require.True(t, fake.workspaceDelete.Recursive)
				require.NotNil(t, fake.workspaceDelete.ExpectedWorkspaceCursor)
				require.Equal(t, int64(0), *fake.workspaceDelete.ExpectedWorkspaceCursor)
			}
			if test.status == http.StatusBadRequest {
				require.False(t, fake.workspaceDelete.EntryID.IsValid(), "invalid recursive request reached application")
			}
		})
	}
}

func candidateWorkspaceRequest(fake *examAttemptHTTPFake, method, path string, body io.Reader, contentType, key string) *http.Request {
	request := newJSONRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set(candidateAttemptCredentialHeader, fake.credential)
	request.Header.Set(candidateAttemptConnectionHeader, fake.connection.ID.String())
	request.Header.Set("Content-Type", contentType)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func candidateWorkspaceMultipart(t *testing.T, metadata any, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataPart, err := writer.CreateFormField("metadata")
	require.NoError(t, err)
	err = json.NewEncoder(metadataPart).Encode(metadata)
	require.NoError(t, err)
	contentPart, err := writer.CreateFormFile("content", "workspace")
	require.NoError(t, err)
	_, err = contentPart.Write(content)
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)
	return &body, writer.FormDataContentType()
}

func assertCandidateWorkspaceMutationHTTP(t *testing.T, fake *examAttemptHTTPFake, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	require.Equal(t, status, response.Code, "response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	require.Contains(t, response.Header().Get("Cache-Control"), "no-store", "response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	body := response.Body.String()
	for _, forbidden := range []string{fake.credential, "continuity_credential", "credential_hash", "participation_id", "generation", "object_id", "session_id"} {
		if forbidden != "" {
			require.NotContains(t, body, forbidden, "response contains")
		}
	}
}

type examAttemptHTTPFake struct {
	principal                 model.Principal
	attempt                   *model.ExamAttempt
	workspace                 *model.ExamAttemptWorkspace
	participation             *store.ExamAttemptParticipationView
	connection                *store.ExamAttemptManagerConnection
	credential                string
	resourceID                model.ExamResourceID
	presentation              examattempt.Presentation
	workspacePage             examattempt.WorkspacePage
	managerPage               examattempt.ManagedAttemptPage
	browserActivityPage       examattempt.BrowserActivityPage
	browserActivityQuery      examattempt.BrowserActivityPageQuery
	correctionAcknowledgement examattempt.AcknowledgeCorrectionCommand
	managerEnd                examattempt.ManagerEndCommand
	content                   examattempt.OpenedContent
	workspaceContentVersion   model.WorkspaceContentVersion
	presentationAccess        examattempt.CandidateAccess
	presentationCalls         int
	workspaceQuery            examattempt.WorkspaceQuery
	managerList               examattempt.ListManagedAttemptsQuery
	managerGet                examattempt.GetManagedAttemptQuery
	workspaceOpen             examattempt.OpenWorkspaceFileQuery
	reallow                   examattempt.ReallowCommand
	suspend                   examattempt.ManualSuspensionCommand
	journalQuery              examattempt.WorkspaceJournalQuery
	journalPage               examattempt.WorkspaceJournalPage
	workspaceDirectory        examattempt.CreateWorkspaceDirectoryCommand
	workspaceFile             examattempt.CreateWorkspaceFileCommand
	workspaceReplace          examattempt.ReplaceWorkspaceFileCommand
	workspaceMove             examattempt.MoveWorkspaceEntryCommand
	workspaceDelete           examattempt.DeleteWorkspaceEntryCommand
	workspaceMutation         examattempt.WorkspaceMutationResult
	submit                    examattempt.SubmitCommand
	submissionReceipt         store.ExamSubmissionReceipt
	submissionView            examattempt.ManagedSubmission
	submissionManifest        examattempt.SubmissionManifestPage
	submissionGet             examattempt.GetSubmissionQuery
	submissionManifestQuery   examattempt.ListSubmissionManifestQuery
	submissionOpen            examattempt.OpenSubmissionFileQuery
	uploaded                  []byte
}

func newExamAttemptHTTPFake(t *testing.T) *examAttemptHTTPFake {
	t.Helper()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 123000000, time.UTC)
	attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), model.NewExamSittingID(), model.NewUserID(), model.NewExamRevisionID(), at)
	require.NoError(t, err)
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), attempt.ID, at)
	require.NoError(t, err)
	connectionID := model.NewAttemptConnectionID()
	resourceID := model.NewExamResourceID()
	workspaceItem := store.CandidateAttemptWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile,
		Path: "cmd/main.go", ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 9, SHA256: strings.Repeat("a", 64)}
	principal := testExamHTTPPrincipal()
	fake := &examAttemptHTTPFake{principal: principal, attempt: attempt, workspace: workspace,
		participation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
			Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)},
		connection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at},
		credential: model.NewCredentialToken(), resourceID: resourceID,
		presentation: examattempt.Presentation{AttemptID: attempt.ID, SittingID: attempt.SittingID,
			Capacity:            model.ExamCapacityPolicy{ResourceMaximumCount: 7, ResourceMaximumBytes: 4096, WorkspaceMaximumEntries: 42, WorkspaceMaximumFileBytes: 1024, WorkspaceMaximumTotalBytes: 8192},
			RuntimeCapabilities: candidateTestRuntimeCapabilities(t, at, attempt), Title: "Algorithms",
			InstructionsMarkdown: "Solve safely.", Resources: []examattempt.Resource{{ResourceID: resourceID, DisplayName: "Input",
				DescriptionMarkdown: "Read this.", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 9, SHA256: strings.Repeat("b", 64)}}},
		workspacePage: examattempt.WorkspacePage{WorkspaceID: workspace.ID, Cursor: 4,
			Items: []store.CandidateAttemptWorkspaceItem{workspaceItem}},
		managerPage: examattempt.ManagedAttemptPage{Items: []store.ExamAttemptManagerSnapshot{{Attempt: attempt, Workspace: workspace,
			LatestParticipation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
				Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)}, CurrentConnection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at}}}},
		content:                 examattempt.OpenedContent{Body: io.NopCloser(strings.NewReader("protected")), MediaType: "text/plain", SizeBytes: 9, SHA256: strings.Repeat("c", 64)},
		workspaceContentVersion: model.NewWorkspaceContentVersion()}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspace.ID, Cursor: 5, EntryID: workspaceItem.EntryID,
		EntryKind: workspaceItem.Kind, Operation: model.AttemptWorkspaceMutationReplaceFile, OldPath: workspaceItem.Path,
		NewPath: workspaceItem.Path, ContentVersion: workspaceItem.ContentVersion, ChangedAt: at}
	fake.workspaceMutation = examattempt.WorkspaceMutationResult{AttemptID: attempt.ID, SittingID: attempt.SittingID,
		CandidateUserID: attempt.CandidateUserID, WorkspaceID: workspace.ID, Entry: &workspaceItem, Change: change}
	fake.journalPage = examattempt.WorkspaceJournalPage{WorkspaceID: workspace.ID, CurrentCursor: 5,
		Entries: []model.AttemptWorkspaceJournalEntry{change}}
	sourceID := model.BrowserSourceSessionID("01912ed5-d4f7-4f02-9b97-928b5e8d3f21")
	fake.browserActivityPage = examattempt.BrowserActivityPage{Items: []store.BrowserActivityRecord{{AttemptID: attempt.ID,
		ParticipationID: fake.participation.ID, Generation: 1, SourceSessionID: sourceID,
		Event: model.BrowserActivityEvent{Sequence: 1, Kind: model.BrowserActivityOpened, PolicyRevisionID: model.NewExamRevisionID(),
			ClientOccurredAt: at, ReceivedAt: at.Add(time.Second)}}}}
	fake.submissionReceipt = store.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: model.NewSubmissionID(), AttemptID: attempt.ID,
		ExamRevisionID: attempt.AdmissionRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 4,
		ManifestDigest: strings.Repeat("d", 64), SubmittedAt: at.Add(time.Minute)}
	submissionManifest, err := model.NewExamSubmissionManifest(4, []model.ExamSubmissionManifestEntry{{
		EntryID: workspaceItem.EntryID, Kind: model.StarterWorkspaceEntryFile, Path: workspaceItem.Path,
		ContentVersion: workspaceItem.ContentVersion, MediaType: workspaceItem.MediaType, SizeBytes: workspaceItem.SizeBytes,
		SHA256: workspaceItem.SHA256, StorageOrigin: model.AttemptWorkspaceStorageStarter,
		StarterObjectID: model.NewStarterWorkspaceObjectID(),
	}})
	require.NoError(t, err)
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: fake.submissionReceipt.SubmissionID,
		AttemptID: attempt.ID, ExamRevisionID: attempt.AdmissionRevisionID, WorkspaceID: workspace.ID, Manifest: submissionManifest,
		BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: at.Add(time.Minute)})
	require.NoError(t, err)
	fake.submissionView = examattempt.ManagedSubmission{Authorization: store.ExamSubmissionAuthorization{
		SubmissionID: submission.ID, ExamID: attempt.ExamID, SittingID: attempt.SittingID, AttemptID: attempt.ID,
		CandidateUserID: attempt.CandidateUserID, AcademicUnitID: model.NewAcademicUnitID()}, Submission: *submission}
	fake.submissionManifest = examattempt.SubmissionManifestPage{SubmissionID: submission.ID,
		WorkspaceCursor: submission.WorkspaceCursor, ManifestDigest: submission.ManifestDigest,
		Items: []store.ExamSubmissionManifestItem{{EntryID: workspaceItem.EntryID, Kind: workspaceItem.Kind,
			Path: workspaceItem.Path, ContentVersion: workspaceItem.ContentVersion, MediaType: workspaceItem.MediaType,
			SizeBytes: workspaceItem.SizeBytes, SHA256: workspaceItem.SHA256}}}
	return fake
}

func candidateTestRuntimeCapabilities(t *testing.T, at time.Time, attempt *model.ExamAttempt) store.CandidateRuntimeCapabilities {
	t.Helper()
	manifest := model.CurrentAttemptConfigurationManifestFingerprint()
	configuration, err := (model.AttemptConfigurationCandidate{ManifestFingerprint: manifest, RegistryFingerprint: "fnv1a64:" + strings.Repeat("b", 16), UserSettingsRevision: model.NewUserSettingsRevision(), DesktopBuild: "test-build", DesktopTarget: "darwin-arm64", Presentation: model.AttemptConfigurationPresentation{ColorTheme: "dark", ZoomPercent: 100, EditorFontSizePX: 14, EditorLineHeightPX: 22, ScreenReaderMode: "auto", AnnouncementMode: "auto", CursorStyle: "line", CursorBlinking: "blink"}, ApprovedCommands: []string{}, ApprovedKeybindings: []string{}}).Freeze(model.NewID())
	require.NoError(t, err)
	return store.CandidateRuntimeCapabilities{SchemaVersion: 1, ServerTime: at,
		InteractionState: store.CandidateInteractionInteractive,
		AttemptConfiguration: store.CandidateAttemptConfiguration{Revision: configuration.Revision, Presentation: configuration.Presentation,
			ApprovedCommands: append([]string{}, configuration.ApprovedCommands...), ApprovedKeybindings: append([]string{}, configuration.ApprovedKeybindings...), Digest: configuration.Digest},
		FocusLossCollectionEnabled: true, WorkspaceMutationAllowed: true, SubmissionAllowed: true,
		Browser: store.CandidateBrowserCapability{State: store.CandidateBrowserDisabled},
		ExamRevision: store.CandidateExamRevisionCapability{AdmissionRevisionID: attempt.AdmissionRevisionID,
			CurrentRevisionID: model.NewExamRevisionID()},
		Departure: store.CandidateDepartureCapability{Allowed: false, Reason: "attempt_in_progress"}}
}

func newExamAttemptFocusedAPI(t *testing.T, fake *examAttemptHTTPFake) *API {
	t.Helper()
	logger, _ := newTestLogger(t)
	return newFocusedResourceAPI(t, logger, fake, examAttemptResource(fake))
}

func (f *examAttemptHTTPFake) candidateRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set(candidateAttemptCredentialHeader, f.credential)
	request.Header.Set(candidateAttemptConnectionHeader, f.connection.ID.String())
	return request
}

func (f *examAttemptHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examAttemptHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examAttemptHTTPFake) GetExamAttempt(_ context.Context, _ invocation.Invocation, query examattempt.GetManagedAttemptQuery) (store.ExamAttemptManagerSnapshot, error) {
	f.managerGet = query
	return f.managerPage.Items[0], nil
}

func (f *examAttemptHTTPFake) ListExamAttempts(_ context.Context, _ invocation.Invocation, query examattempt.ListManagedAttemptsQuery) (examattempt.ManagedAttemptPage, error) {
	f.managerList = query
	return f.managerPage, nil
}

func (f *examAttemptHTTPFake) ListSittingCandidateStatuses(context.Context, invocation.Invocation,
	examattempt.SittingCandidateStatusesQuery,
) (examattempt.SittingCandidateStatusesPage, error) {
	return examattempt.SittingCandidateStatusesPage{}, nil
}

func (f *examAttemptHTTPFake) EndExamAttemptByManager(_ context.Context, _ invocation.Invocation,
	command examattempt.ManagerEndCommand,
) (store.ExamSubmissionReceipt, error) {
	f.managerEnd = command
	return f.submissionReceipt, nil
}

func (f *examAttemptHTTPFake) ListExamAttemptBrowserActivity(_ context.Context, _ invocation.Invocation,
	query examattempt.BrowserActivityPageQuery,
) (examattempt.BrowserActivityPage, error) {
	f.browserActivityQuery = query
	return f.browserActivityPage, nil
}

func (f *examAttemptHTTPFake) AcknowledgeExamAttemptCorrection(_ context.Context, _ invocation.Invocation,
	command examattempt.AcknowledgeCorrectionCommand,
) (examattempt.CorrectionAcknowledgementResult, error) {
	f.correctionAcknowledgement = command
	capabilities := f.presentation.RuntimeCapabilities
	capabilities.ExamRevision.CurrentRevisionID = command.ExpectedCurrentRevisionID
	return examattempt.CorrectionAcknowledgementResult{CorrectionRevisionID: command.CorrectionRevisionID,
		CurrentRevisionID: command.ExpectedCurrentRevisionID, RuntimeCapabilities: capabilities, AcknowledgedAt: model.OptionalTimeFrom(f.attempt.UpdatedAt.Add(time.Minute))}, nil
}

func (f *examAttemptHTTPFake) ReallowExamAttempt(_ context.Context, invocation invocation.Invocation, command examattempt.ReallowCommand) (examattempt.ReallowResult, error) {
	f.reallow = command
	attempt := *f.attempt
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptActive, attempt.UpdatedAt.Add(time.Minute), command.ExpectedAttemptRevision+1
	return examattempt.ReallowResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID,
		ClassID: model.NewClassID(), CandidateUserID: attempt.CandidateUserID, Attempt: attempt,
		Suspension: store.ExamAttemptSuspensionView{ID: command.SuspensionID, AttemptID: attempt.ID,
			ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 1,
			State: model.AttemptSuspensionClosed, Source: model.AttemptSuspensionSourcePolicy,
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: attempt.CreatedAt,
			EndedAt: model.OptionalTimeFrom(attempt.UpdatedAt), ReallowedByUserID: invocation.Principal().UserID}}, nil
}

func (f *examAttemptHTTPFake) SuspendExamAttempt(_ context.Context, invocation invocation.Invocation, command examattempt.ManualSuspensionCommand) (examattempt.ManualSuspensionResult, error) {
	f.suspend = command
	attempt := *f.attempt
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, attempt.UpdatedAt.Add(time.Minute), command.ExpectedAttemptRevision+1
	return examattempt.ManualSuspensionResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID,
		ClassID: model.NewClassID(), CandidateUserID: attempt.CandidateUserID, Attempt: attempt, ConnectionClosed: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: attempt.ID,
			ParticipationID: model.NewAttemptParticipationID(), Generation: 1, State: model.AttemptSuspensionActive,
			Source: model.AttemptSuspensionSourceManual, CandidateReason: model.AttemptSuspensionCandidateReasonManagerSuspended,
			StartedAt: attempt.UpdatedAt, SuspendedByUserID: invocation.Principal().UserID}}, nil
}

func (f *examAttemptHTTPFake) GetCandidateExamPresentation(_ context.Context, _ invocation.Invocation, access examattempt.CandidateAccess) (examattempt.Presentation, error) {
	f.presentationAccess, f.presentationCalls = access, f.presentationCalls+1
	return f.presentation, nil
}

func (f *examAttemptHTTPFake) ListCandidateExamWorkspace(_ context.Context, _ invocation.Invocation, query examattempt.WorkspaceQuery) (examattempt.WorkspacePage, error) {
	f.workspaceQuery = query
	return f.workspacePage, nil
}

func (f *examAttemptHTTPFake) ListCandidateExamWorkspaceJournal(_ context.Context, _ invocation.Invocation, query examattempt.WorkspaceJournalQuery) (examattempt.WorkspaceJournalPage, error) {
	f.journalQuery = query
	return f.journalPage, nil
}

func (f *examAttemptHTTPFake) CreateCandidateExamWorkspaceDirectory(_ context.Context, _ invocation.Invocation, command examattempt.CreateWorkspaceDirectoryCommand) (examattempt.WorkspaceMutationResult, error) {
	f.workspaceDirectory = command
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) CreateCandidateExamWorkspaceFile(_ context.Context, _ invocation.Invocation, command examattempt.CreateWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	f.workspaceFile = command
	f.uploaded, _ = io.ReadAll(command.Body)
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) ReplaceCandidateExamWorkspaceFile(_ context.Context, _ invocation.Invocation, command examattempt.ReplaceWorkspaceFileCommand) (examattempt.WorkspaceMutationResult, error) {
	f.workspaceReplace = command
	f.uploaded, _ = io.ReadAll(command.Body)
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) MoveCandidateExamWorkspaceEntry(_ context.Context, _ invocation.Invocation, command examattempt.MoveWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	f.workspaceMove = command
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) DeleteCandidateExamWorkspaceEntry(_ context.Context, _ invocation.Invocation, command examattempt.DeleteWorkspaceEntryCommand) (examattempt.WorkspaceMutationResult, error) {
	f.workspaceDelete = command
	result := f.workspaceMutation
	result.Entry = nil
	result.Change.Operation = model.AttemptWorkspaceMutationDeleteEntry
	result.Change.NewPath, result.Change.ContentVersion = "", ""
	return result, nil
}

func (f *examAttemptHTTPFake) SubmitExamAttempt(_ context.Context, _ invocation.Invocation,
	command examattempt.SubmitCommand,
) (store.ExamSubmissionReceipt, error) {
	f.submit = command
	return f.submissionReceipt, nil
}

func (f *examAttemptHTTPFake) GetExamSubmission(_ context.Context, _ invocation.Invocation,
	query examattempt.GetSubmissionQuery,
) (examattempt.ManagedSubmission, error) {
	f.submissionGet = query
	return f.submissionView, nil
}

func (f *examAttemptHTTPFake) ListExamSubmissionManifest(_ context.Context, _ invocation.Invocation,
	query examattempt.ListSubmissionManifestQuery,
) (examattempt.SubmissionManifestPage, error) {
	f.submissionManifestQuery = query
	return f.submissionManifest, nil
}

func (f *examAttemptHTTPFake) OpenExamSubmissionFile(_ context.Context, _ invocation.Invocation,
	query examattempt.OpenSubmissionFileQuery,
) (examattempt.OpenedContent, error) {
	f.submissionOpen = query
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	f.content.ContentVersion = f.submissionManifest.Items[0].ContentVersion
	f.content.SHA256 = f.submissionManifest.Items[0].SHA256
	f.content.MediaType = f.submissionManifest.Items[0].MediaType
	f.content.SizeBytes = f.submissionManifest.Items[0].SizeBytes
	return f.content, nil
}

func (f *examAttemptHTTPFake) OpenCandidateExamResource(context.Context, invocation.Invocation, examattempt.OpenResourceQuery) (examattempt.OpenedContent, error) {
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	return f.content, nil
}

func (f *examAttemptHTTPFake) OpenCandidateExamWorkspaceFile(_ context.Context, _ invocation.Invocation, query examattempt.OpenWorkspaceFileQuery) (examattempt.OpenedContent, error) {
	f.workspaceOpen = query
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	f.content.ContentVersion = f.workspaceContentVersion
	return f.content, nil
}

func (f *examAttemptHTTPFake) PrepareExamSecurityPreflight(context.Context, invocation.Invocation, examattempt.PrepareSecurityPreflightCommand) (*store.SecurityPreflightPrepared, error) {
	return nil, errors.New("unexpected preflight prepare")
}
func (f *examAttemptHTTPFake) ReportExamSecurityPreflight(context.Context, invocation.Invocation, examattempt.ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error) {
	return nil, errors.New("unexpected preflight report")
}

func (f *examAttemptHTTPFake) RecoverExamSecurityPolicy(context.Context, invocation.Invocation, model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	return nil, errors.New("unexpected security recovery")
}

func (f *examAttemptHTTPFake) NativeDeliveryStatus(context.Context, invocation.Invocation, examattempt.NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected NativeDeliveryStatus")
}

func (f *examAttemptHTTPFake) NativeDeliveryReceipt(context.Context, invocation.Invocation, examattempt.NativeDeliveryQuery, int64) (*model.NativeBatchReceipt, error) {
	return nil, errors.New("unexpected NativeDeliveryReceipt")
}

func (f *examAttemptHTTPFake) DeclareNativeDeliveryGaps(context.Context, invocation.Invocation, examattempt.NativeDeliveryGapsCommand) (*model.DeliveryGapReceipt, error) {
	return nil, errors.New("unexpected DeclareNativeDeliveryGaps")
}

func (f *examAttemptHTTPFake) SealNativeDelivery(context.Context, invocation.Invocation, examattempt.NativeDeliveryFinalCommand) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected SealNativeDelivery")
}

func (f *examAttemptHTTPFake) UpdateNativeDeliverySummary(context.Context, invocation.Invocation, examattempt.NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected UpdateNativeDeliverySummary")
}

func (f *examAttemptHTTPFake) AppendNativeDelivery(context.Context, invocation.Invocation, examattempt.NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendNativeDelivery")
}

func (f *examAttemptHTTPFake) BrowserSourceStatus(context.Context, invocation.Invocation, examattempt.BrowserSourceQuery) (*model.BrowserSourceStatus, error) {
	return nil, apperror.New("exam.attempt.unavailable")
}
func (f *examAttemptHTTPFake) BrowserSourceList(context.Context, invocation.Invocation, examattempt.BrowserSourceQuery) ([]model.BrowserSourceStatus, error) {
	return nil, apperror.New("exam.attempt.unavailable")
}

func (f *examAttemptHTTPFake) BrowserDeliveryReceipts(context.Context, invocation.Invocation, examattempt.BrowserSourceQuery, int64, int) (*model.BrowserReceiptPage, error) {
	return nil, errors.New("unexpected BrowserDeliveryReceipts")
}
func (f *examAttemptHTTPFake) DeclareBrowserDeliveryGaps(context.Context, invocation.Invocation, examattempt.BrowserDeliveryGapsCommand) (*model.BrowserDeliveryGapResult, error) {
	return nil, errors.New("unexpected DeclareBrowserDeliveryGaps")
}
func (f *examAttemptHTTPFake) SealBrowserDelivery(context.Context, invocation.Invocation, examattempt.BrowserDeliveryFinalCommand) (*model.BrowserSourceStatus, error) {
	return nil, errors.New("unexpected SealBrowserDelivery")
}
func (f *examAttemptHTTPFake) UpdateBrowserDeliverySummary(context.Context, invocation.Invocation, examattempt.BrowserDeliverySummaryCommand) (*model.BrowserDeliverySummaryResult, error) {
	return nil, errors.New("unexpected UpdateBrowserDeliverySummary")
}
func (f *examAttemptHTTPFake) AppendHistoricalBrowserDelivery(context.Context, invocation.Invocation, examattempt.BrowserDeliveryAppendCommand) (*model.BrowserActivityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendHistoricalBrowserDelivery")
}

func (f *examAttemptHTTPFake) DeliveryBudget(context.Context, invocation.Invocation, examattempt.DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error) {
	return nil, errors.New("unexpected DeliveryBudget")
}
func (f *examAttemptHTTPFake) StopDeliveryDetails(context.Context, invocation.Invocation, examattempt.StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error) {
	return nil, errors.New("unexpected StopDeliveryDetails")
}

// assertNoForbiddenJSONTerms rejects forbidden terms in member names and in
// non-identifier string values. Random opaque IDs are skipped because their
// base32 text can spell a forbidden word by chance.
func assertNoForbiddenJSONTerms(t *testing.T, body []byte, forbidden ...string) {
	t.Helper()
	var document any
	require.NoError(t, json.Unmarshal(body, &document), "decode %s", body)
	var texts []string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, member := range typed {
				texts = append(texts, key)
				walk(member)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case string:
			if !model.IsValidOpaqueID(typed) {
				texts = append(texts, typed)
			}
		}
	}
	walk(document)
	for _, text := range texts {
		for _, term := range forbidden {
			require.NotContains(t, strings.ToLower(text), term, "response exposes %q in %q: %s", term, text, body)
		}
	}
}

func TestWorkspaceContentNeverRendersAsAnActiveDocument(t *testing.T) {
	t.Parallel()

	for mediaType, want := range map[string]string{
		"text/x-go":                 "text/x-go",
		"text/plain; charset=utf-8": "text/plain; charset=utf-8",
		"image/png":                 "image/png",
		"application/pdf":           "application/pdf",
		"text/html":                 "text/plain; charset=utf-8",
		"Text/HTML; charset=utf-8":  "text/plain; charset=utf-8",
		"application/xhtml+xml":     "text/plain; charset=utf-8",
		"image/svg+xml":             "text/plain; charset=utf-8",
		"application/xml":           "text/plain; charset=utf-8",
		"text/xml":                  "text/plain; charset=utf-8",
		"not a media type;;":        "application/octet-stream",
	} {
		t.Run(mediaType, func(t *testing.T) {
			t.Parallel()
			headers := http.Header{"Content-Type": {mediaType}}
			untrustedWorkspaceContentHeaders(headers)
			require.Equal(t, want, headers.Get("Content-Type"))
			require.Equal(t, "sandbox", headers.Get("Content-Security-Policy"))
		})
	}
}
