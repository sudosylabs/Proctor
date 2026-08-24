// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamRevisionHTTPPublishUsesStrictIdempotentCommandAndSafeSummary(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions", bytes.NewReader([]byte(`{"expected_draft_revision":4}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "publish-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if fake.publish.ExamID != fake.summary.ExamID || fake.publish.ExpectedDraftRevision != 4 || fake.publish.IdempotencyKey != "publish-once" {
		t.Fatalf("command = %#v", fake.publish)
	}
	var got examRevisionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != fake.summary.ID.String() || got.Number != 3 || got.PolicyDigest != fake.summary.PolicyDigest || got.StarterWorkspaceEntryCount != 2 || got.StarterWorkspaceTotalBytes != 37 {
		t.Fatalf("response = %#v", got)
	}
	for _, forbidden := range [][]byte{[]byte(`"instructions_markdown"`), []byte(`"policy"`), []byte(`"resources"`), []byte(`"path"`), []byte(`"object_id"`), []byte(`"file_revision_id"`)} {
		if bytes.Contains(response.Body.Bytes(), forbidden) {
			t.Fatalf("response exposed frozen authored or storage data %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestExamRevisionHTTPGetAndListUseExactIdentityAndVersionedTupleCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	older := fake.summary
	older.ID = model.NewExamRevisionID()
	older.Number = 2
	fake.page = application.ExamRevisionPage{Items: []application.ExamRevisionSummary{fake.summary, older}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions/"+fake.summary.ID.String(), nil)
	getRequest.Header.Set("Authorization", "Bearer credential")
	getResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || fake.get.ExamID != fake.summary.ExamID || fake.get.RevisionID != fake.summary.ID {
		t.Fatalf("status = %d query = %#v body = %s", getResponse.Code, fake.get, getResponse.Body.String())
	}

	beforeRevisionID := model.NewExamRevisionID()
	cursor, err := encodeExamRevisionCursor(examRevisionCursor{Number: 5, RevisionID: beforeRevisionID.String()})
	if err != nil {
		t.Fatal(err)
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions?limit=2&cursor="+cursor, nil)
	listRequest.Header.Set("Authorization", "Bearer credential")
	listResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || fake.list.ExamID != fake.summary.ExamID || fake.list.Limit != 2 || fake.list.BeforeNumber != 5 || fake.list.BeforeRevisionID != beforeRevisionID {
		t.Fatalf("status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	}
	malformedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions?cursor=not-a-cursor", nil)
	malformedRequest.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformedRequest)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
	var page examRevisionListResponse
	if err := json.Unmarshal(listResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	next, err := decodeExamRevisionCursor(page.NextCursor)
	if err != nil || next.Version != examRevisionCursorVersion || next.Number != older.Number || next.RevisionID != older.ID.String() {
		t.Fatalf("next cursor = %#v, err = %v", next, err)
	}

	fake.page.HasMore = false
	terminalResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(terminalResponse, listRequest.Clone(listRequest.Context()))
	if terminalResponse.Code != http.StatusOK {
		t.Fatalf("terminal status = %d, body = %s", terminalResponse.Code, terminalResponse.Body.String())
	}
	var terminal examRevisionListResponse
	if err := json.Unmarshal(terminalResponse.Body.Bytes(), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.NextCursor != "" {
		t.Fatalf("terminal page exposed cursor %q", terminal.NextCursor)
	}
	fake.page = application.ExamRevisionPage{HasMore: true}
	noProgress := httptest.NewRecorder()
	httpAPI.ServeHTTP(noProgress, listRequest.Clone(listRequest.Context()))
	if noProgress.Code != http.StatusInternalServerError {
		t.Fatalf("no-progress status = %d, body = %s", noProgress.Code, noProgress.Body.String())
	}
}

func TestExamRevisionHTTPCursorAndPublishBodyAreStrict(t *testing.T) {
	t.Parallel()
	unsupported, _ := json.Marshal(examRevisionCursor{Version: 2, Number: 2, RevisionID: model.NewExamRevisionID().String()})
	valid, _ := json.Marshal(examRevisionCursor{Version: 1, Number: 2, RevisionID: model.NewExamRevisionID().String()})
	for name, raw := range map[string]string{
		"malformed":           "not-a-cursor",
		"unsupported version": base64.RawURLEncoding.EncodeToString(unsupported),
		"trailing payload":    base64.RawURLEncoding.EncodeToString(append(valid, []byte(`{}`)...)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExamRevisionCursor(raw); err == nil {
				t.Fatal("cursor was accepted")
			}
		})
	}

	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))
	for name, body := range map[string]string{
		"unknown field":    `{"expected_draft_revision":4,"unexpected":true}`,
		"missing revision": `{}`,
		"trailing JSON":    `{"expected_draft_revision":4}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions", bytes.NewBufferString(body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "publish-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || fake.publish.ExamID.IsValid() {
				t.Fatalf("status = %d command = %#v body = %s", response.Code, fake.publish, response.Body.String())
			}
		})
	}
}

type examRevisionHTTPFake struct {
	Application
	principal model.Principal
	summary   application.ExamRevisionSummary
	publish   application.PublishExamRevisionCommand
	get       application.GetExamRevisionQuery
	list      application.ListExamRevisionsQuery
	page      application.ExamRevisionPage
}

func newExamRevisionHTTPFake() *examRevisionHTTPFake {
	at := time.Date(2026, time.August, 15, 9, 30, 0, 123456789, time.UTC)
	summary := application.ExamRevisionSummary{
		ID: model.NewExamRevisionID(), ExamID: model.NewExamID(), Number: 3, SourceDraftRevision: 4,
		Title: "Distributed Systems", PolicySchemaVersion: 1,
		PolicyDigest: strings.Repeat("a", 64), StarterWorkspaceDigest: strings.Repeat("b", 64),
		ContentDigest: strings.Repeat("c", 64), ResourceCount: 1, StarterWorkspaceEntries: 2,
		StarterWorkspaceBytes: 37, PublishedByUserID: model.NewUserID(), PublishedAt: at,
		BaseRevisionID: model.NewExamRevisionID(), Kind: model.ExamRevisionPublicationStandard,
	}
	return &examRevisionHTTPFake{principal: testExamHTTPPrincipal(), summary: summary}
}

func (f *examRevisionHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examRevisionHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}

func (f *examRevisionHTTPFake) PublishExamRevision(_ context.Context, _ application.Invocation, command application.PublishExamRevisionCommand) (application.ExamRevisionSummary, error) {
	f.publish = command
	return f.summary, nil
}

func (f *examRevisionHTTPFake) GetExamRevision(_ context.Context, _ application.Invocation, query application.GetExamRevisionQuery) (application.ExamRevisionSummary, error) {
	f.get = query
	return f.summary, nil
}

func (f *examRevisionHTTPFake) ListExamRevisions(_ context.Context, _ application.Invocation, query application.ListExamRevisionsQuery) (application.ExamRevisionPage, error) {
	f.list = query
	return f.page, nil
}
