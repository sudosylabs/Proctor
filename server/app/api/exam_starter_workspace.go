// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examStarterWorkspaceUploadBodyLimit = model.StarterWorkspaceMaximumFileBytes + 64*1024

type ExamStarterWorkspaceApplication interface {
	ListExamStarterWorkspace(context.Context, application.Invocation, application.ListExamStarterWorkspaceQuery) ([]application.ExamStarterWorkspaceItem, error)
	OpenExamStarterWorkspaceFile(context.Context, application.Invocation, application.OpenExamStarterWorkspaceFileQuery) (application.OpenedExamStarterWorkspaceFile, error)
	CreateExamStarterWorkspaceDirectory(context.Context, application.Invocation, application.CreateExamStarterWorkspaceDirectoryCommand) (application.ExamStarterWorkspaceResult, error)
	CreateExamStarterWorkspaceFile(context.Context, application.Invocation, application.CreateExamStarterWorkspaceFileCommand) (application.ExamStarterWorkspaceResult, error)
	MoveExamStarterWorkspaceEntry(context.Context, application.Invocation, application.MoveExamStarterWorkspaceEntryCommand) (application.ExamStarterWorkspaceResult, error)
	ReplaceExamStarterWorkspaceFile(context.Context, application.Invocation, application.ReplaceExamStarterWorkspaceFileCommand) (application.ExamStarterWorkspaceResult, error)
	RemoveExamStarterWorkspaceEntry(context.Context, application.Invocation, application.RemoveExamStarterWorkspaceEntryCommand) (application.ExamStarterWorkspaceResult, error)
}

type examStarterWorkspaceHTTPModule struct {
	application ExamStarterWorkspaceApplication
}

type createExamStarterWorkspaceDirectoryRequest struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	Path                  string `json:"path"`
}
type examStarterWorkspaceFileUploadMetadata struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	Path                  string `json:"path"`
	MediaType             string `json:"media_type"`
	Size                  *int64 `json:"size"`
	SHA256                string `json:"sha256"`
}
type examStarterWorkspaceFileReplacementMetadata struct {
	ExpectedDraftRevision  int64  `json:"expected_draft_revision"`
	ExpectedContentVersion string `json:"expected_content_version"`
	MediaType              string `json:"media_type"`
	Size                   *int64 `json:"size"`
	SHA256                 string `json:"sha256"`
}
type moveExamStarterWorkspaceEntryRequest struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	Path                  string `json:"path"`
}
type removeExamStarterWorkspaceEntryRequest struct {
	ExpectedDraftRevision int64 `json:"expected_draft_revision"`
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
type examStarterWorkspaceListResponse struct {
	Items []examStarterWorkspaceListItemResponse `json:"items"`
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
		"exam.starter_workspace.no_changes", "exam.starter_workspace.invalid_move")
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
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress"}
	return academicMutationErrorCodes(append(common, specific...)...)
}

func (module examStarterWorkspaceHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	items, err := module.application.ListExamStarterWorkspace(request.context, request.invocation(), application.ListExamStarterWorkspaceQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	response := examStarterWorkspaceListResponse{Items: make([]examStarterWorkspaceListItemResponse, 0, len(items))}
	for index := range items {
		response.Items = append(response.Items, examStarterWorkspaceListItem(items[index]))
	}
	return jsonResult(http.StatusOK, response), nil
}

func (module examStarterWorkspaceHTTPModule) createDirectory(request operationRequest) (operationResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createExamStarterWorkspaceDirectoryRequest
	if err = request.decodeJSON(&body, "createExamStarterWorkspaceDirectory"); err != nil {
		return operationResult{}, err
	}
	result, err := module.application.CreateExamStarterWorkspaceDirectory(request.context, request.invocation(), application.CreateExamStarterWorkspaceDirectoryCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, Path: body.Path, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examStarterWorkspaceResultResponse(result)), nil
}

func (module examStarterWorkspaceHTTPModule) createFile(request operationRequest) (protocolResult, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examStarterWorkspaceFileUploadMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError("multipart", err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("metadata size is required"))
	}
	result, err := module.application.CreateExamStarterWorkspaceFile(request.context, request.invocation(), application.CreateExamStarterWorkspaceFileCommand{
		ExamID: examID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, Path: metadata.Path, MediaType: metadata.MediaType,
		ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusCreated, examStarterWorkspaceResultResponse(result)), nil
}

func (module examStarterWorkspaceHTTPModule) moveEntry(request operationRequest) (operationResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body moveExamStarterWorkspaceEntryRequest
	if err = request.decodeJSON(&body, "moveExamStarterWorkspaceEntry"); err != nil {
		return operationResult{}, err
	}
	result, err := module.application.MoveExamStarterWorkspaceEntry(request.context, request.invocation(), application.MoveExamStarterWorkspaceEntryCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: body.ExpectedDraftRevision, Path: body.Path, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examStarterWorkspaceResultResponse(result)), nil
}

func (module examStarterWorkspaceHTTPModule) replaceFile(request operationRequest) (protocolResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examStarterWorkspaceFileReplacementMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError("multipart", err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("metadata size is required"))
	}
	expectedContentVersion, err := model.ParseWorkspaceContentVersion(metadata.ExpectedContentVersion)
	if err != nil {
		return protocolResult{}, invalidRequestError("expected_content_version", err)
	}
	result, err := module.application.ReplaceExamStarterWorkspaceFile(request.context, request.invocation(), application.ReplaceExamStarterWorkspaceFileCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, ExpectedContentVersion: expectedContentVersion, MediaType: metadata.MediaType,
		ExpectedSHA256: metadata.SHA256, Body: content, Size: *metadata.Size, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, examStarterWorkspaceResultResponse(result)), nil
}

func (module examStarterWorkspaceHTTPModule) removeEntry(request operationRequest) (operationResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body removeExamStarterWorkspaceEntryRequest
	if err = request.decodeJSON(&body, "removeExamStarterWorkspaceEntry"); err != nil {
		return operationResult{}, err
	}
	_, err = module.application.RemoveExamStarterWorkspaceEntry(request.context, request.invocation(), application.RemoveExamStarterWorkspaceEntryCommand{
		ExamID: examID, EntryID: entryID, ExpectedDraftRevision: body.ExpectedDraftRevision, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (module examStarterWorkspaceHTTPModule) openFile(request operationRequest) (protocolResult, error) {
	examID, entryID, err := examStarterWorkspaceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := module.application.OpenExamStarterWorkspaceFile(request.context, request.invocation(), application.OpenExamStarterWorkspaceFileQuery{ExamID: examID, EntryID: entryID})
	if err != nil {
		return protocolResult{}, err
	}
	if opened.Body == nil {
		return protocolResult{}, errors.New("Starter Workspace application returned incomplete content")
	}
	etag := `"` + opened.SHA256 + `"`
	headers := http.Header{"Content-Type": {opened.MediaType}, "Cache-Control": {"private, no-store"}, "ETag": {etag}}
	if request.request.Header.Get("If-None-Match") == etag {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.SizeBytes).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.SizeBytes).withHeaders(headers), nil
}

func examStarterWorkspaceExamID(request operationRequest) (model.ExamID, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return "", err
	}
	examID, err := model.ParseExamID(raw)
	if err != nil {
		return "", invalidRequestError("exam_id", err)
	}
	return examID, nil
}

func examStarterWorkspaceIDs(request operationRequest) (model.ExamID, model.StarterWorkspaceEntryID, error) {
	examID, err := examStarterWorkspaceExamID(request)
	if err != nil {
		return "", "", err
	}
	raw, err := request.params.RequireStarterWorkspaceEntryId()
	if err != nil {
		return "", "", err
	}
	entryID, err := model.ParseStarterWorkspaceEntryID(raw)
	if err != nil {
		return "", "", invalidRequestError("starter_workspace_entry_id", err)
	}
	return examID, entryID, nil
}

func examStarterWorkspaceResultResponse(result application.ExamStarterWorkspaceResult) examStarterWorkspaceEntryResponse {
	item := examStarterWorkspaceListItem(application.ExamStarterWorkspaceItem{Entry: result.Entry, Object: result.Object})
	return examStarterWorkspaceEntryResponse{ID: item.ID, Kind: item.Kind, Path: item.Path, ContentVersion: item.ContentVersion,
		MediaType: item.MediaType, Size: item.Size, SHA256: item.SHA256, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		DraftRevision: result.DraftRevision}
}

func examStarterWorkspaceListItem(item application.ExamStarterWorkspaceItem) examStarterWorkspaceListItemResponse {
	entry := item.Entry
	response := examStarterWorkspaceListItemResponse{ID: entry.ID.String(), Kind: string(entry.Kind), Path: entry.Path,
		CreatedAt: entry.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: entry.UpdatedAt.Format(time.RFC3339Nano)}
	if item.Object != nil {
		size := item.Object.SizeBytes
		response.ContentVersion = item.Object.ContentVersion.String()
		response.MediaType = item.Object.MediaType
		response.Size = &size
		response.SHA256 = item.Object.SHA256
	}
	return response
}
