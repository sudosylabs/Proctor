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

	application "github.com/sudosylabs/proctor/server/app"
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
	body, contentType := examResourceMultipart(t, `{"expected_draft_revision":1,"display_name":"Reference","description_markdown":"Read **this**.","media_type":"text/markdown","size":7,"sha256":"`+checksum+`"}`, raw, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if string(fake.uploaded) != "# Notes" || fake.create.ExpectedSHA256 != checksum || fake.create.IdempotencyKey != "resource-once" || fake.create.MediaType != model.ExamResourceMediaMarkdown {
		t.Fatalf("command=%#v uploaded=%q", fake.create, fake.uploaded)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(`"file_entry"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"path"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"key"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"url"`)) {
		t.Fatalf("response exposed storage details: %s", response.Body.String())
	}
}

func TestExamResourceHTTPRejectsTrailingMultipartPartBeforeApplicationCommit(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	raw := []byte("notes")
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	body, contentType := examResourceMultipart(t, `{"expected_draft_revision":1,"display_name":"Reference","description_markdown":"","media_type":"text/plain","size":5,"sha256":"`+checksum+`"}`, raw, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-once")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || fake.created {
		t.Fatalf("status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
	}
}

func TestExamResourceHTTPRejectsDuplicateOrMissingMultipartMetadata(t *testing.T) {
	t.Parallel()
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte("notes")))
	for _, test := range []struct {
		name     string
		metadata string
	}{
		{name: "duplicate expected revision", metadata: `{"expected_draft_revision":1,"expected_draft_revision":2,"display_name":"Reference","media_type":"text/plain","size":5,"sha256":"` + checksum + `"}`},
		{name: "duplicate checksum", metadata: `{"expected_draft_revision":1,"display_name":"Reference","media_type":"text/plain","size":5,"sha256":"` + checksum + `","sha256":"` + checksum + `"}`},
		{name: "missing size", metadata: `{"expected_draft_revision":1,"display_name":"Reference","media_type":"text/plain","sha256":"` + checksum + `"}`},
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
			if response.Code != http.StatusBadRequest || fake.created {
				t.Fatalf("status=%d created=%v body=%s", response.Code, fake.created, response.Body.String())
			}
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
	body, contentType := examResourceMultipart(t, `{"expected_draft_revision":1,"display_name":"Empty notes","media_type":"text/plain","size":0,"sha256":"`+checksum+`"}`, nil, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/resources", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "resource-empty")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !fake.created || fake.create.Size != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("status=%d created=%v command=%#v uploaded=%q", response.Code, fake.created, fake.create, fake.uploaded)
	}
}

func TestExamResourceHTTPMetadataPatchIsPresenceAware(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/resources/" + fake.record.Resource.ID.String()
	request := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"expected_draft_revision":1,"description_markdown":""}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-metadata")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.edit.DisplayName != nil || fake.edit.DescriptionMarkdown == nil || *fake.edit.DescriptionMarkdown != "" {
		t.Fatalf("status=%d edit=%#v body=%s", response.Code, fake.edit, response.Body.String())
	}

	omitted := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"expected_draft_revision":1}`))
	omitted.Header = request.Header.Clone()
	omittedResponse := httptest.NewRecorder()
	api.ServeHTTP(omittedResponse, omitted)
	if omittedResponse.Code != http.StatusBadRequest || fake.editCalls != 1 {
		t.Fatalf("omitted status=%d edit calls=%d body=%s", omittedResponse.Code, fake.editCalls, omittedResponse.Body.String())
	}
}

func TestExamResourceHTTPRemoveReturnsNoContent(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	principal := testExamHTTPPrincipal()
	fake := newExamResourceHTTPFake(principal)
	api := newFocusedResourceAPI(t, logger, fake, examResourceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/resources/" + fake.record.Resource.ID.String()
	request := httptest.NewRequest(http.MethodDelete, path, strings.NewReader(`{"expected_draft_revision":1}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resource-remove")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || !fake.removed {
		t.Fatalf("status=%d removed=%v body=%q", response.Code, fake.removed, response.Body.String())
	}
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
	if response.Code != http.StatusOK || response.Body.String() != "protected" || response.Header().Get("Content-Disposition") != "" || response.Header().Get("Cache-Control") != "private, max-age=300" || response.Header().Get("ETag") != etag || response.Header().Get("Content-Security-Policy") == "" || len(response.Header().Values("X-Content-Type-Options")) != 1 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	conditional := httptest.NewRequest(http.MethodGet, path, nil)
	conditional.Header.Set("Authorization", "Bearer credential")
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	api.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional=%d body=%q", notModified.Code, notModified.Body.String())
	}
}

type examResourceHTTPFake struct {
	principal model.Principal
	examID    model.ExamID
	record    application.ExamResourceRecord
	create    application.CreateExamResourceCommand
	uploaded  []byte
	created   bool
	edit      application.EditExamResourceMetadataCommand
	editCalls int
	removed   bool
}

func newExamResourceHTTPFake(principal model.Principal) *examResourceHTTPFake {
	examID := model.NewExamID()
	revisionID := model.NewFileRevisionID()
	at := time.Now().UTC()
	resource, _ := model.NewExamResource(model.NewExamResourceID(), examID, model.NewFileEntryID(), revisionID, "Reference", "", 0, at)
	rendition, _ := model.NewFileRendition(model.NewFileRenditionID(), revisionID, "original", "text/plain", 9, 0, 0, strings.Repeat("a", 64), at)
	return &examResourceHTTPFake{principal: principal, examID: examID, record: application.ExamResourceRecord{Resource: resource, Rendition: rendition, DraftRevision: 2}}
}
func (f *examResourceHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	p := f.principal
	return &p, nil
}
func (f *examResourceHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	p := f.principal
	return &p, nil
}
func (f *examResourceHTTPFake) CreateExamResource(_ context.Context, _ application.Invocation, c application.CreateExamResourceCommand) (application.ExamResourceRecord, error) {
	raw, err := io.ReadAll(c.Body)
	if err != nil {
		return application.ExamResourceRecord{}, application.NewError("exam.resource.invalid_content")
	}
	f.create, f.uploaded, f.created = c, raw, true
	return f.record, nil
}
func (f *examResourceHTTPFake) ReplaceExamResourceContent(context.Context, application.Invocation, application.ReplaceExamResourceContentCommand) (application.ExamResourceRecord, error) {
	return f.record, nil
}
func (f *examResourceHTTPFake) EditExamResourceMetadata(_ context.Context, _ application.Invocation, command application.EditExamResourceMetadataCommand) (application.ExamResourceRecord, error) {
	f.edit = command
	f.editCalls++
	return f.record, nil
}
func (f *examResourceHTTPFake) ReorderExamResources(context.Context, application.Invocation, application.ReorderExamResourcesCommand) ([]application.ExamResourceRecord, error) {
	return []application.ExamResourceRecord{f.record}, nil
}
func (f *examResourceHTTPFake) RemoveExamResource(context.Context, application.Invocation, application.RemoveExamResourceCommand) (application.ExamResourceRecord, error) {
	f.removed = true
	return f.record, nil
}
func (f *examResourceHTTPFake) ListExamResources(context.Context, application.Invocation, application.ListExamResourcesQuery) ([]application.ExamResourceRecord, error) {
	return []application.ExamResourceRecord{f.record}, nil
}
func (f *examResourceHTTPFake) OpenExamResource(context.Context, application.Invocation, application.OpenExamResourceQuery) (application.OpenedExamResource, error) {
	opened := application.OpenedExamResource{Record: f.record, Body: io.NopCloser(strings.NewReader("protected"))}
	return opened, nil
}
func examResourceMultipart(t *testing.T, metadata string, content []byte, trailing bool) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("metadata")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(metadata))
	contentPart, err := writer.CreateFormFile("content", "ignored.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = contentPart.Write(content)
	if trailing {
		extra, _ := writer.CreateFormField("unexpected")
		_, _ = extra.Write([]byte("value"))
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}
