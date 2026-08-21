// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examResourceUploadBodyLimit = model.ExamResourceMaximumBytes + 64*1024
const examResourceMetadataLimit = 32 * 1024

type ExamResourceApplication interface {
	CreateExamResource(context.Context, application.Invocation, application.CreateExamResourceCommand) (application.ExamResourceRecord, error)
	ReplaceExamResourceContent(context.Context, application.Invocation, application.ReplaceExamResourceContentCommand) (application.ExamResourceRecord, error)
	EditExamResourceMetadata(context.Context, application.Invocation, application.EditExamResourceMetadataCommand) (application.ExamResourceRecord, error)
	ReorderExamResources(context.Context, application.Invocation, application.ReorderExamResourcesCommand) ([]application.ExamResourceRecord, error)
	RemoveExamResource(context.Context, application.Invocation, application.RemoveExamResourceCommand) (application.ExamResourceRecord, error)
	ListExamResources(context.Context, application.Invocation, application.ListExamResourcesQuery) ([]application.ExamResourceRecord, error)
	OpenExamResource(context.Context, application.Invocation, application.OpenExamResourceQuery) (application.OpenedExamResource, error)
}

type examResourceHTTPModule struct{ application ExamResourceApplication }
type examResourceUploadMetadata struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	DisplayName           string `json:"display_name"`
	DescriptionMarkdown   string `json:"description_markdown"`
	MediaType             string `json:"media_type"`
	Size                  *int64 `json:"size"`
	SHA256                string `json:"sha256"`
}
type examResourceContentReplacementMetadata struct {
	ExpectedDraftRevision int64  `json:"expected_draft_revision"`
	MediaType             string `json:"media_type"`
	Size                  *int64 `json:"size"`
	SHA256                string `json:"sha256"`
}
type editExamResourceMetadataRequest struct {
	ExpectedDraftRevision int64   `json:"expected_draft_revision"`
	DisplayName           *string `json:"display_name"`
	DescriptionMarkdown   *string `json:"description_markdown"`
}
type reorderExamResourcesRequest struct {
	ExpectedDraftRevision int64    `json:"expected_draft_revision"`
	ResourceIDs           []string `json:"resource_ids"`
}
type removeExamResourceRequest struct {
	ExpectedDraftRevision int64 `json:"expected_draft_revision"`
}
type examResourceResponse struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	DescriptionMarkdown string `json:"description_markdown"`
	Position            int    `json:"position"`
	ContentRevisionID   string `json:"content_revision_id"`
	MediaType           string `json:"media_type"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
	UpdatedAt           string `json:"updated_at"`
	DraftRevision       int64  `json:"draft_revision"`
}
type examResourceListResponse struct {
	Items []examResourceResponse `json:"items"`
}

func examResourceHTTPResource(application ExamResourceApplication) resource {
	m := examResourceHTTPModule{application: application}
	collection := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("resources"))
	member := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("resources"), canonicalID("exam_resource_id"))
	order := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("resources"), literal("order"))
	content := apiPath(literal("exams"), canonicalID("exam_id"), literal("draft"), literal("resources"), canonicalID("exam_resource_id"), literal("content"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.resource.invalid", "exam.resource.unavailable")
	return newResource("exam-resources",
		principalRoute(http.MethodGet, collection, readErrors, m.list),
		idempotentProtocolRoute(IdempotencyRequired, examResourceUploadBodyLimit, "exam-resource-upload", RouteProtocolStreamingUpload, AuthPrincipalRequired, http.MethodPost, collection, examResourceMutationErrorCodes("exam.resource.invalid_content", "exam.resource.limit", "exam.resource.upload_invalid", "exam.resource.revision_conflict"), m.create),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPatch, member, examResourceMutationErrorCodes("exam.resource.no_changes", "exam.resource.revision_conflict"), m.editMetadata),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPut, order, examResourceMutationErrorCodes("exam.resource.no_changes", "exam.resource.order_invalid"), m.reorder),
		idempotentProtocolRoute(IdempotencyRequired, examResourceUploadBodyLimit, "exam-resource-content-replacement", RouteProtocolStreamingUpload, AuthPrincipalRequired, http.MethodPut, content, examResourceMutationErrorCodes("exam.resource.invalid_content", "exam.resource.upload_invalid", "exam.resource.revision_conflict"), m.replaceContent),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodDelete, member, examResourceMutationErrorCodes("exam.resource.revision_conflict"), m.remove),
		protocolRoute("exam-resource-protected-content", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet, content, readErrors, m.open))
}

func examResourceMutationErrorCodes(specific ...string) []string {
	common := []string{"request.invalid", "resource.not_found", "exam.archived", "exam.draft.revision_conflict", "exam.resource.invalid", "exam.resource.conflict", "exam.resource.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress"}
	return academicMutationErrorCodes(append(common, specific...)...)
}

func (m examResourceHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examResourceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	items, err := m.application.ListExamResources(request.context, request.invocation(), application.ListExamResourcesQuery{ExamID: examID})
	if err != nil {
		return operationResult{}, err
	}
	response := examResourceListResponse{Items: make([]examResourceResponse, 0, len(items))}
	for i := range items {
		response.Items = append(response.Items, examResourceResponseFromRecord(items[i]))
	}
	return jsonResult(http.StatusOK, response), nil
}
func (m examResourceHTTPModule) create(request operationRequest) (protocolResult, error) {
	examID, err := examResourceExamID(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examResourceUploadMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError("multipart", err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("size is required"))
	}
	result, err := m.application.CreateExamResource(request.context, request.invocation(), application.CreateExamResourceCommand{ExamID: examID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, DisplayName: metadata.DisplayName, DescriptionMarkdown: metadata.DescriptionMarkdown, MediaType: model.ExamResourceMediaType(metadata.MediaType), Body: content, Size: *metadata.Size, ExpectedSHA256: metadata.SHA256, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusCreated, examResourceResponseFromRecord(result)), nil
}
func (m examResourceHTTPModule) replaceContent(request operationRequest) (protocolResult, error) {
	examID, resourceID, err := examResourceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	var metadata examResourceContentReplacementMetadata
	content, err := decodeExamResourceMultipart(request.request, &metadata)
	if err != nil {
		return protocolResult{}, invalidRequestError("multipart", err)
	}
	if metadata.Size == nil {
		return protocolResult{}, invalidRequestError("multipart", errors.New("size is required"))
	}
	result, err := m.application.ReplaceExamResourceContent(request.context, request.invocation(), application.ReplaceExamResourceContentCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: metadata.ExpectedDraftRevision, MediaType: model.ExamResourceMediaType(metadata.MediaType), Body: content, Size: *metadata.Size, ExpectedSHA256: metadata.SHA256, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusOK, examResourceResponseFromRecord(result)), nil
}
func (m examResourceHTTPModule) editMetadata(request operationRequest) (operationResult, error) {
	examID, resourceID, err := examResourceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body editExamResourceMetadataRequest
	if err = request.decodeJSON(&body, "editExamResourceMetadata"); err != nil {
		return operationResult{}, err
	}
	if body.DisplayName == nil && body.DescriptionMarkdown == nil {
		return operationResult{}, invalidRequestError("metadata", errors.New("at least one metadata field is required"))
	}
	result, err := m.application.EditExamResourceMetadata(request.context, request.invocation(), application.EditExamResourceMetadataCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: body.ExpectedDraftRevision, DisplayName: body.DisplayName, DescriptionMarkdown: body.DescriptionMarkdown, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examResourceResponseFromRecord(result)), nil
}
func (m examResourceHTTPModule) reorder(request operationRequest) (operationResult, error) {
	examID, err := examResourceExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body reorderExamResourcesRequest
	if err = request.decodeJSON(&body, "reorderExamResources"); err != nil {
		return operationResult{}, err
	}
	ids := make([]model.ExamResourceID, len(body.ResourceIDs))
	for i, raw := range body.ResourceIDs {
		id, parseErr := model.ParseExamResourceID(raw)
		if parseErr != nil {
			return operationResult{}, invalidRequestError("resource_ids", parseErr)
		}
		ids[i] = id
	}
	items, err := m.application.ReorderExamResources(request.context, request.invocation(), application.ReorderExamResourcesCommand{ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, ResourceIDs: ids, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	response := examResourceListResponse{Items: make([]examResourceResponse, 0, len(items))}
	for i := range items {
		response.Items = append(response.Items, examResourceResponseFromRecord(items[i]))
	}
	return jsonResult(http.StatusOK, response), nil
}
func (m examResourceHTTPModule) remove(request operationRequest) (operationResult, error) {
	examID, resourceID, err := examResourceIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	var body removeExamResourceRequest
	if err = request.decodeJSON(&body, "removeExamResource"); err != nil {
		return operationResult{}, err
	}
	_, err = m.application.RemoveExamResource(request.context, request.invocation(), application.RemoveExamResourceCommand{ExamID: examID, ResourceID: resourceID, ExpectedDraftRevision: body.ExpectedDraftRevision, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}
func (m examResourceHTTPModule) open(request operationRequest) (protocolResult, error) {
	examID, resourceID, err := examResourceIDs(request)
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenExamResource(request.context, request.invocation(), application.OpenExamResourceQuery{ExamID: examID, ResourceID: resourceID})
	if err != nil {
		return protocolResult{}, err
	}
	if opened.Body == nil || opened.Record.Resource == nil || opened.Record.Rendition == nil {
		return protocolResult{}, errors.New("exam resource application returned incomplete content")
	}
	etag := `"` + opened.Record.Rendition.SHA256 + `"`
	headers := http.Header{"Content-Type": {opened.Record.Rendition.MediaType}, "Cache-Control": {"private, max-age=300"}, "ETag": {etag}}
	if request.request.Header.Get("If-None-Match") == etag {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.Record.Rendition.Size).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.Record.Rendition.Size).withHeaders(headers), nil
}

func examResourceExamID(request operationRequest) (model.ExamID, error) {
	raw, err := request.params.RequireExamId()
	if err != nil {
		return "", err
	}
	id, err := model.ParseExamID(raw)
	if err != nil {
		return "", invalidRequestError("exam_id", err)
	}
	return id, nil
}
func examResourceIDs(request operationRequest) (model.ExamID, model.ExamResourceID, error) {
	examID, err := examResourceExamID(request)
	if err != nil {
		return "", "", err
	}
	raw, err := request.params.RequireExamResourceId()
	if err != nil {
		return "", "", err
	}
	resourceID, err := model.ParseExamResourceID(raw)
	if err != nil {
		return "", "", invalidRequestError("exam_resource_id", err)
	}
	return examID, resourceID, nil
}
func examResourceResponseFromRecord(record application.ExamResourceRecord) examResourceResponse {
	r, x := record.Resource, record.Rendition
	if r == nil || x == nil {
		return examResourceResponse{}
	}
	return examResourceResponse{ID: r.ID.String(), DisplayName: r.DisplayName, DescriptionMarkdown: r.DescriptionMarkdown, Position: r.Position, ContentRevisionID: r.SelectedFileRevisionID.String(), MediaType: x.MediaType, Size: x.Size, SHA256: x.SHA256, UpdatedAt: model.TimeUTC(r.UpdatedAt).Format("2006-01-02T15:04:05.999999999Z07:00"), DraftRevision: record.DraftRevision}
}

func decodeExamResourceMultipart[T any](request *http.Request, target *T) (io.Reader, error) {
	if request == nil || target == nil {
		return nil, errors.New("request is required")
	}
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, errors.New("multipart/form-data is required")
	}
	metadata, err := reader.NextPart()
	if err != nil {
		return nil, errors.New("metadata part is required")
	}
	defer metadata.Close()
	if metadata.FormName() != "metadata" || metadata.FileName() != "" {
		return nil, errors.New("metadata must be the first non-file part")
	}
	raw, err := io.ReadAll(io.LimitReader(metadata, examResourceMetadataLimit+1))
	if err != nil || len(raw) > examResourceMetadataLimit {
		return nil, errors.New("metadata is invalid")
	}
	if err = rejectDuplicateTopLevelJSONMembers(raw); err != nil {
		return nil, errors.New("metadata is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return nil, errors.New("metadata is invalid")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("metadata is invalid")
	}
	content, err := reader.NextPart()
	if err != nil {
		return nil, errors.New("content part is required")
	}
	if content.FormName() != "content" {
		_ = content.Close()
		return nil, errors.New("content must be the second part")
	}
	return &exactMultipartContent{part: content, reader: reader}, nil
}

func rejectDuplicateTopLevelJSONMembers(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("metadata must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		member, ok := name.(string)
		if !ok {
			return errors.New("metadata member name is invalid")
		}
		if _, exists := seen[member]; exists {
			return errors.New("metadata contains a duplicate member")
		}
		seen[member] = struct{}{}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("metadata object is incomplete")
	}
	var trailing json.RawMessage
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("metadata contains trailing data")
	}
	return nil
}

type exactMultipartContent struct {
	part   *multipart.Part
	reader *multipart.Reader
	done   bool
}

func (r *exactMultipartContent) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	n, err := r.part.Read(buffer)
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	_ = r.part.Close()
	next, nextErr := r.reader.NextPart()
	if nextErr == nil {
		_ = next.Close()
		r.done = true
		return n, errors.New("unexpected multipart part")
	}
	if !errors.Is(nextErr, io.EOF) {
		r.done = true
		return n, nextErr
	}
	r.done = true
	return n, io.EOF
}
