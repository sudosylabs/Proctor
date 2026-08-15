// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"io"
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
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 7 {
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
	if err != nil || cursor.Path != fake.workspacePage.Items[0].Path || cursor.ID != fake.workspacePage.Items[0].EntryID {
		t.Fatalf("Workspace cursor = %#v, %v", cursor, err)
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

func TestCandidateWorkspaceCursorBindsCanonicalPathAndEntryIdentity(t *testing.T) {
	t.Parallel()
	want := candidateWorkspaceCursor{Path: "cmd/main.go", ID: model.NewAttemptWorkspaceEntryID()}
	encoded := encodeCandidateWorkspaceCursor(want)
	got, err := decodeCandidateWorkspaceCursor(encoded)
	if err != nil || got != want {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}
	for _, invalid := range []string{"", "%%%", encodeCandidateWorkspaceCursor(candidateWorkspaceCursor{Path: "../escape", ID: want.ID})} {
		if _, decodeErr := decodeCandidateWorkspaceCursor(invalid); decodeErr == nil {
			t.Fatalf("invalid cursor %q was accepted", invalid)
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
	return &examAttemptHTTPFake{principal: principal, attempt: attempt, workspace: workspace,
		participation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
			Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)},
		connection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at},
		credential: model.NewCredentialToken(), resourceID: resourceID,
		presentation: application.CandidateExamPresentation{AttemptID: attempt.ID, SittingID: attempt.SittingID,
			AdmissionRevisionID: attempt.AdmissionRevisionID, CurrentRevisionID: model.NewExamRevisionID(), Title: "Algorithms",
			InstructionsMarkdown: "Solve safely.", Resources: []examattempt.Resource{{ResourceID: resourceID, DisplayName: "Input",
				DescriptionMarkdown: "Read this.", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 9, SHA256: strings.Repeat("b", 64)}}},
		workspacePage: application.CandidateExamWorkspacePage{Items: []application.CandidateExamWorkspaceItem{workspaceItem}},
		managerPage: application.ExamAttemptManagerPage{Items: []application.ExamAttemptManagerView{{Attempt: attempt, Workspace: workspace,
			LatestParticipation: &store.ExamAttemptParticipationView{ID: model.NewAttemptParticipationID(), AttemptID: attempt.ID, State: model.AttemptParticipationActive,
				Generation: 1, StartedAt: at, UpdatedAt: at, LeaseExpiresAt: at.Add(20 * time.Second)}, CurrentConnection: &store.ExamAttemptManagerConnection{ID: connectionID, State: model.AttemptConnectionOpen, OpenedAt: at}}}},
		content:                 application.OpenedExamAttemptContent{Body: io.NopCloser(strings.NewReader("protected")), MediaType: "text/plain", SizeBytes: 9, SHA256: strings.Repeat("c", 64)},
		workspaceContentVersion: model.NewWorkspaceContentVersion()}
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

func (fake *examAttemptHTTPFake) GetCandidateExamPresentation(_ context.Context, _ application.Invocation, access application.CandidateExamAttemptAccess) (application.CandidateExamPresentation, error) {
	fake.presentationAccess, fake.presentationCalls = access, fake.presentationCalls+1
	return fake.presentation, nil
}

func (fake *examAttemptHTTPFake) ListCandidateExamWorkspace(_ context.Context, _ application.Invocation, query application.ListCandidateExamWorkspaceQuery) (application.CandidateExamWorkspacePage, error) {
	fake.workspaceQuery = query
	return fake.workspacePage, nil
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
