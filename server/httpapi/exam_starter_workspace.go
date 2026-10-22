// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examStarterWorkspaceUploadBodyLimit = model.StarterWorkspaceMaximumFileBytes + 64*1024

type ExamStarterWorkspaceApplication interface {
	ListExamStarterWorkspace(context.Context, app.Invocation, app.ListExamStarterWorkspaceQuery) ([]app.ExamStarterWorkspaceItem, error)
	OpenExamStarterWorkspaceFile(context.Context, app.Invocation, app.OpenExamStarterWorkspaceFileQuery) (app.OpenedExamStarterWorkspaceFile, error)
	CreateExamStarterWorkspaceDirectory(context.Context, app.Invocation, app.CreateExamStarterWorkspaceDirectoryCommand) (app.ExamStarterWorkspaceResult, error)
	CreateExamStarterWorkspaceFile(context.Context, app.Invocation, app.CreateExamStarterWorkspaceFileCommand) (app.ExamStarterWorkspaceResult, error)
	MoveExamStarterWorkspaceEntry(context.Context, app.Invocation, app.MoveExamStarterWorkspaceEntryCommand) (app.ExamStarterWorkspaceResult, error)
	ReplaceExamStarterWorkspaceFile(context.Context, app.Invocation, app.ReplaceExamStarterWorkspaceFileCommand) (app.ExamStarterWorkspaceResult, error)
	RemoveExamStarterWorkspaceEntry(context.Context, app.Invocation, app.RemoveExamStarterWorkspaceEntryCommand) (app.ExamStarterWorkspaceResult, error)
}

type examStarterWorkspaceHTTPModule struct {
	application ExamStarterWorkspaceApplication
}

type createExamStarterWorkspaceDirectoryRequest struct {
	ExpectedDraftRevision int64  `json:"expected_revision"`
	Path                  string `json:"path"`
}
type examStarterWorkspaceFileUploadMetadata struct {
	ExpectedDraftRevision int64  `json:"expected_revision"`
	Path                  string `json:"path"`
	MediaType             string `json:"media_type"`
	Size                  *int64 `json:"size"`
	SHA256                string `json:"sha256"`
}
type examStarterWorkspaceFileReplacementMetadata struct {
	ExpectedDraftRevision  int64  `json:"expected_revision"`
	ExpectedContentVersion string `json:"expected_content_version"`
	MediaType              string `json:"media_type"`
	Size                   *int64 `json:"size"`
	SHA256                 string `json:"sha256"`
}
type moveExamStarterWorkspaceEntryRequest struct {
	ExpectedDraftRevision int64  `json:"expected_revision"`
	Path                  string `json:"path"`
}
type removeExamStarterWorkspaceEntryRequest struct {
	ExpectedDraftRevision int64 `json:"expected_revision"`
	Recursive             bool  `json:"recursive,omitempty"`
}
type examStarterWorkspaceEntryResponse struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ContentVersion string `json:"content_version,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	DraftRevision  int64  `json:"draft_revision,omitempty"`
}
type examStarterWorkspaceListItemResponse struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ContentVersion string `json:"content_version,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Size           *int64 `json:"size,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func examStarterWorkspaceHTTPResource(application ExamStarterWorkspaceApplication) resource {
	module := examStarterWorkspaceHTTPModule{application: application}
	collection := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("starter-workspace"))
	directories := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("starter-workspace"), literal("directories"))
	files := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("starter-workspace"), literal("files"))
	entry := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("starter-workspace"), literal("entries"), canonicalID("starter_workspace_entry_id"))
	fileContent := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("starter-workspace"), literal("files"), canonicalID("starter_workspace_entry_id"), literal("content"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.starter_workspace.invalid", "exam.starter_workspace.unavailable")
	directoryErrors := examStarterWorkspaceMutationErrors("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found", "exam.starter_workspace.entry_limit")
	createFileErrors := examStarterWorkspaceMutationErrors("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found",
		"exam.starter_workspace.entry_limit", "exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired", "exam.starter_workspace.object_conflict")
	moveErrors := examStarterWorkspaceMutationErrors("exam.starter_workspace.path_conflict", "exam.starter_workspace.parent_not_found",
		"exam.starter_workspace.no_changes", "exam.starter_workspace.move_conflict")
	replaceErrors := examStarterWorkspaceMutationErrors("exam.starter_workspace.total_size_limit", "exam.starter_workspace.upload_expired",
		"exam.starter_workspace.object_conflict", "exam.starter_workspace.content_conflict", "exam.starter_workspace.entry_kind")
	removeErrors := examStarterWorkspaceMutationErrors("exam.starter_workspace.directory_not_empty")
	return newResource("exam-starter-workspace",
		principalRoute(http.MethodGet, collection, readErrors, module.list),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, directories, directoryErrors, module.createDirectory),
		idempotentProtocolRoute(IdempotencyRequired, examStarterWorkspaceUploadBodyLimit, "exam-starter-workspace-file-upload", RouteProtocolStreamingUpload,
			AuthPrincipalRequired, http.MethodPost, files, createFileErrors, module.createFile),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, entry, moveErrors, module.moveEntry),
		idempotentProtocolRoute(IdempotencyRequired, examStarterWorkspaceUploadBodyLimit, "exam-starter-workspace-file-replacement", RouteProtocolStreamingUpload,
			AuthPrincipalRequired, http.MethodPut, fileContent, replaceErrors, module.replaceFile),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodDelete, entry, removeErrors, module.removeEntry),
		protocolRoute("exam-starter-workspace-protected-content", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet, fileContent, readErrors, module.openFile),
	)
}

func examStarterWorkspaceMutationErrors(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict",
		"exam.starter_workspace.invalid", "exam.starter_workspace.conflict", "exam.starter_workspace.unavailable",
		"idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}
	return academicMutationErrorCodes(append(common, specific...)...)
}

func (m examStarterWorkspaceHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	items, err := m.application.ListExamStarterWorkspace(request.context, request.invocation(), app.ListExamStarterWorkspaceQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	// The Exam Capacity Policy bounds the Starter Workspace, so the complete
	// set is paged in canonical path order.
	response, err := boundedListResult(request, "Starter Workspace", items,
		func(item app.ExamStarterWorkspaceItem) boundedPageKey {
			return boundedPageKey{Name: item.Entry.Path, ID: item.Entry.ID.String()}
		}, examStarterWorkspaceListItem)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m examStarterWorkspaceHTTPModule) createDirectory(request operationRequest) (operationResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createExamStarterWorkspaceDirectoryRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.CreateExamStarterWorkspaceDirectory(request.context, request.invocation(), app.CreateExamStarterWorkspaceDirectoryCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, Path: body.Path, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examStarterWorkspaceResultResponse(result)), nil
}

func (m examStarterWorkspaceHTTPModule) createFile(request operationRequest) (protocolResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examStarterWorkspaceFileUploadMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError(err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError(errors.New("metadata size is required"))
	}
	result, err := m.application.CreateExamStarterWorkspaceFile(request.context, request.invocation(), app.CreateExamStarterWorkspaceFileCommand{
		ExamID: examID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, Path: metadata.Path, MediaType: metadata.MediaType,
		ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusCreated, examStarterWorkspaceResultResponse(result)), nil
}

func (m examStarterWorkspaceHTTPModule) moveEntry(request operationRequest) (operationResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body moveExamStarterWorkspaceEntryRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.MoveExamStarterWorkspaceEntry(request.context, request.invocation(), app.MoveExamStarterWorkspaceEntryCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: body.ExpectedDraftRevision, Path: body.Path, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examStarterWorkspaceResultResponse(result)), nil
}

func (m examStarterWorkspaceHTTPModule) replaceFile(request operationRequest) (protocolResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examStarterWorkspaceFileReplacementMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError(err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError(errors.New("metadata size is required"))
	}
	expectedContentVersion, err := model.ParseWorkspaceContentVersion(metadata.ExpectedContentVersion)
	if err != nil {
		return protocolResult{}, invalidRequestError(err)
	}
	result, err := m.application.ReplaceExamStarterWorkspaceFile(request.context, request.invocation(), app.ReplaceExamStarterWorkspaceFileCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, ExpectedContentVersion: expectedContentVersion, MediaType: metadata.MediaType,
		ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, examStarterWorkspaceResultResponse(result)), nil
}

func (m examStarterWorkspaceHTTPModule) removeEntry(request operationRequest) (operationResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body removeExamStarterWorkspaceEntryRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	_, err = m.application.RemoveExamStarterWorkspaceEntry(request.context, request.invocation(), app.RemoveExamStarterWorkspaceEntryCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: body.ExpectedDraftRevision, Recursive: body.Recursive, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m examStarterWorkspaceHTTPModule) openFile(request operationRequest) (protocolResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenExamStarterWorkspaceFile(request.context, request.invocation(), app.OpenExamStarterWorkspaceFileQuery{ExamID: examID, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	if opened.Body == nil {
		return protocolResult{}, errors.New("Starter Workspace application returned incomplete content")
	}
	etag := `"` + opened.SHA256 + `"`
	headers := http.Header{"Content-Type": {opened.MediaType}, "Cache-Control": {"private, no-store"}, "ETag": {etag}}
	untrustedWorkspaceContentHeaders(headers)
	if request.request.Header.Get("If-None-Match") == etag {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.SizeBytes).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.SizeBytes).withHeaders(headers), nil
}

func examStarterWorkspaceExamID(request operationRequest) (model.ExamID, error) {
	examID, err := request.params.RequireExamID()
	if err != nil {
		return "", err
	}
	return examID, nil
}

func examStarterWorkspaceIDs(request operationRequest) (model.ExamID, model.StarterWorkspaceEntryID, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return "", "", err
	}
	entryID, err := request.params.RequireStarterWorkspaceEntryID()
	if err != nil {
		return "", "", err
	}
	return examID, entryID, nil
}

func examStarterWorkspaceResultResponse(result app.ExamStarterWorkspaceResult) examStarterWorkspaceEntryResponse {
	item := examStarterWorkspaceListItem(app.ExamStarterWorkspaceItem{Entry: result.Entry, Object: result.Object})
	return examStarterWorkspaceEntryResponse{ID: item.ID, Kind: item.Kind, Path: item.Path, ContentVersion: item.ContentVersion,
		MediaType: item.MediaType, Size: item.Size, SHA256: item.SHA256, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		DraftRevision: result.DraftRevision}
}

func examStarterWorkspaceListItem(item app.ExamStarterWorkspaceItem) examStarterWorkspaceListItemResponse {
	entry := item.Entry
	response := examStarterWorkspaceListItemResponse{ID: entry.ID.String(), Kind: string(entry.Kind), Path: entry.Path,
		CreatedAt: model.FormatRFC3339(entry.CreatedAt), UpdatedAt: model.FormatRFC3339(entry.UpdatedAt)}
	if item.Object != nil {
		size := item.Object.SizeBytes
		response.ContentVersion = item.Object.ContentVersion.String()
		response.MediaType = item.Object.MediaType
		response.Size = &size
		response.SHA256 = item.Object.SHA256
	}
	return response
}
