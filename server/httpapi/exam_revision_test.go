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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamRevisionHTTPPublishUsesStrictIdempotentCommandAndSafeSummary(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))

	request := newJSONRequest(http.MethodPost, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions", bytes.NewReader([]byte(`{"expected_revision":4}`)))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Idempotency-Key", "publish-once")
	response := httptest.NewRecorder()
	httpAPI.ServeHTTP(response, request)

	require.Equal(t, http.StatusCreated, response.Code, "status = %d, body = %s", response.Code, response.Body.String())
	require.Equal(t, fake.summary.ExamID, fake.publish.ExamID)
	require.Equal(t, int64(4), fake.publish.ExpectedDraftRevision)
	require.Equal(t, "publish-once", fake.publish.IdempotencyKey)
	var got examRevisionResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
	require.Equal(t, fake.summary.ID.String(), got.ID)
	require.Equal(t, int64(3), got.Number)
	require.Equal(t, fake.summary.PolicyDigest, got.PolicyDigest)
	require.Equal(t, 2, got.StarterWorkspaceEntryCount)
	require.Equal(t, int64(37), got.StarterWorkspaceTotalBytes)
	for _, forbidden := range [][]byte{[]byte(`"instructions_markdown"`), []byte(`"policy"`), []byte(`"resources"`), []byte(`"path"`), []byte(`"object_id"`), []byte(`"file_revision_id"`)} {
		require.False(t, bytes.Contains(response.Body.Bytes(), forbidden), "response exposed frozen authored or storage data %q: %s", forbidden, response.Body.String())
	}
}

func TestExamRevisionHTTPGetAndListUseExactIdentityAndVersionedTupleCursor(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	older := fake.summary
	older.ID = model.NewExamRevisionID()
	older.Number = 2
	fake.page = app.ExamRevisionPage{Items: []app.ExamRevisionSummary{fake.summary, older}, HasMore: true}
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions/"+fake.summary.ID.String(), nil)
	getRequest.Header.Set("Authorization", "Bearer credential")
	getResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(getResponse, getRequest)
	require.Equal(t, http.StatusOK, getResponse.Code, "status = %d query = %#v body = %s", getResponse.Code, fake.get, getResponse.Body.String())
	require.Equal(t, fake.summary.ExamID, fake.get.ExamID, "status = %d query = %#v body = %s", getResponse.Code, fake.get, getResponse.Body.String())
	require.Equal(t, fake.summary.ID, fake.get.RevisionID, "status = %d query = %#v body = %s", getResponse.Code, fake.get, getResponse.Body.String())

	beforeRevisionID := model.NewExamRevisionID()
	cursor, err := encodeExamRevisionCursor(examRevisionCursor{Number: 5, RevisionID: beforeRevisionID.String()})
	require.NoError(t, err)
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions?limit=2&cursor="+cursor, nil)
	listRequest.Header.Set("Authorization", "Bearer credential")
	listResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(listResponse, listRequest)
	require.Equal(t, http.StatusOK, listResponse.Code, "status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	require.Equal(t, fake.summary.ExamID, fake.list.ExamID, "status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	require.Equal(t, 2, fake.list.Limit, "status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	require.Equal(t, int64(5), fake.list.BeforeNumber, "status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	require.Equal(t, beforeRevisionID, fake.list.BeforeRevisionID, "status = %d query = %#v body = %s", listResponse.Code, fake.list, listResponse.Body.String())
	malformedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions?cursor=not-a-cursor", nil)
	malformedRequest.Header.Set("Authorization", "Bearer credential")
	malformedResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(malformedResponse, malformedRequest)
	assertHTTPProblem(t, malformedResponse, http.StatusBadRequest, "request.invalid")
	var page examRevisionListResponse
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &page))
	require.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)
	next, err := decodeExamRevisionCursor(page.NextCursor)
	require.NoError(t, err, "next cursor = %#v, err", next)
	require.Equal(t, older.Number, next.Number, "next cursor = %#v, err", next)
	require.Equal(t, older.ID.String(), next.RevisionID, "next cursor = %#v, err", next)

	fake.page.HasMore = false
	terminalResponse := httptest.NewRecorder()
	httpAPI.ServeHTTP(terminalResponse, listRequest.Clone(listRequest.Context()))
	require.Equal(t, http.StatusOK, terminalResponse.Code, "terminal status = %d, body = %s", terminalResponse.Code, terminalResponse.Body.String())
	var terminal examRevisionListResponse
	require.NoError(t, json.Unmarshal(terminalResponse.Body.Bytes(), &terminal))
	require.Empty(t, terminal.NextCursor)
	fake.page = app.ExamRevisionPage{HasMore: true}
	noProgress := httptest.NewRecorder()
	httpAPI.ServeHTTP(noProgress, listRequest.Clone(listRequest.Context()))
	require.Equal(t, http.StatusInternalServerError, noProgress.Code, "no-progress status = %d, body = %s", noProgress.Code, noProgress.Body.String())
}

func TestExamRevisionHTTPCursorAndPublishBodyAreStrict(t *testing.T) {
	t.Parallel()
	revisionID := model.NewExamRevisionID().String()
	unsupported := []byte(`{"version":1,"number":2,"revision_id":"` + revisionID + `"}`)
	valid, _ := json.Marshal(examRevisionCursor{Number: 2, RevisionID: revisionID})
	for name, raw := range map[string]string{
		"malformed":        "not-a-cursor",
		"versioned":        base64.RawURLEncoding.EncodeToString(unsupported),
		"trailing payload": base64.RawURLEncoding.EncodeToString(append(valid, []byte(`{}`)...)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeExamRevisionCursor(raw)
			require.Error(t, err, "cursor was accepted")
		})
	}

	logger, _ := newTestLogger(t)
	fake := newExamRevisionHTTPFake()
	httpAPI := newFocusedResourceAPI(t, logger, fake, examRevisionResource(fake))
	for name, body := range map[string]string{
		"unknown field":    `{"expected_revision":4,"unexpected":true}`,
		"missing revision": `{}`,
		"trailing JSON":    `{"expected_revision":4}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := newJSONRequest(http.MethodPost, "/api/v1/exams/"+fake.summary.ExamID.String()+"/revisions", bytes.NewBufferString(body))
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Idempotency-Key", "publish-once")
			response := httptest.NewRecorder()
			httpAPI.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status = %d command = %#v body = %s", response.Code, fake.publish, response.Body.String())
			require.False(t, fake.publish.ExamID.IsValid(), "status = %d command = %#v body = %s", response.Code, fake.publish, response.Body.String())
		})
	}
}

type examRevisionHTTPFake struct {
	Application
	principal model.Principal
	summary   app.ExamRevisionSummary
	publish   app.PublishExamRevisionCommand
	get       app.GetExamRevisionQuery
	list      app.ListExamRevisionsQuery
	page      app.ExamRevisionPage
}

func newExamRevisionHTTPFake() *examRevisionHTTPFake {
	at := time.Date(2026, time.August, 15, 9, 30, 0, 123456789, time.UTC)
	summary := app.ExamRevisionSummary{
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

func (f *examRevisionHTTPFake) PublishExamRevision(_ context.Context, _ app.Invocation, command app.PublishExamRevisionCommand) (app.ExamRevisionSummary, error) {
	f.publish = command
	return f.summary, nil
}

func (f *examRevisionHTTPFake) GetExamRevision(_ context.Context, _ app.Invocation, query app.GetExamRevisionQuery) (app.ExamRevisionSummary, error) {
	f.get = query
	return f.summary, nil
}

func (f *examRevisionHTTPFake) ListExamRevisions(_ context.Context, _ app.Invocation, query app.ListExamRevisionsQuery) (app.ExamRevisionPage, error) {
	f.list = query
	return f.page, nil
}
