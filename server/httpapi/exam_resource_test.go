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
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamResourceHTTPUploadUsesStrictMetadataFirstStreamingContract(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	raw := []byte("# Notes")
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	body, contentType := examResourceMultipart(t, `{"expected_revision":1,"display_name":"Reference","description_markdown":"Read **this**.","media_type":"text/markdown","size":7,"sha256":"`+checksum+`"}`, raw, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d body=%s", response.Code, response.Body.String())
	require.Equal(t, "# Notes", string(fake.uploaded), "command=%#v uploaded", fake.create)
	require.Equal(t, checksum, fake.create.ExpectedSHA256, "command=%#v uploaded", fake.create)
	require.Equal(t, "resource-once", fake.create.IdempotencyKey, "command=%#v uploaded", fake.create)
	require.Equal(t, model.ExamResourceMediaMarkdown, fake.create.MediaType, "command=%#v uploaded", fake.create)
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"file_entry"`)))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"path"`)))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"key"`)))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(`"url"`)))
}

func TestExamResourceHTTPRejectsTrailingMultipartPartBeforeApplicationCommit(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	raw := []byte("notes")
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	body, contentType := examResourceMultipart(t, `{"expected_revision":1,"display_name":"Reference","description_markdown":"","media_type":"text/plain","size":5,"sha256":"`+checksum+`"}`, raw, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
	require.False(t, fake.created, "status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
}

func TestExamResourceHTTPRejectsDuplicateOrMissingMultipartMetadata(t *testing.T) {
	t.Parallel()
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte("notes")))
	for _, test := range []struct {
		name     string
		metadata string
	}{
		{name: "duplicate expected revision", metadata: `{"expected_revision":1,"expected_revision":2,"display_name":"Reference","media_type":"text/plain","size":5,"sha256":"` + checksum + `"}`},
		{name: "duplicate checksum", metadata: `{"expected_revision":1,"display_name":"Reference","media_type":"text/plain","size":5,"sha256":"` + checksum + `","sha256":"` + checksum + `"}`},
		{name: "missing size", metadata: `{"expected_revision":1,"display_name":"Reference","media_type":"text/plain","sha256":"` + checksum + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, _ := newTestLogger(t)
			principal := testExamHTTPPrincipal()
			fake := newExamResourceHTTPFake(principal)
			api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
			body, contentType := examResourceMultipart(t, test.metadata, []byte("notes"), false)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
			request.Header.Set("Authorization", "Bearer credential")
			request.Header.Set("Content-Type", contentType)
			request.Header.Set("Idempotency-Key", "resource-once")
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, "status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
			require.False(t, fake.created, "status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
		})
	}
}

func TestExamResourceHTTPAcceptsExplicitZeroSizeText(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	checksum := fmt.Sprintf("%x", sha256.Sum256(nil))
	body, contentType := examResourceMultipart(t, `{"expected_revision":1,"display_name":"Empty notes","media_type":"text/plain","size":0,"sha256":"`+checksum+`"}`, nil, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-empty")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d created=%v command=%#v uploaded", response.Code, fake.created, fake.create)
	require.True(t, fake.created, "status=%d created=%v command=%#v uploaded", response.Code, fake.created, fake.create)
	require.Equal(t, int64(0), fake.create.Size, "status=%d created=%v command=%#v uploaded", response.Code, fake.created, fake.create)
	require.Empty(t, fake.uploaded, "status=%d created=%v command=%#v uploaded", response.Code, fake.created, fake.create)
}

func TestExamResourceHTTPMetadataPatchIsPresenceAware(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/resources/" + fake.record.Resource.ID.String()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, path, strings.NewReader(`{"expected_revision":1,"description_markdown":""}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-metadata")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, "status=%d edit=%#v body=%s", response.Code, fake.edit, response.Body.String())
	require.Nil(t, fake.edit.DisplayName, "status=%d edit=%#v body=%s", response.Code, fake.edit, response.Body.String())
	require.NotNil(t, fake.edit.DescriptionMarkdown, "status=%d edit=%#v body=%s", response.Code, fake.edit, response.Body.String())
	require.Empty(t, *fake.edit.DescriptionMarkdown, "status=%d edit=%#v body=%s", response.Code, fake.edit, response.Body.String())

	omitted := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, path, strings.NewReader(`{"expected_revision":1}`))
	omitted.Header = request.Header.Clone()
	omittedResponse := httptest.NewRecorder()
	api.ServeHTTP(omittedResponse, omitted)
	require.Equal(t, http.StatusBadRequest, omittedResponse.Code, "omitted status=%d edit calls=%d body=%s", omittedResponse.Code, fake.editCalls, omittedResponse.Body.String())
	require.Equal(t, 1, fake.editCalls, "omitted status=%d edit calls=%d body=%s", omittedResponse.Code, fake.editCalls, omittedResponse.Body.String())
}

func TestExamResourceHTTPRemoveReturnsNoContent(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/resources/" + fake.record.Resource.ID.String()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, path, strings.NewReader(`{"expected_revision":1}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-remove")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, "status=%d removed=%v body=%q", response.Code, fake.removed, response.Body.String())
	require.Equal(t, 0, response.Body.Len(), "status=%d removed=%v body=%q", response.Code, fake.removed, response.Body.String())
	require.True(t, fake.removed, "status=%d removed=%v body=%q", response.Code, fake.removed, response.Body.String())
}

func TestExamResourceHTTPProtectedContentIsInlinePrivateAndConditional(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/resources/" + fake.record.Resource.ID.String() + "/content"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	etag := `"` + fake.record.Rendition.SHA256 + `"`
	require.Equal(t, http.StatusOK, response.Code, "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, "protected", response.Body.String(), "response=%d headers=%v body", response.Code, response.Header())
	require.Empty(t, response.Header().Get("Content-Disposition"), "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, "private, max-age=300", response.Header().Get("Cache-Control"), "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, etag, response.Header().Get("ETag"), "response=%d headers=%v body", response.Code, response.Header())
	require.NotEmpty(t, response.Header().Get("Content-Security-Policy"), "response=%d headers=%v body", response.Code, response.Header())
	require.Len(t, response.Header().Values("X-Content-Type-Options"), 1, "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"), "response=%d headers=%v body", response.Code, response.Header())
	conditional := httptest.NewRequest(http.MethodGet, path, nil)
	conditional.Header.Set("Authorization", "Bearer credential")
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	api.ServeHTTP(notModified, conditional)
	require.Equal(t, http.StatusNotModified, notModified.Code, "conditional=%d body=%q", notModified.Code, notModified.Body.String())
	require.Equal(t, 0, notModified.Body.Len(), "conditional=%d body=%q", notModified.Code, notModified.Body.String())
}

type examResourceHTTPFake struct {
	principal model.Principal
	examID    model.ExamID
	record    app.ExamResourceRecord
	create    app.CreateExamResourceCommand
	uploaded  []byte
	created   bool
	edit      app.EditExamResourceMetadataCommand
	editCalls int
	removed   bool
}

func newExamResourceHTTPFake(principal model.Principal) *examResourceHTTPFake {
	examID := model.NewExamID()
	revisionID := model.NewFileRevisionID()
	at := time.Now().UTC()
	resource, _ := model.NewExamResource(model.NewExamResourceID(), examID, model.NewFileEntryID(), revisionID, "Reference", "", 0, at)
	rendition, _ := model.NewFileRendition(model.NewFileRenditionID(), revisionID, "original", "text/plain", 9, 0, 0, strings.Repeat("a", 64), at)
	return &examResourceHTTPFake{principal: principal, examID: examID, record: app.ExamResourceRecord{Resource: resource, Rendition: rendition, DraftRevision: 2}}
}
func (f *examResourceHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	p := f.principal
	return &p, nil
}
func (f *examResourceHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	p := f.principal
	return &p, nil
}
func (f *examResourceHTTPFake) CreateExamResource(_ context.Context, _ app.Invocation, c app.CreateExamResourceCommand) (app.ExamResourceRecord, error) {
	raw, err := io.ReadAll(c.Body)
	if err != nil {
		return app.ExamResourceRecord{}, apperror.New("exam.resource.content_invalid")
	}
	f.create, f.uploaded, f.created = c, raw, true
	return f.record, nil
}
func (f *examResourceHTTPFake) ReplaceExamResourceContent(context.Context, app.Invocation, app.ReplaceExamResourceContentCommand) (app.ExamResourceRecord, error) {
	return f.record, nil
}
func (f *examResourceHTTPFake) EditExamResourceMetadata(_ context.Context, _ app.Invocation, command app.EditExamResourceMetadataCommand) (app.ExamResourceRecord, error) {
	f.edit = command
	f.editCalls++
	return f.record, nil
}
func (f *examResourceHTTPFake) ReorderExamResources(context.Context, app.Invocation, app.ReorderExamResourcesCommand) ([]app.ExamResourceRecord, error) {
	return []app.ExamResourceRecord{f.record}, nil
}
func (f *examResourceHTTPFake) RemoveExamResource(context.Context, app.Invocation, app.RemoveExamResourceCommand) (app.ExamResourceRecord, error) {
	f.removed = true
	return f.record, nil
}
func (f *examResourceHTTPFake) ListExamResources(context.Context, app.Invocation, app.ListExamResourcesQuery) ([]app.ExamResourceRecord, error) {
	return []app.ExamResourceRecord{f.record}, nil
}
func (f *examResourceHTTPFake) OpenExamResource(context.Context, app.Invocation, app.OpenExamResourceQuery) (app.OpenedExamResource, error) {
	opened := app.OpenedExamResource{Record: f.record, Body: io.NopCloser(strings.NewReader("protected"))}
	return opened, nil
}
func examResourceMultipart(t *testing.T, metadata string, content []byte, trailing bool) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("metadata")
	require.NoError(t, err)
	_, _ = part.Write([]byte(metadata))
	contentPart, err := writer.CreateFormFile("content", "ignored.txt")
	require.NoError(t, err)
	_, _ = contentPart.Write(content)
	if trailing {
		extra, _ := writer.CreateFormField("unexpected")
		_, _ = extra.Write([]byte("value"))
	}
	err = writer.Close()
	require.NoError(t, err)
	return &body, writer.FormDataContentType()
}
