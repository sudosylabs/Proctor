// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
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
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 8 ||
		string(payload["focus_loss_collection_enabled"]) != "true" {
		t.Fatalf("presentation shape = %v, %v", payload, err)
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
}

func TestCandidateSubmitExamAttemptUsesStrictCausalSelectorsAndReturnsSafeReceipt(t *testing.T) {
	t.Parallel()

	fake := newExamAttemptHTTPFake(t)
	httpAPI := newExamAttemptFocusedAPI(t, fake)
	body := `{"participation_id":"` + fake.participation.ID.String() + `","generation":1,"expected_workspace_cursor":4,"final_focus_loss_sequence":0}`
	path := "/api/v1/exam-attempts/" + fake.attempt.ID.String() + "/submissions"
	request := fake.candidateRequest(http.MethodPost, path)
	request.Body = io.NopCloser(strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "submit-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" ||
		fake.submit.Access.AttemptID != fake.attempt.ID || fake.submit.Access.ParticipationID != fake.participation.ID ||
		fake.submit.Access.Generation != 1 || fake.submit.ExpectedWorkspaceCursor != 4 ||
		fake.submit.FinalFocusLossSequence != 0 || fake.submit.IdempotencyKey != "submit-once" {
		t.Fatalf("status=%d headers=%v command=%#v body=%s", response.Code, response.Header(), fake.submit, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 6 ||
		string(payload["state"]) != `"submitted"` || string(payload["workspace_cursor"]) != "4" {
		t.Fatalf("receipt=%v error=%v", payload, err)
	}
	for _, forbidden := range []string{"participation", "generation", "connection", "credential", "integrity", "gap", "path", "content", "evidence"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("receipt contains %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestSubmissionManifestCursorIsVersionedStrictAndPathFree(t *testing.T) {
	t.Parallel()

	entryID := model.NewAttemptWorkspaceEntryID()
	raw := encodeSubmissionManifestCursor(submissionManifestCursor{EntryID: entryID})
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
	encoded := encodeExamAttemptManagerCursor(want)
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
	encoded := encodeCandidateWorkspaceCursor(want)
	got, err := decodeCandidateWorkspaceCursor(encoded)
	if err != nil || got != want {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || strings.Contains(string(decoded), "cmd/main.go") || strings.Contains(string(decoded), `"path"`) {
		t.Fatalf("cursor exposes Workspace path: %q, %v", decoded, err)
	}
	for _, invalid := range []string{"", "%%%", encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{ExpectedCursor: -1, ID: want.ID})} {
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
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"suspension_id":"`+suspensionID.String()+`","expected_attempt_revision":2,"reason":"manager verified connectivity"}`))
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

func candidateWorkspaceRequest(fake *examAttemptHTTPFake, method, path string, body io.Reader, contentType, key string) *http.Request {
	request := httptest.NewRequest(method, path, body)
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
	principal               model.Principal
	attempt                 *model.ExamAttempt
	workspace               *model.ExamAttemptWorkspace
	participation           *store.ExamAttemptParticipationView
	connection              *store.ExamAttemptManagerConnection
	credential              string
	resourceID              model.ExamResourceID
	presentation            application.CandidateExamPresentation
	workspacePage           application.CandidateExamWorkspacePage
	managerPage             application.ExamAttemptManagerPage
	content                 application.OpenedExamAttemptContent
	workspaceContentVersion model.WorkspaceContentVersion
	presentationAccess      application.CandidateExamAttemptAccess
	presentationCalls       int
	workspaceQuery          application.ListCandidateExamWorkspaceQuery
	managerList             application.ListExamAttemptsQuery
	managerGet              application.GetExamAttemptQuery
	workspaceOpen           application.OpenCandidateExamWorkspaceFileQuery
	reallow                 application.ReallowExamAttemptCommand
	journalQuery            application.ListCandidateExamWorkspaceJournalQuery
	journalPage             application.CandidateExamWorkspaceJournalPage
	workspaceDirectory      application.CreateCandidateExamWorkspaceDirectoryCommand
	workspaceFile           application.CreateCandidateExamWorkspaceFileCommand
	workspaceReplace        application.ReplaceCandidateExamWorkspaceFileCommand
	workspaceMove           application.MoveCandidateExamWorkspaceEntryCommand
	workspaceDelete         application.DeleteCandidateExamWorkspaceEntryCommand
	workspaceMutation       application.ExamAttemptWorkspaceMutationResult
	submit                  application.SubmitExamAttemptCommand
	submissionReceipt       application.ExamSubmissionReceipt
	submissionView          application.ExamSubmissionManagerView
	submissionManifest      application.ExamSubmissionManifestPage
	submissionGet           application.GetExamSubmissionQuery
	submissionManifestQuery application.ListExamSubmissionManifestQuery
	submissionOpen          application.OpenExamSubmissionFileQuery
	uploaded                []byte
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
	workspaceItem := application.CandidateExamWorkspaceItem{EntryID: model.NewAttemptWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile,
		Path: "cmd/main.go", ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 9, SHA256: strings.Repeat("a", 64)}
	principal := testExamHTTPPrincipal()
	fake := &examAttemptHTTPFake{principal: principal, attempt: attempt, workspace: workspace,
		participation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
			Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)},
		connection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at},
		credential: model.NewCredentialToken(), resourceID: resourceID,
		presentation: application.CandidateExamPresentation{AttemptID: attempt.ID, SittingID: attempt.SittingID,
			AdmissionRevisionID: attempt.AdmissionRevisionID, CurrentRevisionID: model.NewExamRevisionID(), Title: "Algorithms",
			FocusLossCollectionEnabled: true,
			InstructionsMarkdown:       "Solve safely.", Resources: []examattempt.Resource{{ResourceID: resourceID, DisplayName: "Input",
				DescriptionMarkdown: "Read this.", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 9, SHA256: strings.Repeat("b", 64)}}},
		workspacePage: application.CandidateExamWorkspacePage{WorkspaceID: workspace.ID, Cursor: 4,
			Items: []application.CandidateExamWorkspaceItem{workspaceItem}},
		managerPage: application.ExamAttemptManagerPage{Items: []application.ExamAttemptManagerView{{Attempt: attempt, Workspace: workspace,
			LatestParticipation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
				Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)}, CurrentConnection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at}}}},
		content:                 application.OpenedExamAttemptContent{Body: io.NopCloser(strings.NewReader("protected")), MediaType: "text/plain", SizeBytes: 9, SHA256: strings.Repeat("c", 64)},
		workspaceContentVersion: model.NewWorkspaceContentVersion()}
	change := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspace.ID, Cursor: 5, EntryID: workspaceItem.EntryID,
		EntryKind: workspaceItem.Kind, Operation: model.AttemptWorkspaceMutationReplaceFile, OldPath: workspaceItem.Path,
		NewPath: workspaceItem.Path, ContentVersion: workspaceItem.ContentVersion, ChangedAt: at}
	fake.workspaceMutation = application.ExamAttemptWorkspaceMutationResult{AttemptID: attempt.ID, SittingID: attempt.SittingID,
		CandidateUserID: attempt.CandidateUserID, WorkspaceID: workspace.ID, Entry: &workspaceItem, Change: change}
	fake.journalPage = application.CandidateExamWorkspaceJournalPage{WorkspaceID: workspace.ID, CurrentCursor: 5,
		Entries: []model.AttemptWorkspaceJournalEntry{change}}
	fake.submissionReceipt = application.ExamSubmissionReceipt{SubmissionID: model.NewSubmissionID(), AttemptID: attempt.ID,
		State: model.ExamAttemptSubmitted, WorkspaceCursor: 4, ManifestDigest: strings.Repeat("d", 64), SubmittedAt: at.Add(time.Minute)}
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
		AttemptID: attempt.ID, WorkspaceID: workspace.ID, Manifest: submissionManifest, SubmittedAt: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	fake.submissionView = application.ExamSubmissionManagerView{Authorization: store.ExamSubmissionAuthorization{
		SubmissionID: submission.ID, ExamID: attempt.ExamID, SittingID: attempt.SittingID, AttemptID: attempt.ID,
		AcademicUnitID: model.NewAcademicUnitID()}, Submission: *submission}
	fake.submissionManifest = application.ExamSubmissionManifestPage{SubmissionID: submission.ID,
		WorkspaceCursor: submission.WorkspaceCursor, ManifestDigest: submission.ManifestDigest,
		Items: []store.ExamSubmissionManifestItem{{EntryID: workspaceItem.EntryID, Kind: workspaceItem.Kind,
			Path: workspaceItem.Path, ContentVersion: workspaceItem.ContentVersion, MediaType: workspaceItem.MediaType,
			SizeBytes: workspaceItem.SizeBytes, SHA256: workspaceItem.SHA256}}}
	return fake
}

func newExamAttemptFocusedAPI(t *testing.T, fake *examAttemptHTTPFake) *API {
	t.Helper()
	logger, _ := newTestLogger(t)
	return newFocusedResourceAPI(t, logger, fake, examAttemptResource(fake))
}

func (fake *examAttemptHTTPFake) candidateRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set(candidateAttemptCredentialHeader, fake.credential)
	request.Header.Set(candidateAttemptConnectionHeader, fake.connection.ID.String())
	return request
}

func (fake *examAttemptHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := fake.principal
	return &principal, nil
}

func (fake *examAttemptHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := fake.principal
	return &principal, nil
}

func (fake *examAttemptHTTPFake) GetExamAttempt(_ context.Context, _ application.Invocation, query application.GetExamAttemptQuery) (application.ExamAttemptManagerView, error) {
	fake.managerGet = query
	return fake.managerPage.Items[0], nil
}

func (fake *examAttemptHTTPFake) ListExamAttempts(_ context.Context, _ application.Invocation, query application.ListExamAttemptsQuery) (application.ExamAttemptManagerPage, error) {
	fake.managerList = query
	return fake.managerPage, nil
}

func (fake *examAttemptHTTPFake) ReallowExamAttempt(_ context.Context, invocation application.Invocation, command application.ReallowExamAttemptCommand) (application.ExamAttemptReallowResult, error) {
	fake.reallow = command
	attempt := *fake.attempt
	attempt.State, attempt.UpdatedAt, attempt.Revision = model.ExamAttemptActive, attempt.UpdatedAt.Add(time.Minute), command.ExpectedAttemptRevision+1
	return application.ExamAttemptReallowResult{ExamID: attempt.ExamID, SittingID: attempt.SittingID,
		ClassID: model.NewClassID(), CandidateUserID: attempt.CandidateUserID, Attempt: attempt,
		Suspension: store.ExamAttemptSuspensionView{ID: command.SuspensionID, AttemptID: attempt.ID,
			ParticipationID: model.NewAttemptParticipationID(), FlagID: model.NewIntegrityFlagID(), Generation: 1,
			State: model.AttemptSuspensionClosed, Source: model.AttemptSuspensionSourcePolicy,
			CandidateReason: model.AttemptSuspensionCandidateReasonSecureContinuityLost, StartedAt: attempt.CreatedAt,
			EndedAt: model.OptionalTimeFrom(attempt.UpdatedAt), ReallowedByUserID: invocation.Principal().UserID}}, nil
}

func (fake *examAttemptHTTPFake) GetCandidateExamPresentation(_ context.Context, _ application.Invocation, access application.CandidateExamAttemptAccess) (application.CandidateExamPresentation, error) {
	fake.presentationAccess, fake.presentationCalls = access, fake.presentationCalls+1
	return fake.presentation, nil
}

func (fake *examAttemptHTTPFake) ListCandidateExamWorkspace(_ context.Context, _ application.Invocation, query application.ListCandidateExamWorkspaceQuery) (application.CandidateExamWorkspacePage, error) {
	fake.workspaceQuery = query
	return fake.workspacePage, nil
}

func (fake *examAttemptHTTPFake) ListCandidateExamWorkspaceJournal(_ context.Context, _ application.Invocation, query application.ListCandidateExamWorkspaceJournalQuery) (application.CandidateExamWorkspaceJournalPage, error) {
	fake.journalQuery = query
	return fake.journalPage, nil
}

func (fake *examAttemptHTTPFake) CreateCandidateExamWorkspaceDirectory(_ context.Context, _ application.Invocation, command application.CreateCandidateExamWorkspaceDirectoryCommand) (application.ExamAttemptWorkspaceMutationResult, error) {
	fake.workspaceDirectory = command
	return fake.workspaceMutation, nil
}

func (fake *examAttemptHTTPFake) CreateCandidateExamWorkspaceFile(_ context.Context, _ application.Invocation, command application.CreateCandidateExamWorkspaceFileCommand) (application.ExamAttemptWorkspaceMutationResult, error) {
	fake.workspaceFile = command
	fake.uploaded, _ = io.ReadAll(command.Body)
	return fake.workspaceMutation, nil
}

func (fake *examAttemptHTTPFake) ReplaceCandidateExamWorkspaceFile(_ context.Context, _ application.Invocation, command application.ReplaceCandidateExamWorkspaceFileCommand) (application.ExamAttemptWorkspaceMutationResult, error) {
	fake.workspaceReplace = command
	fake.uploaded, _ = io.ReadAll(command.Body)
	return fake.workspaceMutation, nil
}

func (fake *examAttemptHTTPFake) MoveCandidateExamWorkspaceEntry(_ context.Context, _ application.Invocation, command application.MoveCandidateExamWorkspaceEntryCommand) (application.ExamAttemptWorkspaceMutationResult, error) {
	fake.workspaceMove = command
	return fake.workspaceMutation, nil
}

func (fake *examAttemptHTTPFake) DeleteCandidateExamWorkspaceEntry(_ context.Context, _ application.Invocation, command application.DeleteCandidateExamWorkspaceEntryCommand) (application.ExamAttemptWorkspaceMutationResult, error) {
	fake.workspaceDelete = command
	result := fake.workspaceMutation
	result.Entry = nil
	result.Change.Operation = model.AttemptWorkspaceMutationDeleteEntry
	result.Change.NewPath, result.Change.ContentVersion = "", ""
	return result, nil
}

func (fake *examAttemptHTTPFake) SubmitExamAttempt(_ context.Context, _ application.Invocation,
	command application.SubmitExamAttemptCommand,
) (application.ExamSubmissionReceipt, error) {
	fake.submit = command
	return fake.submissionReceipt, nil
}

func (fake *examAttemptHTTPFake) GetExamSubmission(_ context.Context, _ application.Invocation,
	query application.GetExamSubmissionQuery,
) (application.ExamSubmissionManagerView, error) {
	fake.submissionGet = query
	return fake.submissionView, nil
}

func (fake *examAttemptHTTPFake) ListExamSubmissionManifest(_ context.Context, _ application.Invocation,
	query application.ListExamSubmissionManifestQuery,
) (application.ExamSubmissionManifestPage, error) {
	fake.submissionManifestQuery = query
	return fake.submissionManifest, nil
}

func (fake *examAttemptHTTPFake) OpenExamSubmissionFile(_ context.Context, _ application.Invocation,
	query application.OpenExamSubmissionFileQuery,
) (application.OpenedExamAttemptContent, error) {
	fake.submissionOpen = query
	fake.content.Body = io.NopCloser(strings.NewReader("protected"))
	fake.content.ContentVersion = fake.submissionManifest.Items[0].ContentVersion
	fake.content.SHA256 = fake.submissionManifest.Items[0].SHA256
	fake.content.MediaType = fake.submissionManifest.Items[0].MediaType
	fake.content.SizeBytes = fake.submissionManifest.Items[0].SizeBytes
	return fake.content, nil
}

func (fake *examAttemptHTTPFake) OpenCandidateExamResource(context.Context, application.Invocation, application.OpenCandidateExamResourceQuery) (application.OpenedExamAttemptContent, error) {
	fake.content.Body = io.NopCloser(strings.NewReader("protected"))
	return fake.content, nil
}

func (fake *examAttemptHTTPFake) OpenCandidateExamWorkspaceFile(_ context.Context, _ application.Invocation, query application.OpenCandidateExamWorkspaceFileQuery) (application.OpenedExamAttemptContent, error) {
	fake.workspaceOpen = query
	fake.content.Body = io.NopCloser(strings.NewReader("protected"))
	fake.content.ContentVersion = fake.workspaceContentVersion
	return fake.content, nil
}
