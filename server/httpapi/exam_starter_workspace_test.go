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

func TestExamStarterWorkspaceHTTPFileUploadUsesMetadataFirstStreaming(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	raw := []byte("package main\n")
	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
	body, contentType := examResourceMultipart(t, `{"expected_draft_revision":1,"path":"cmd/main.go","media_type":"text/x-go","size":13,"sha256":"`+checksum+`"}`, raw, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fake.examID.String()+"/draft/starter-workspace/files", body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "workspace-file")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, "status=%d body=%s", response.Code, response.Body.String())
	require.Equal(t, string(raw), string(fake.uploaded), "command=%#v uploaded", fake.createFile)
	require.Equal(t, "cmd/main.go", fake.createFile.Path, "command=%#v uploaded", fake.createFile)
	require.Equal(t, checksum, fake.createFile.ExpectedSHA256, "command=%#v uploaded", fake.createFile)
	require.Equal(t, "workspace-file", fake.createFile.IdempotencyKey, "command=%#v uploaded", fake.createFile)
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte(fake.object.ID.String())))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("object")))
	require.False(t, bytes.Contains(response.Body.Bytes(), []byte("storage")))
}

func TestExamStarterWorkspaceHTTPFileUploadRequiresSizeButAcceptsExplicitZero(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace/files"

	omittedBody, omittedType := examResourceMultipart(t, `{"expected_draft_revision":1,"path":"empty.txt","media_type":"text/plain","sha256":"`+fmt.Sprintf("%x", sha256.Sum256(nil))+`"}`, nil, false)
	omitted := httptest.NewRequest(http.MethodPost, path, omittedBody)
	omitted.Header.Set("Authorization", "Bearer credential")
	omitted.Header.Set("Content-Type", omittedType)
	omitted.Header.Set("Idempotency-Key", "workspace-file-omitted-size")
	omittedResponse := httptest.NewRecorder()
	api.ServeHTTP(omittedResponse, omitted)
	require.Equal(t, http.StatusBadRequest, omittedResponse.Code, "omitted size status=%d command=%#v body=%s", omittedResponse.Code, fake.createFile, omittedResponse.Body.String())
	require.False(t, fake.createFile.ExamID.IsValid(), "omitted size status=%d command=%#v body=%s", omittedResponse.Code, fake.createFile, omittedResponse.Body.String())

	explicitBody, explicitType := examResourceMultipart(t, `{"expected_draft_revision":1,"path":"empty.txt","media_type":"text/plain","size":0,"sha256":"`+fmt.Sprintf("%x", sha256.Sum256(nil))+`"}`, nil, false)
	explicit := httptest.NewRequest(http.MethodPost, path, explicitBody)
	explicit.Header.Set("Authorization", "Bearer credential")
	explicit.Header.Set("Content-Type", explicitType)
	explicit.Header.Set("Idempotency-Key", "workspace-file-empty")
	explicitResponse := httptest.NewRecorder()
	api.ServeHTTP(explicitResponse, explicit)
	require.Equal(t, http.StatusCreated, explicitResponse.Code, "explicit zero status=%d command=%#v body=%s", explicitResponse.Code, fake.createFile, explicitResponse.Body.String())
	require.Equal(t, int64(0), fake.createFile.Size, "explicit zero status=%d command=%#v body=%s", explicitResponse.Code, fake.createFile, explicitResponse.Body.String())
}

func TestExamStarterWorkspaceHTTPFileReplacementRequiresSize(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace/files/" + fake.entry.ID.String() + "/content"
	body, contentType := examResourceMultipart(t, `{"expected_draft_revision":1,"expected_content_version":"`+fake.object.ContentVersion.String()+`","media_type":"text/plain","sha256":"`+fmt.Sprintf("%x", sha256.Sum256(nil))+`"}`, nil, false)
	request := httptest.NewRequest(http.MethodPut, path, body)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Idempotency-Key", "workspace-replace-omitted-size")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, "status=%d command=%#v body=%s", response.Code, fake.replaceFile, response.Body.String())
	require.False(t, fake.replaceFile.EntryID.IsValid(), "status=%d command=%#v body=%s", response.Code, fake.replaceFile, response.Body.String())
}

func TestExamStarterWorkspaceHTTPFileReplacementRequiresAndCarriesExpectedContentVersion(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace/files/" + fake.entry.ID.String() + "/content"
	checksum := fmt.Sprintf("%x", sha256.Sum256(nil))

	omittedBody, omittedType := examResourceMultipart(t, `{"expected_draft_revision":1,"media_type":"text/plain","size":0,"sha256":"`+checksum+`"}`, nil, false)
	omitted := httptest.NewRequest(http.MethodPut, path, omittedBody)
	omitted.Header.Set("Authorization", "Bearer credential")
	omitted.Header.Set("Content-Type", omittedType)
	omitted.Header.Set("Idempotency-Key", "workspace-replace-no-version")
	omittedResponse := httptest.NewRecorder()
	api.ServeHTTP(omittedResponse, omitted)
	require.Equal(t, http.StatusBadRequest, omittedResponse.Code, "omitted status=%d command=%#v body=%s", omittedResponse.Code, fake.replaceFile, omittedResponse.Body.String())
	require.False(t, fake.replaceFile.EntryID.IsValid(), "omitted status=%d command=%#v body=%s", omittedResponse.Code, fake.replaceFile, omittedResponse.Body.String())

	explicitBody, explicitType := examResourceMultipart(t, `{"expected_draft_revision":1,"expected_content_version":"`+fake.object.ContentVersion.String()+`","media_type":"text/plain","size":0,"sha256":"`+checksum+`"}`, nil, false)
	explicit := httptest.NewRequest(http.MethodPut, path, explicitBody)
	explicit.Header.Set("Authorization", "Bearer credential")
	explicit.Header.Set("Content-Type", explicitType)
	explicit.Header.Set("Idempotency-Key", "workspace-replace-version")
	explicitResponse := httptest.NewRecorder()
	api.ServeHTTP(explicitResponse, explicit)
	require.Equal(t, http.StatusOK, explicitResponse.Code, "explicit status=%d command=%#v body=%s", explicitResponse.Code, fake.replaceFile, explicitResponse.Body.String())
	require.Equal(t, fake.object.ContentVersion, fake.replaceFile.ExpectedContentVersion, "explicit status=%d command=%#v body=%s", explicitResponse.Code, fake.replaceFile, explicitResponse.Body.String())
}

func TestExamStarterWorkspaceHTTPDirectoryAndMoveUseStrictJSON(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	base := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace"
	create := httptest.NewRequest(http.MethodPost, base+"/directories", strings.NewReader(`{"expected_draft_revision":1,"path":"cmd"}`))
	create.Header.Set("Authorization", "Bearer credential")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "workspace-directory")
	created := httptest.NewRecorder()
	api.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code, "create=%d command=%#v body=%s", created.Code, fake.createDirectory, created.Body.String())
	require.Equal(t, "cmd", fake.createDirectory.Path, "create=%d command=%#v body=%s", created.Code, fake.createDirectory, created.Body.String())
	move := httptest.NewRequest(http.MethodPatch, base+"/entries/"+fake.entry.ID.String(), strings.NewReader(`{"expected_draft_revision":2,"path":"src/main.go","unexpected":true}`))
	move.Header.Set("Authorization", "Bearer credential")
	move.Header.Set("Content-Type", "application/json")
	move.Header.Set("Idempotency-Key", "workspace-move")
	moved := httptest.NewRecorder()
	api.ServeHTTP(moved, move)
	require.Equal(t, http.StatusBadRequest, moved.Code, "strict move=%d command=%#v body=%s", moved.Code, fake.move, moved.Body.String())
	require.False(t, fake.move.EntryID.IsValid(), "strict move=%d command=%#v body=%s", moved.Code, fake.move, moved.Body.String())
}

func TestExamStarterWorkspaceHTTPContentIsInlinePrivateAndConditional(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace/files/" + fake.entry.ID.String() + "/content"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	etag := `"` + fake.object.SHA256 + `"`
	require.Equal(t, http.StatusOK, response.Code, "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, "protected code", response.Body.String(), "response=%d headers=%v body", response.Code, response.Header())
	require.Empty(t, response.Header().Get("Content-Disposition"), "response=%d headers=%v body", response.Code, response.Header())
	require.Contains(t, response.Header().Values("Content-Security-Policy"), "sandbox", "authored content renders with an opaque origin")
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"), "response=%d headers=%v body", response.Code, response.Header())
	require.Equal(t, etag, response.Header().Get("ETag"), "response=%d headers=%v body", response.Code, response.Header())
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

func TestExamStarterWorkspaceHTTPRemoveReturnsNoContent(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	fake := newExamStarterWorkspaceHTTPFake(testExamHTTPPrincipal())
	api := newFocusedResourceAPI(t, logger, fake, examStarterWorkspaceHTTPResource(fake))
	path := "/api/v1/exams/" + fake.examID.String() + "/draft/starter-workspace/entries/" + fake.entry.ID.String()
	request := httptest.NewRequest(http.MethodDelete, path, strings.NewReader(`{"expected_draft_revision":1}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "workspace-remove")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, "status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
	require.Equal(t, 0, response.Body.Len(), "status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
	require.Equal(t, fake.entry.ID, fake.removeEntry.EntryID, "status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
	request = httptest.NewRequest(http.MethodDelete, path, strings.NewReader(`{"expected_draft_revision":1,"recursive":true}`))
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "workspace-recursive-remove")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, "recursive status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
	require.True(t, fake.removeEntry.Recursive, "recursive status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
	require.Equal(t, int64(1), fake.removeEntry.ExpectedDraftRevision, "recursive status=%d command=%#v body=%q", response.Code, fake.removeEntry, response.Body.String())
}

type examStarterWorkspaceHTTPFake struct {
	principal       model.Principal
	examID          model.ExamID
	entry           model.StarterWorkspaceEntry
	object          model.StarterWorkspaceObject
	createDirectory app.CreateExamStarterWorkspaceDirectoryCommand
	createFile      app.CreateExamStarterWorkspaceFileCommand
	move            app.MoveExamStarterWorkspaceEntryCommand
	replaceFile     app.ReplaceExamStarterWorkspaceFileCommand
	removeEntry     app.RemoveExamStarterWorkspaceEntryCommand
	uploaded        []byte
}

func newExamStarterWorkspaceHTTPFake(principal model.Principal) *examStarterWorkspaceHTTPFake {
	at := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	examID := model.NewExamID()
	object := model.StarterWorkspaceObject{ID: model.NewStarterWorkspaceObjectID(), ExamID: examID, CreatedByUserID: principal.UserID,
		CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(time.Hour), State: model.StarterWorkspaceObjectCurrent,
		ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 14, SHA256: strings.Repeat("a", 64)}
	entry, _ := model.NewStarterWorkspaceFile(model.NewStarterWorkspaceEntryID(), examID, "cmd/main.go", object.ID, at)
	return &examStarterWorkspaceHTTPFake{principal: principal, examID: examID, entry: *entry, object: object}
}
func (f *examStarterWorkspaceHTTPFake) AuthenticateAccess(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}
func (f *examStarterWorkspaceHTTPFake) AuthenticateBearer(context.Context, string) (*model.Principal, error) {
	principal := f.principal
	return &principal, nil
}
func (f *examStarterWorkspaceHTTPFake) result() app.ExamStarterWorkspaceResult {
	return app.ExamStarterWorkspaceResult{Entry: f.entry, Object: &f.object, DraftRevision: 2}
}
func (f *examStarterWorkspaceHTTPFake) ListExamStarterWorkspace(context.Context, app.Invocation, app.ListExamStarterWorkspaceQuery) ([]app.ExamStarterWorkspaceItem, error) {
	return []app.ExamStarterWorkspaceItem{{Entry: f.entry, Object: &f.object}}, nil
}
func (f *examStarterWorkspaceHTTPFake) OpenExamStarterWorkspaceFile(context.Context, app.Invocation, app.OpenExamStarterWorkspaceFileQuery) (app.OpenedExamStarterWorkspaceFile, error) {
	return app.OpenedExamStarterWorkspaceFile{Body: io.NopCloser(strings.NewReader("protected code")), MediaType: f.object.MediaType,
		SizeBytes: 14, SHA256: f.object.SHA256, ContentVersion: f.object.ContentVersion}, nil
}
func (f *examStarterWorkspaceHTTPFake) CreateExamStarterWorkspaceDirectory(_ context.Context, _ app.Invocation, command app.CreateExamStarterWorkspaceDirectoryCommand) (app.ExamStarterWorkspaceResult, error) {
	f.createDirectory = command
	directory, _ := model.NewStarterWorkspaceDirectory(f.entry.ID, f.examID, command.Path, f.entry.CreatedAt)
	return app.ExamStarterWorkspaceResult{Entry: *directory, DraftRevision: 2}, nil
}
func (f *examStarterWorkspaceHTTPFake) CreateExamStarterWorkspaceFile(_ context.Context, _ app.Invocation, command app.CreateExamStarterWorkspaceFileCommand) (app.ExamStarterWorkspaceResult, error) {
	raw, err := io.ReadAll(command.Body)
	if err != nil {
		return app.ExamStarterWorkspaceResult{}, apperror.New("exam.starter_workspace.invalid")
	}
	f.createFile, f.uploaded = command, raw
	return f.result(), nil
}
func (f *examStarterWorkspaceHTTPFake) MoveExamStarterWorkspaceEntry(_ context.Context, _ app.Invocation, command app.MoveExamStarterWorkspaceEntryCommand) (app.ExamStarterWorkspaceResult, error) {
	f.move = command
	return f.result(), nil
}
func (f *examStarterWorkspaceHTTPFake) ReplaceExamStarterWorkspaceFile(_ context.Context, _ app.Invocation, command app.ReplaceExamStarterWorkspaceFileCommand) (app.ExamStarterWorkspaceResult, error) {
	f.replaceFile = command
	return f.result(), nil
}
func (f *examStarterWorkspaceHTTPFake) RemoveExamStarterWorkspaceEntry(_ context.Context, _ app.Invocation, command app.RemoveExamStarterWorkspaceEntryCommand) (app.ExamStarterWorkspaceResult, error) {
	f.removeEntry = command
	return f.result(), nil
}
