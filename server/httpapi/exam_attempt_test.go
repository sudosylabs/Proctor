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

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
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
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache = %d/%q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if fake.presentationAccess.AttemptID != fake.attempt.ID || fake.presentationAccess.ConnectionID != fake.connection.ID ||
		fake.presentationAccess.ContinuityCredential != fake.credential {
		t.Fatalf("candidate access = %#v", fake.presentationAccess)
	}
	if strings.Contains(response.Body.String(), fake.credential) || strings.Contains(response.Body.String(), "credential") ||
		strings.Contains(response.Body.String(), "file_revision") || strings.Contains(response.Body.String(), "object_id") {
		t.Fatalf("candidate response leaked protected internals: %s", response.Body.String())
	}
	var payload map[string]json.RawMessage
	var capabilities candidateRuntimeCapabilitiesResponse
	var capacity examCapacityPolicyResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 9 {
		t.Fatalf("presentation shape = %v, %v", payload, err)
	}
	if err := json.Unmarshal(payload["capacity"], &capacity); err != nil || capacity != examCapacityPolicyResponseFromModel(fake.presentation.Capacity) {
		t.Fatalf("presentation capacity = %#v, %v", capacity, err)
	}
	if err := json.Unmarshal(payload["candidate_runtime_capabilities"], &capabilities); err != nil ||
		!capabilities.FocusLossCollectionEnabled || capabilities.SchemaVersion != 1 ||
		capabilities.Departure.Allowed || capabilities.Departure.Reason != "attempt_in_progress" {
		t.Fatalf("presentation capabilities = %#v, %v", capabilities, err)
	}
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
		if response.Code != http.StatusBadRequest || fake.presentationCalls != 0 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/calls/cache = %d/%d/%q: %s", response.Code, fake.presentationCalls, response.Header().Get("Cache-Control"), response.Body.String())
		}
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
	if response.Code != http.StatusOK || fake.workspaceQuery.Limit != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/query/cache = %d/%#v/%q: %s", response.Code, fake.workspaceQuery, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var page candidateExamWorkspaceListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].Size == nil {
		t.Fatalf("Workspace page = %#v, %v", page, err)
	}
	cursor, err := decodeCandidateWorkspaceCursor(page.NextCursor)
	if err != nil || cursor.ExpectedCursor != fake.workspacePage.Cursor || cursor.ID != fake.workspacePage.Items[0].EntryID ||
		fake.workspaceQuery.ExpectedCursor != -1 {
		t.Fatalf("Workspace cursor = %#v, %v", cursor, err)
	}
	fake.workspacePage.HasMore = false
	second := fake.candidateRequest(http.MethodGet, "/api/v1/exam-attempts/"+fake.attempt.ID.String()+"/workspace?cursor="+url.QueryEscape(page.NextCursor))
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK || fake.workspaceQuery.ExpectedCursor != cursor.ExpectedCursor || fake.workspaceQuery.AfterEntryID != cursor.ID {
		t.Fatalf("Workspace cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.workspaceQuery, secondResponse.Body.String())
	}
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
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" ||
		fake.submit.Access.AttemptID != fake.attempt.ID || fake.submit.Access.ParticipationID != fake.participation.ID ||
		fake.submit.Access.Generation != 1 || fake.submit.ExpectedCurrentRevisionID != fake.submissionReceipt.ExamRevisionID ||
		fake.submit.ExpectedWorkspaceCursor != 4 ||
		fake.submit.FinalFocusLossSequence != 0 || fake.submit.IdempotencyKey != "submit-once" {
		t.Fatalf("status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 8 ||
		string(payload["exam_revision_id"]) != `"`+fake.submissionReceipt.ExamRevisionID.String()+`"` ||
		string(payload["state"]) != `"submitted"` || string(payload["workspace_cursor"]) != "4" {
		t.Fatalf("receipt=%v error=%v", payload, err)
	}
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
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		command.Access.AttemptID != fake.attempt.ID || command.Access.ConnectionID != fake.connection.ID ||
		command.Access.ContinuityCredential != fake.credential || command.Access.ParticipationID != fake.participation.ID ||
		command.Access.Generation != 1 || command.CorrectionRevisionID != correctionID ||
		command.ExpectedCurrentRevisionID != currentRevisionID || command.IdempotencyKey != "acknowledge-correction-once" {
		t.Fatalf("status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), command, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 5 ||
		string(payload["revision_id"]) != `"`+correctionID.String()+`"` ||
		string(payload["acknowledgement_state"]) != `"acknowledged"` {
		t.Fatalf("acknowledgement response = %v, %v", payload, err)
	}
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
	if invalidFake.correctionAcknowledgement.CorrectionRevisionID.IsValid() {
		t.Fatalf("unknown-member request reached application: %#v", invalidFake.correctionAcknowledgement)
	}
}

func TestSubmissionManifestCursorIsVersionedStrictAndPathFree(t *testing.T) {
	t.Parallel()

	entryID := model.NewAttemptWorkspaceEntryID()
	raw, err := encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entryID})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSubmissionManifestCursor(raw)
	if err != nil || decoded.EntryID != entryID {
		t.Fatalf("decoded cursor = %#v, %v", decoded, err)
	}
	if strings.Contains(raw, "src") || strings.Contains(raw, "main.go") {
		t.Fatalf("cursor exposed a Workspace path: %q", raw)
	}
	unsupported, _ := json.Marshal(submissionManifestCursorWire{Version: 2, EntryID: entryID.String()})
	if _, err = decodeSubmissionManifestCursor(base64.RawURLEncoding.EncodeToString(unsupported)); err == nil {
		t.Fatal("unsupported cursor version was accepted")
	}
	unknown := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"entry_id":"` + entryID.String() + `","path":"src/main.go"}`))
	if _, err = decodeSubmissionManifestCursor(unknown); err == nil {
		t.Fatal("cursor containing a path field was accepted")
	}
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
	if response.Code != http.StatusOK || response.Body.String() != "protected" || response.Header().Get("Content-Disposition") != "" ||
		response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("ETag") != wantETag {
		t.Fatalf("content response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}

	request = fake.candidateRequest(http.MethodGet, path)
	request.Header.Set("If-None-Match", wantETag)
	response = httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("conditional response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
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
	if response.Code != http.StatusOK || fake.workspaceOpen.EntryID != entryID || !fake.workspaceOpen.Access.ConnectionID.IsValid() ||
		response.Header().Get("ETag") != `"`+fake.workspaceContentVersion.String()+`"` || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Workspace content = %d query=%#v headers=%#v body=%s", response.Code, fake.workspaceOpen, response.Header(), response.Body.String())
	}
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
	if response.Code != http.StatusOK || fake.managerList.Limit != 1 || len(fake.managerList.States) != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("manager response/query = %d/%#v/%q: %s", response.Code, fake.managerList, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if strings.Contains(response.Body.String(), "credential") || strings.Contains(response.Body.String(), "session_id") || strings.Contains(response.Body.String(), "hash") {
		t.Fatalf("manager response leaked secrets: %s", response.Body.String())
	}
	var page examAttemptManagerListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("manager page = %#v, %v", page, err)
	}
	cursor, err := decodeExamAttemptManagerCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	fake.managerPage.HasMore = false
	second := httptest.NewRequest(http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK || !fake.managerList.BeforeCreatedAt.Equal(cursor.CreatedAt) || fake.managerList.BeforeAttemptID != cursor.ID {
		t.Fatalf("manager cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.managerList, secondResponse.Body.String())
	}
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
	if response.Code != http.StatusOK || fake.managerGet.ExamID != fake.attempt.ExamID || fake.managerGet.SittingID != fake.attempt.SittingID ||
		fake.managerGet.AttemptID != fake.attempt.ID || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("manager exact = %d query=%#v headers=%#v body=%s", response.Code, fake.managerGet, response.Header(), response.Body.String())
	}
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
	if response.Code != http.StatusOK || fake.browserActivityQuery.Limit != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Browser Activity response/query = %d/%#v/%q: %s", response.Code, fake.browserActivityQuery, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var page browserActivityListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].Location != nil {
		t.Fatalf("Browser Activity page = %#v, %v", page, err)
	}
	for _, forbidden := range []string{"participation_id", "connection_id", "registration_id", "credential", "review", "location_scheme"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("Browser Activity response contains %q: %s", forbidden, response.Body.String())
		}
	}
	cursor, err := decodeBrowserActivityCursor(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	fake.browserActivityPage.HasMore = false
	second := httptest.NewRequest(http.MethodGet, path+"&cursor="+url.QueryEscape(page.NextCursor), nil)
	second.Header.Set("Authorization", "Bearer credential")
	secondResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusOK || !fake.browserActivityQuery.AfterReceivedAt.Equal(cursor.ReceivedAt) ||
		fake.browserActivityQuery.AfterSourceID != cursor.SourceSessionID || fake.browserActivityQuery.AfterSequence != cursor.Sequence {
		t.Fatalf("Browser Activity cursor forwarding = %d query=%#v body=%s", secondResponse.Code, fake.browserActivityQuery, secondResponse.Body.String())
	}
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
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s = %d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
		for _, forbidden := range []string{"starter_object", "attempt_object", "storage_origin", "vfs", "url"} {
			if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
				t.Fatalf("GET %s exposed %q: %s", path, forbidden, response.Body.String())
			}
		}
	}
	if fake.submissionGet.SubmissionID != fake.submissionView.Submission.ID || fake.submissionManifestQuery.Limit != 1 ||
		fake.submissionManifestQuery.SubmissionID != fake.submissionView.Submission.ID {
		t.Fatalf("get=%#v manifest=%#v", fake.submissionGet, fake.submissionManifestQuery)
	}
	entry := fake.submissionManifest.Items[0]
	cursor, err := encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entry.EntryID})
	if err != nil {
		t.Fatal(err)
	}
	fake.submissionManifest.HasMore = false
	cursorRequest := httptest.NewRequest(http.MethodGet, base+"/manifest?cursor="+url.QueryEscape(cursor), nil)
	cursorRequest.Header.Set("Authorization", "Bearer credential")
	cursorResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(cursorResponse, cursorRequest)
	if cursorResponse.Code != http.StatusOK || fake.submissionManifestQuery.AfterEntryID != entry.EntryID {
		t.Fatalf("manifest cursor forwarding = %d query=%#v body=%s", cursorResponse.Code, fake.submissionManifestQuery, cursorResponse.Body.String())
	}
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
	if response.Code != http.StatusOK || response.Body.String() != "protected" ||
		response.Header().Get("Cache-Control") != "private, no-store" ||
		response.Header().Get("ETag") != `"`+entry.SHA256+`"` || response.Header().Get("Content-Disposition") != "" ||
		fake.submissionOpen.EntryID != entry.EntryID {
		t.Fatalf("content=%d headers=%v query=%#v body=%q", response.Code, response.Header(), fake.submissionOpen, response.Body.String())
	}
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
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("header read = %d: %s", response.Code, response.Body.String())
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			for _, counter := range []string{"final_focus_loss_sequence", "unresolved_integrity_count"} {
				value, exists := fields[counter]
				if exists == retired || exists && string(value) != "0" {
					t.Fatalf("counter presence/value disagrees with retirement: %s", response.Body.String())
				}
			}
			_, hasRetirement := fields["integrity_retired_at"]
			if hasRetirement != retired || string(fields["integrity_state"]) != `"`+string(submission.IntegrityState)+`"` ||
				string(fields["manifest_digest"]) != `"`+submission.ManifestDigest+`"` || len(fields["workspace_cursor"]) == 0 {
				t.Fatalf("retirement/retained-work projection = %s", response.Body.String())
			}
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
	if err != nil || access.ContinuityCredential != credential || access.ConnectionID != connectionID {
		t.Fatalf("valid headers = %#v, %v", access, err)
	}

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
			if _, headerErr := candidateAttemptAccessHeaders(request); headerErr == nil {
				t.Fatal("invalid candidate Attempt headers were accepted")
			}
		})
	}
}

func TestExamAttemptManagerCursorIsOpaqueStrictAndVersioned(t *testing.T) {
	t.Parallel()
	want := examAttemptManagerCursor{CreatedAt: time.Date(2026, time.August, 17, 9, 0, 0, 123000000, time.UTC), ID: model.NewExamAttemptID()}
	encoded, err := encodeExamAttemptManagerCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	if encoded == "" || strings.ContainsAny(encoded, "+/=") {
		t.Fatalf("cursor is not raw URL-safe: %q", encoded)
	}
	got, err := decodeExamAttemptManagerCursor(encoded)
	if err != nil || got != want {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}
	for _, invalid := range []string{"", "%%%", encoded + "trailing"} {
		if _, decodeErr := decodeExamAttemptManagerCursor(invalid); decodeErr == nil {
			t.Fatalf("invalid cursor %q was accepted", invalid)
		}
	}
}

func TestCandidateWorkspaceCursorContainsNoReversiblePath(t *testing.T) {
	t.Parallel()
	want := candidateWorkspaceCursor{ExpectedCursor: 7, ID: model.NewAttemptWorkspaceEntryID()}
	encoded, err := encodeCandidateWorkspaceCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCandidateWorkspaceCursor(encoded)
	if err != nil || got != want {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || strings.Contains(string(decoded), "cmd/main.go") || strings.Contains(string(decoded), `"path"`) {
		t.Fatalf("cursor exposes Workspace path: %q, %v", decoded, err)
	}
	if _, encodeErr := encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: -1, ID: want.ID}); encodeErr == nil {
		t.Fatal("negative Workspace cursor encoded successfully")
	}
	withoutEntry, err := encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: want.ExpectedCursor})
	if err != nil {
		t.Fatal(err)
	}
	decodedWithoutEntry, err := decodeCandidateWorkspaceCursor(withoutEntry)
	if err != nil || decodedWithoutEntry.ExpectedCursor != want.ExpectedCursor || decodedWithoutEntry.ID.IsValid() {
		t.Fatalf("snapshot-only Workspace cursor = %#v, %v", decodedWithoutEntry, err)
	}
	for _, invalid := range []string{"", "%%%"} {
		if _, decodeErr := decodeCandidateWorkspaceCursor(invalid); decodeErr == nil {
			t.Fatalf("invalid cursor %q was accepted", invalid)
		}
	}
}

func TestManagerReallowExamAttemptUsesExactSuspensionAndNeverReturnsPrivateReason(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	suspensionID := model.NewAttemptSuspensionID()
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/reallow"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"suspension_id":"`+suspensionID.String()+`","expected_attempt_revision":2,"reason":"manager verified connectivity"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "reallow-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if fake.reallow.SuspensionID != suspensionID || fake.reallow.ExpectedAttemptRevision != 2 ||
		fake.reallow.PrivateReason != "manager verified connectivity" || fake.reallow.IdempotencyKey != "reallow-once" {
		t.Fatalf("reallow command = %#v", fake.reallow)
	}
	if strings.Contains(response.Body.String(), "manager verified connectivity") || strings.Contains(response.Body.String(), "private") ||
		strings.Contains(response.Body.String(), "replayed") {
		t.Fatalf("reallow response exposed internal data: %s", response.Body.String())
	}
}

func TestManagerSuspendExamAttemptUsesRevisionFenceAndNeverReturnsPrivateReason(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/suspend"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_attempt_revision":2,"reason":"candidate left the room"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "kick-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if fake.suspend.AttemptID != fake.attempt.ID || fake.suspend.ExpectedAttemptRevision != 2 ||
		fake.suspend.PrivateReason != "candidate left the room" || fake.suspend.IdempotencyKey != "kick-once" {
		t.Fatalf("suspend command = %#v", fake.suspend)
	}
	body := response.Body.String()
	if strings.Contains(body, "candidate left the room") || strings.Contains(body, "private") || strings.Contains(body, "replayed") ||
		!strings.Contains(body, `"source":"manual"`) || !strings.Contains(body, `"candidate_reason":"manager_suspended"`) ||
		!strings.Contains(body, `"connection_closed":true`) {
		t.Fatalf("suspend response = %s", body)
	}
	missing := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_attempt_revision":0,"reason":"x"}`))
	missing.Header.Set("Authorization", "Bearer credential")
	missing.Header.Set("Idempotency-Key", "kick-invalid")
	invalid := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalid, missing)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision status = %d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestManagerEndExamAttemptUsesRevisionFenceAndReturnsOnlySafeReceipt(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	path := "/api/v1/exams/" + fake.attempt.ExamID.String() + "/sittings/" + fake.attempt.SittingID.String() +
		"/attempts/" + fake.attempt.ID.String() + "/end"
	privateReason := "candidate requested an assisted early end"
	request := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_attempt_revision":1,"reason":"`+privateReason+`"}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "manager-end-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status/cache=%d/%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	if fake.managerEnd.ExamID != fake.attempt.ExamID || fake.managerEnd.SittingID != fake.attempt.SittingID ||
		fake.managerEnd.AttemptID != fake.attempt.ID || fake.managerEnd.ExpectedAttemptRevision != 1 ||
		fake.managerEnd.PrivateReason != privateReason || fake.managerEnd.IdempotencyKey != "manager-end-once" {
		t.Fatalf("manager-end command = %#v", fake.managerEnd)
	}
	if strings.Contains(response.Body.String(), privateReason) || strings.Contains(response.Body.String(), "private") ||
		!strings.Contains(response.Body.String(), fake.submissionReceipt.SubmissionID.String()) {
		t.Fatalf("manager-end response exposed private state or omitted receipt: %s", response.Body.String())
	}

	invalid := newJSONRequest(http.MethodPost, path, strings.NewReader(`{"expected_attempt_revision":1,"reason":"valid","extra":true}`))
	invalid.Header.Set("Authorization", "Bearer credential")
	invalid.Header.Set("Idempotency-Key", "manager-end-invalid")
	invalidResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown member status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), suspension.ID.String()) ||
		!strings.Contains(response.Body.String(), `"candidate_reason":"secure_connectivity_lost"`) ||
		strings.Contains(response.Body.String(), "private_reason") {
		t.Fatalf("manager suspension response = %d %s", response.Code, response.Body.String())
	}
}

func TestCandidateWorkspaceJournalAndAllHTTPMutationsMapExactProtectedCommands(t *testing.T) {
	t.Parallel()
	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	base := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/workspace"
	participationID := fake.participation.ID.String()

	journalRequest := fake.candidateRequest(http.MethodGet, base+"/changes?after_cursor=3&limit=20")
	journalResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, journalRequest)
	if journalResponse.Code != http.StatusOK || fake.journalQuery.AfterCursor != 3 || fake.journalQuery.Limit != 20 ||
		!strings.Contains(journalResponse.Body.String(), `"current_cursor":5`) || !strings.Contains(journalResponse.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("journal=%d %s query=%#v", journalResponse.Code, journalResponse.Body.String(), fake.journalQuery)
	}
	fake.journalPage.Entries[0].Operation = model.AttemptWorkspaceMutationDeleteEntry
	fake.journalPage.Entries[0].EntryKind = model.StarterWorkspaceEntryDirectory
	fake.journalPage.Entries[0].OldPath = "src"
	fake.journalPage.Entries[0].NewPath, fake.journalPage.Entries[0].ContentVersion = "", ""
	fake.journalPage.Entries[0].Recursive = true
	journalResponse = httptest.NewRecorder()
	httpAPI.ServeHTTP(journalResponse, fake.candidateRequest(http.MethodGet, base+"/changes?after_cursor=3&limit=20"))
	if journalResponse.Code != http.StatusOK || !strings.Contains(journalResponse.Body.String(), `"recursive":true`) ||
		!strings.Contains(journalResponse.Body.String(), `"old_path":"src"`) {
		t.Fatalf("recursive journal=%d %s", journalResponse.Code, journalResponse.Body.String())
	}

	directoryBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"path":"src"}`)
	directoryRequest := candidateWorkspaceRequest(fake, http.MethodPost, base+"/directories", directoryBody, "application/json", "mkdir-once")
	directoryResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(directoryResponse, directoryRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, directoryResponse, http.StatusCreated)
	if fake.workspaceDirectory.Path != "src" || fake.workspaceDirectory.Access.ParticipationID != fake.participation.ID ||
		fake.workspaceDirectory.Access.Generation != 1 || fake.workspaceDirectory.IdempotencyKey != "mkdir-once" {
		t.Fatalf("directory command=%#v", fake.workspaceDirectory)
	}

	fileMetadata := map[string]any{"participation_id": participationID, "generation": 1, "path": "empty.txt",
		"media_type": "text/plain", "size": 0, "sha256": strings.Repeat("e", 64)}
	fileBody, fileType := candidateWorkspaceMultipart(t, fileMetadata, nil)
	fileRequest := candidateWorkspaceRequest(fake, http.MethodPost, base+"/files", fileBody, fileType, "file-once")
	fileResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(fileResponse, fileRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, fileResponse, http.StatusCreated)
	if fake.workspaceFile.Size != 0 || fake.workspaceFile.Path != "empty.txt" || len(fake.uploaded) != 0 || fake.workspaceFile.IdempotencyKey != "file-once" {
		t.Fatalf("file command=%#v uploaded=%q", fake.workspaceFile, fake.uploaded)
	}

	entryID := fake.workspacePage.Items[0].EntryID
	moveBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"expected_path":"cmd/main.go","destination_path":"src/main.go"}`)
	moveRequest := candidateWorkspaceRequest(fake, http.MethodPatch, base+"/entries/"+entryID.String(), moveBody, "application/json", "move-once")
	moveResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(moveResponse, moveRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, moveResponse, http.StatusOK)
	if fake.workspaceMove.EntryID != entryID || fake.workspaceMove.ExpectedPath != "cmd/main.go" || fake.workspaceMove.DestinationPath != "src/main.go" {
		t.Fatalf("move command=%#v", fake.workspaceMove)
	}

	version := fake.workspacePage.Items[0].ContentVersion
	replaceMetadata := map[string]any{"participation_id": participationID, "generation": 1, "expected_path": "cmd/main.go",
		"expected_content_version": version.String(), "media_type": "text/plain", "size": 3, "sha256": strings.Repeat("f", 64)}
	replaceBody, replaceType := candidateWorkspaceMultipart(t, replaceMetadata, []byte("new"))
	replaceRequest := candidateWorkspaceRequest(fake, http.MethodPut, base+"/files/"+entryID.String()+"/content", replaceBody, replaceType, "replace-once")
	replaceResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(replaceResponse, replaceRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, replaceResponse, http.StatusOK)
	if fake.workspaceReplace.EntryID != entryID || fake.workspaceReplace.ExpectedContentVersion != version || string(fake.uploaded) != "new" {
		t.Fatalf("replace command=%#v uploaded=%q", fake.workspaceReplace, fake.uploaded)
	}

	deleteBody := strings.NewReader(`{"participation_id":"` + participationID + `","generation":1,"expected_path":"cmd/main.go","expected_content_version":"` + version.String() + `"}`)
	deleteRequest := candidateWorkspaceRequest(fake, http.MethodDelete, base+"/entries/"+entryID.String(), deleteBody, "application/json", "delete-once")
	deleteResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(deleteResponse, deleteRequest)
	assertCandidateWorkspaceMutationHTTP(t, fake, deleteResponse, http.StatusOK)
	if fake.workspaceDelete.EntryID != entryID || fake.workspaceDelete.ExpectedPath != "cmd/main.go" || fake.workspaceDelete.ExpectedContentVersion != version {
		t.Fatalf("delete command=%#v", fake.workspaceDelete)
	}
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
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
	metadata := map[string]any{"participation_id": participationID, "generation": 1, "path": "x", "media_type": "text/plain", "sha256": strings.Repeat("a", 64)}
	body, contentType := candidateWorkspaceMultipart(t, metadata, []byte("x"))
	request := candidateWorkspaceRequest(fake, http.MethodPost, base+"/files", body, contentType, "missing-size")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing size=%d %s", response.Code, response.Body.String())
	}
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
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.status == http.StatusOK && (!fake.workspaceDelete.Recursive || fake.workspaceDelete.ExpectedWorkspaceCursor == nil || *fake.workspaceDelete.ExpectedWorkspaceCursor != 0) {
				t.Fatalf("command=%#v", fake.workspaceDelete)
			}
			if test.status == http.StatusBadRequest && fake.workspaceDelete.EntryID.IsValid() {
				t.Fatal("invalid recursive request reached application")
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
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(metadataPart).Encode(metadata); err != nil {
		t.Fatal(err)
	}
	contentPart, err := writer.CreateFormFile("content", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = contentPart.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func assertCandidateWorkspaceMutationHTTP(t *testing.T, fake *examAttemptHTTPFake, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{fake.credential, "continuity_credential", "credential_hash", "participation_id", "generation", "object_id", "session_id"} {
		if forbidden != "" && strings.Contains(body, forbidden) {
			t.Fatalf("response contains %q: %s", forbidden, body)
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
	presentation              app.CandidateExamPresentation
	workspacePage             app.CandidateExamWorkspacePage
	managerPage               app.ExamAttemptManagerPage
	browserActivityPage       app.BrowserActivityPage
	browserActivityQuery      app.ListBrowserActivityQuery
	correctionAcknowledgement app.AcknowledgeExamCorrectionCommand
	managerEnd                app.EndExamAttemptByManagerCommand
	content                   app.OpenedExamAttemptContent
	workspaceContentVersion   model.WorkspaceContentVersion
	presentationAccess        app.CandidateExamAttemptAccess
	presentationCalls         int
	workspaceQuery            app.ListCandidateExamWorkspaceQuery
	managerList               app.ListExamAttemptsQuery
	managerGet                app.GetExamAttemptQuery
	workspaceOpen             app.OpenCandidateExamWorkspaceFileQuery
	reallow                   app.ReallowExamAttemptCommand
	suspend                   app.SuspendExamAttemptCommand
	journalQuery              app.ListCandidateExamWorkspaceJournalQuery
	journalPage               app.CandidateExamWorkspaceJournalPage
	workspaceDirectory        app.CreateCandidateExamWorkspaceDirectoryCommand
	workspaceFile             app.CreateCandidateExamWorkspaceFileCommand
	workspaceReplace          app.ReplaceCandidateExamWorkspaceFileCommand
	workspaceMove             app.MoveCandidateExamWorkspaceEntryCommand
	workspaceDelete           app.DeleteCandidateExamWorkspaceEntryCommand
	workspaceMutation         app.ExamAttemptWorkspaceMutationResult
	submit                    app.SubmitExamAttemptCommand
	submissionReceipt         app.ExamSubmissionReceipt
	submissionView            app.ExamSubmissionManagerView
	submissionManifest        app.ExamSubmissionManifestPage
	submissionGet             app.GetExamSubmissionQuery
	submissionManifestQuery   app.ListExamSubmissionManifestQuery
	submissionOpen            app.OpenExamSubmissionFileQuery
	uploaded                  []byte
}

func newExamAttemptHTTPFake(t *testing.T) *examAttemptHTTPFake {
	t.Helper()
	at := time.Date(2026, time.August, 17, 9, 0, 0, 123000000, time.UTC)
	attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), model.NewExamID(), model.NewExamSittingID(), model.NewUserID(), model.NewExamRevisionID(), at)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := model.NewExamAttemptWorkspace(model.NewExamAttemptWorkspaceID(), attempt.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	connectionID := model.NewAttemptConnectionID()
	resourceID := model.NewExamResourceID()
	workspaceItem := app.CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile,
		Path: "cmd/main.go", ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 9, SHA256: strings.Repeat("a", 64)}
	principal := testExamHTTPPrincipal()
	fake := &examAttemptHTTPFake{principal: principal, attempt: attempt, workspace: workspace,
		participation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
			Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)},
		connection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at},
		credential: model.NewCredentialToken(), resourceID: resourceID,
		presentation: app.CandidateExamPresentation{AttemptID: attempt.ID, SittingID: attempt.SittingID,
			Capacity:            model.ExamCapacityPolicy{ResourceMaximumCount: 7, ResourceMaximumBytes: 4096, WorkspaceMaximumEntries: 42, WorkspaceMaximumFileBytes: 1024, WorkspaceMaximumTotalBytes: 8192},
			RuntimeCapabilities: candidateTestRuntimeCapabilities(t, at, attempt), Title: "Algorithms",
			InstructionsMarkdown: "Solve safely.", Resources: []examattempt.Resource{{ResourceID: resourceID, DisplayName: "Input",
				DescriptionMarkdown: "Read this.", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 9, SHA256: strings.Repeat("b", 64)}}},
		workspacePage: app.CandidateExamWorkspacePage{WorkspaceID: workspace.ID, Cursor: 4,
			Items: []app.CandidateExamWorkspaceItem{workspaceItem}},
		managerPage: app.ExamAttemptManagerPage{Items: []app.ExamAttemptManagerView{{Attempt: attempt, Workspace: workspace,
			LatestParticipation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
				Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)}, CurrentConnection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at}}}},
		content:                 app.OpenedExamAttemptContent{Body: io.NopCloser(strings.NewReader("protected")), MediaType: "text/plain", SizeBytes: 9, SHA256: strings.Repeat("c", 64)},
		workspaceContentVersion: model.NewWorkspaceContentVersion()}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspace.ID, Cursor: 5, EntryID: workspaceItem.EntryID,
		EntryKind: workspaceItem.Kind, Operation: model.AttemptWorkspaceMutationReplaceFile, OldPath: workspaceItem.Path,
		NewPath: workspaceItem.Path, ContentVersion: workspaceItem.ContentVersion, ChangedAt: at}
	fake.workspaceMutation = app.ExamAttemptWorkspaceMutationResult{AttemptID: attempt.ID, SittingID: attempt.SittingID,
		CandidateUserID: attempt.CandidateUserID, WorkspaceID: workspace.ID, Entry: &workspaceItem, Change: change}
	fake.journalPage = app.CandidateExamWorkspaceJournalPage{WorkspaceID: workspace.ID, CurrentCursor: 5,
		Entries: []model.AttemptWorkspaceJournalEntry{change}}
	sourceID := model.BrowserSourceSessionID("01912ed5-d4f7-4f02-9b97-928b5e8d3f21")
	fake.browserActivityPage = app.BrowserActivityPage{Items: []app.BrowserActivityRecord{{AttemptID: attempt.ID,
		ParticipationID: fake.participation.ID, Generation: 1, SourceSessionID: sourceID,
		Event: model.BrowserActivityEvent{Sequence: 1, Kind: model.BrowserActivityOpened, PolicyRevisionID: model.NewExamRevisionID(),
			ClientOccurredAt: at, ReceivedAt: at.Add(time.Second)}}}}
	fake.submissionReceipt = app.ExamSubmissionReceipt{BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1}, SubmissionID: model.NewSubmissionID(), AttemptID: attempt.ID,
		ExamRevisionID: attempt.AdmissionRevisionID, State: model.ExamAttemptSubmitted, WorkspaceCursor: 4,
		ManifestDigest: strings.Repeat("d", 64), SubmittedAt: at.Add(time.Minute)}
	submissionManifest, err := model.NewExamSubmissionManifest(4, []model.ExamSubmissionManifestEntry{{
		EntryID: workspaceItem.EntryID, Kind: model.StarterWorkspaceEntryFile, Path: workspaceItem.Path,
		ContentVersion: workspaceItem.ContentVersion, MediaType: workspaceItem.MediaType, SizeBytes: workspaceItem.SizeBytes,
		SHA256: workspaceItem.SHA256, StorageOrigin: model.AttemptWorkspaceStorageStarter,
		StarterObjectID: model.NewStarterWorkspaceObjectID(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: fake.submissionReceipt.SubmissionID,
		AttemptID: attempt.ID, ExamRevisionID: attempt.AdmissionRevisionID, WorkspaceID: workspace.ID, Manifest: submissionManifest,
		BrowserActivity: model.BrowserSubmissionSettlement{State: "not_applicable", InventoryRevision: 1},
		Provenance:      model.ExamSubmissionCandidateSubmitted, SubmittedAt: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	fake.submissionView = app.ExamSubmissionManagerView{Authorization: store.ExamSubmissionAuthorization{
		SubmissionID: submission.ID, ExamID: attempt.ExamID, SittingID: attempt.SittingID, AttemptID: attempt.ID,
		CandidateUserID: attempt.CandidateUserID, AcademicUnitID: model.NewAcademicUnitID()}, Submission: *submission}
	fake.submissionManifest = app.ExamSubmissionManifestPage{SubmissionID: submission.ID,
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
	if err != nil {
		t.Fatal(err)
	}
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

func (f *examAttemptHTTPFake) GetExamAttempt(_ context.Context, _ app.Invocation, query app.GetExamAttemptQuery) (app.ExamAttemptManagerView, error) {
	f.managerGet = query
	return f.managerPage.Items[0], nil
}

func (f *examAttemptHTTPFake) ListExamAttempts(_ context.Context, _ app.Invocation, query app.ListExamAttemptsQuery) (app.ExamAttemptManagerPage, error) {
	f.managerList = query
	return f.managerPage, nil
}

func (f *examAttemptHTTPFake) ListSittingCandidateStatuses(context.Context, app.Invocation,
	app.ListSittingCandidateStatusesQuery,
) (app.SittingCandidateStatusesPage, error) {
	return app.SittingCandidateStatusesPage{}, nil
}

func (f *examAttemptHTTPFake) EndExamAttemptByManager(_ context.Context, _ app.Invocation,
	command app.EndExamAttemptByManagerCommand,
) (app.ExamSubmissionReceipt, error) {
	f.managerEnd = command
	return f.submissionReceipt, nil
}

func (f *examAttemptHTTPFake) ListExamAttemptBrowserActivity(_ context.Context, _ app.Invocation,
	query app.ListBrowserActivityQuery,
) (app.BrowserActivityPage, error) {
	f.browserActivityQuery = query
	return f.browserActivityPage, nil
}

func (f *examAttemptHTTPFake) AcknowledgeExamAttemptCorrection(_ context.Context, _ app.Invocation,
	command app.AcknowledgeExamCorrectionCommand,
) (app.ExamCorrectionAcknowledgementResult, error) {
	f.correctionAcknowledgement = command
	capabilities := f.presentation.RuntimeCapabilities
	capabilities.ExamRevision.CurrentRevisionID = command.ExpectedCurrentRevisionID
	return app.ExamCorrectionAcknowledgementResult{CorrectionRevisionID: command.CorrectionRevisionID,
		CurrentRevisionID: command.ExpectedCurrentRevisionID, RuntimeCapabilities: capabilities, AcknowledgedAt: model.OptionalTimeFrom(f.attempt.UpdatedAt.Add(time.Minute))}, nil
}

func (f *examAttemptHTTPFake) ReallowExamAttempt(_ context.Context, invocation app.Invocation, command app.ReallowExamAttemptCommand) (app.ExamAttemptReallowResult, error) {
	f.reallow = command
	attempt := *f.attempt
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptActive, attempt.UpdatedAt.Add(time.Minute), command.ExpectedAttemptRevision+1
	return app.ExamAttemptReallowResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID,
		ClassID: model.NewClassID(), CandidateUserID: attempt.CandidateUserID, Attempt: attempt,
		Suspension: store.ExamAttemptSuspensionView{ID: command.SuspensionID, AttemptID: attempt.ID,
			ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 1,
			State: model.AttemptSuspensionClosed, Source: model.AttemptSuspensionSourcePolicy,
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: attempt.CreatedAt,
			EndedAt: model.OptionalTimeFrom(attempt.UpdatedAt), ReallowedByUserID: invocation.Principal().UserID}}, nil
}

func (f *examAttemptHTTPFake) SuspendExamAttempt(_ context.Context, invocation app.Invocation, command app.SuspendExamAttemptCommand) (app.ExamAttemptManualSuspensionResult, error) {
	f.suspend = command
	attempt := *f.attempt
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptSuspended, attempt.UpdatedAt.Add(time.Minute), command.ExpectedAttemptRevision+1
	return app.ExamAttemptManualSuspensionResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID,
		ClassID: model.NewClassID(), CandidateUserID: attempt.CandidateUserID, Attempt: attempt, ConnectionClosed: true,
		Suspension: store.ExamAttemptSuspensionView{ID: model.NewAttemptSuspensionID(), AttemptID: attempt.ID,
			ParticipationID: model.NewAttemptParticipationID(), Generation: 1, State: model.AttemptSuspensionActive,
			Source: model.AttemptSuspensionSourceManual, CandidateReason: model.AttemptSuspensionCandidateReasonManagerSuspended,
			StartedAt: attempt.UpdatedAt, SuspendedByUserID: invocation.Principal().UserID}}, nil
}

func (f *examAttemptHTTPFake) GetCandidateExamPresentation(_ context.Context, _ app.Invocation, access app.CandidateExamAttemptAccess) (app.CandidateExamPresentation, error) {
	f.presentationAccess, f.presentationCalls = access, f.presentationCalls+1
	return f.presentation, nil
}

func (f *examAttemptHTTPFake) ListCandidateExamWorkspace(_ context.Context, _ app.Invocation, query app.ListCandidateExamWorkspaceQuery) (app.CandidateExamWorkspacePage, error) {
	f.workspaceQuery = query
	return f.workspacePage, nil
}

func (f *examAttemptHTTPFake) ListCandidateExamWorkspaceJournal(_ context.Context, _ app.Invocation, query app.ListCandidateExamWorkspaceJournalQuery) (app.CandidateExamWorkspaceJournalPage, error) {
	f.journalQuery = query
	return f.journalPage, nil
}

func (f *examAttemptHTTPFake) CreateCandidateExamWorkspaceDirectory(_ context.Context, _ app.Invocation, command app.CreateCandidateExamWorkspaceDirectoryCommand) (app.ExamAttemptWorkspaceMutationResult, error) {
	f.workspaceDirectory = command
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) CreateCandidateExamWorkspaceFile(_ context.Context, _ app.Invocation, command app.CreateCandidateExamWorkspaceFileCommand) (app.ExamAttemptWorkspaceMutationResult, error) {
	f.workspaceFile = command
	f.uploaded, _ = io.ReadAll(command.Body)
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) ReplaceCandidateExamWorkspaceFile(_ context.Context, _ app.Invocation, command app.ReplaceCandidateExamWorkspaceFileCommand) (app.ExamAttemptWorkspaceMutationResult, error) {
	f.workspaceReplace = command
	f.uploaded, _ = io.ReadAll(command.Body)
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) MoveCandidateExamWorkspaceEntry(_ context.Context, _ app.Invocation, command app.MoveCandidateExamWorkspaceEntryCommand) (app.ExamAttemptWorkspaceMutationResult, error) {
	f.workspaceMove = command
	return f.workspaceMutation, nil
}

func (f *examAttemptHTTPFake) DeleteCandidateExamWorkspaceEntry(_ context.Context, _ app.Invocation, command app.DeleteCandidateExamWorkspaceEntryCommand) (app.ExamAttemptWorkspaceMutationResult, error) {
	f.workspaceDelete = command
	result := f.workspaceMutation
	result.Entry = nil
	result.Change.Operation = model.AttemptWorkspaceMutationDeleteEntry
	result.Change.NewPath, result.Change.ContentVersion = "", ""
	return result, nil
}

func (f *examAttemptHTTPFake) SubmitExamAttempt(_ context.Context, _ app.Invocation,
	command app.SubmitExamAttemptCommand,
) (app.ExamSubmissionReceipt, error) {
	f.submit = command
	return f.submissionReceipt, nil
}

func (f *examAttemptHTTPFake) GetExamSubmission(_ context.Context, _ app.Invocation,
	query app.GetExamSubmissionQuery,
) (app.ExamSubmissionManagerView, error) {
	f.submissionGet = query
	return f.submissionView, nil
}

func (f *examAttemptHTTPFake) ListExamSubmissionManifest(_ context.Context, _ app.Invocation,
	query app.ListExamSubmissionManifestQuery,
) (app.ExamSubmissionManifestPage, error) {
	f.submissionManifestQuery = query
	return f.submissionManifest, nil
}

func (f *examAttemptHTTPFake) OpenExamSubmissionFile(_ context.Context, _ app.Invocation,
	query app.OpenExamSubmissionFileQuery,
) (app.OpenedExamAttemptContent, error) {
	f.submissionOpen = query
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	f.content.ContentVersion = f.submissionManifest.Items[0].ContentVersion
	f.content.SHA256 = f.submissionManifest.Items[0].SHA256
	f.content.MediaType = f.submissionManifest.Items[0].MediaType
	f.content.SizeBytes = f.submissionManifest.Items[0].SizeBytes
	return f.content, nil
}

func (f *examAttemptHTTPFake) OpenCandidateExamResource(context.Context, app.Invocation, app.OpenCandidateExamResourceQuery) (app.OpenedExamAttemptContent, error) {
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	return f.content, nil
}

func (f *examAttemptHTTPFake) OpenCandidateExamWorkspaceFile(_ context.Context, _ app.Invocation, query app.OpenCandidateExamWorkspaceFileQuery) (app.OpenedExamAttemptContent, error) {
	f.workspaceOpen = query
	f.content.Body = io.NopCloser(strings.NewReader("protected"))
	f.content.ContentVersion = f.workspaceContentVersion
	return f.content, nil
}

func (f *examAttemptHTTPFake) PrepareExamSecurityPreflight(context.Context, app.Invocation, app.PrepareSecurityPreflightCommand) (*app.PreparedSecurityPreflight, error) {
	return nil, errors.New("unexpected preflight prepare")
}
func (f *examAttemptHTTPFake) ReportExamSecurityPreflight(context.Context, app.Invocation, app.ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error) {
	return nil, errors.New("unexpected preflight report")
}

func (f *examAttemptHTTPFake) RecoverExamSecurityPolicy(context.Context, app.Invocation, model.ExamAttemptID) (*app.RecoveredSecurityPolicy, error) {
	return nil, errors.New("unexpected security recovery")
}

func (f *examAttemptHTTPFake) NativeDeliveryStatus(context.Context, app.Invocation, app.NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected NativeDeliveryStatus")
}

func (f *examAttemptHTTPFake) NativeDeliveryReceipt(context.Context, app.Invocation, app.NativeDeliveryQuery, int64) (*model.NativeBatchReceipt, error) {
	return nil, errors.New("unexpected NativeDeliveryReceipt")
}

func (f *examAttemptHTTPFake) DeclareNativeDeliveryGaps(context.Context, app.Invocation, app.NativeDeliveryGapsCommand) (*model.DeliveryGapReceipt, error) {
	return nil, errors.New("unexpected DeclareNativeDeliveryGaps")
}

func (f *examAttemptHTTPFake) SealNativeDelivery(context.Context, app.Invocation, app.NativeDeliveryFinalCommand) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected SealNativeDelivery")
}

func (f *examAttemptHTTPFake) UpdateNativeDeliverySummary(context.Context, app.Invocation, app.NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	return nil, errors.New("unexpected UpdateNativeDeliverySummary")
}

func (f *examAttemptHTTPFake) AppendNativeDelivery(context.Context, app.Invocation, app.NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendNativeDelivery")
}

func (f *examAttemptHTTPFake) BrowserSourceStatus(context.Context, app.Invocation, app.BrowserSourceQuery) (*model.BrowserSourceStatus, error) {
	return nil, apperror.New("exam.attempt.unavailable")
}
func (f *examAttemptHTTPFake) BrowserSourceList(context.Context, app.Invocation, app.BrowserSourceQuery) ([]model.BrowserSourceStatus, error) {
	return nil, apperror.New("exam.attempt.unavailable")
}

func (f *examAttemptHTTPFake) BrowserDeliveryReceipts(context.Context, app.Invocation, app.BrowserSourceQuery, int64, int) (*model.BrowserReceiptPage, error) {
	return nil, errors.New("unexpected BrowserDeliveryReceipts")
}
func (f *examAttemptHTTPFake) DeclareBrowserDeliveryGaps(context.Context, app.Invocation, app.BrowserDeliveryGapsCommand) (*model.BrowserDeliveryGapResult, error) {
	return nil, errors.New("unexpected DeclareBrowserDeliveryGaps")
}
func (f *examAttemptHTTPFake) SealBrowserDelivery(context.Context, app.Invocation, app.BrowserDeliveryFinalCommand) (*model.BrowserSourceStatus, error) {
	return nil, errors.New("unexpected SealBrowserDelivery")
}
func (f *examAttemptHTTPFake) UpdateBrowserDeliverySummary(context.Context, app.Invocation, app.BrowserDeliverySummaryCommand) (*model.BrowserDeliverySummaryResult, error) {
	return nil, errors.New("unexpected UpdateBrowserDeliverySummary")
}
func (f *examAttemptHTTPFake) AppendHistoricalBrowserDelivery(context.Context, app.Invocation, app.BrowserDeliveryAppendCommand) (*model.BrowserActivityAcknowledgement, error) {
	return nil, errors.New("unexpected AppendHistoricalBrowserDelivery")
}

func (f *examAttemptHTTPFake) DeliveryBudget(context.Context, app.Invocation, app.DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error) {
	return nil, errors.New("unexpected DeliveryBudget")
}
func (f *examAttemptHTTPFake) StopDeliveryDetails(context.Context, app.Invocation, app.StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error) {
	return nil, errors.New("unexpected StopDeliveryDetails")
}

// assertNoForbiddenJSONTerms rejects forbidden terms in member names and in
// non-identifier string values. Random opaque IDs are skipped because their
// base32 text can spell a forbidden word by chance.
func assertNoForbiddenJSONTerms(t *testing.T, body []byte, forbidden ...string) {
	t.Helper()
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
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
			if strings.Contains(strings.ToLower(text), term) {
				t.Fatalf("response exposes %q in %q: %s", term, text, body)
			}
		}
	}
}
