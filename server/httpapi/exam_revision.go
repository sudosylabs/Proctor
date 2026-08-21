// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const examRevisionCursorVersion = 1

type ExamRevisionApplication interface {
	PublishExamRevision(context.Context, application.Invocation, application.PublishExamRevisionCommand) (application.ExamRevisionSummary, error)
	GetExamRevision(context.Context, application.Invocation, application.GetExamRevisionQuery) (application.ExamRevisionSummary, error)
	ListExamRevisions(context.Context, application.Invocation, application.ListExamRevisionsQuery) (application.ExamRevisionPage, error)
}

type publishExamRevisionRequest struct {
	ExpectedDraftRevision int64 `json:"expected_draft_revision"`
}

type examRevisionResponse struct {
	ID                         string `json:"id"`
	ExamID                     string `json:"exam_id"`
	Number                     int64  `json:"number"`
	SourceDraftRevision        int64  `json:"source_draft_revision"`
	Title                      string `json:"title"`
	PolicySchemaVersion        int    `json:"policy_schema_version"`
	PolicyDigest               string `json:"policy_digest"`
	StarterWorkspaceDigest     string `json:"starter_workspace_digest"`
	ContentDigest              string `json:"content_digest"`
	ResourceCount              int    `json:"resource_count"`
	StarterWorkspaceEntryCount int    `json:"starter_workspace_entry_count"`
	StarterWorkspaceTotalBytes int64  `json:"starter_workspace_total_bytes"`
	PublishedByUserID          string `json:"published_by_user_id"`
	PublishedAt                string `json:"published_at"`
	BaseRevisionID             string `json:"base_revision_id,omitempty"`
	PublicationKind            string `json:"publication_kind"`
}

type examRevisionListResponse struct {
	Items      []examRevisionResponse `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type examRevisionCursor struct {
	Version    int    `json:"version"`
	Number     int64  `json:"number"`
	RevisionID string `json:"revision_id"`
}

type examRevisionHTTPModule struct{ application ExamRevisionApplication }

func examRevisionResource(application ExamRevisionApplication) resource {
	module := examRevisionHTTPModule{application: application}
	collection := apiPath(literal("exams"), canonicalID("exam_id"), literal("revisions"))
	member := apiPath(literal("exams"), canonicalID("exam_id"), literal("revisions"), canonicalID("exam_revision_id"))
	readErrors := academicReadErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable")
	return newResource(
		"exam-revisions",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, examRevisionMutationErrorCodes(), module.publish),
		principalRoute(http.MethodGet, collection, readErrors, module.list),
		principalRoute(http.MethodGet, member, readErrors, module.get),
	)
}

func examRevisionMutationErrorCodes() []string {
	return academicMutationErrorCodes(
		"request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.conflict",
		"exam.draft.revision_conflict", "exam.revision.no_changes", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
	)
}

func (module examRevisionHTTPModule) publish(request operationRequest) (operationResult, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body publishExamRevisionRequest
	if err = request.decodeJSON(&body, "publishExamRevision"); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError("expected_draft_revision", errors.New("must be positive"))
	}
	summary, err := module.application.PublishExamRevision(request.context, request.invocation(), application.PublishExamRevisionCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examRevisionResponseFromSummary(summary)), nil
}

func (module examRevisionHTTPModule) get(request operationRequest) (operationResult, error) {
	examID, revisionID, err := examRevisionIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	summary, err := module.application.GetExamRevision(request.context, request.invocation(), application.GetExamRevisionQuery{ExamID: examID, RevisionID: revisionID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examRevisionResponseFromSummary(summary)), nil
}

func (module examRevisionHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := application.ListExamRevisionsQuery{ExamID: examID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError("limit", errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamRevisionCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError("cursor", decodeErr)
		}
		query.BeforeNumber = cursor.Number
		query.BeforeRevisionID = model.ExamRevisionID(cursor.RevisionID)
	}
	page, err := module.application.ListExamRevisions(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examRevisionListResponse{Items: make([]examRevisionResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examRevisionResponseFromSummary(item))
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		response.NextCursor = encodeExamRevisionCursor(examRevisionCursor{Number: last.Number, RevisionID: last.ID.String()})
	}
	return jsonResult(http.StatusOK, response), nil
}

func examRevisionExamID(request operationRequest) (model.ExamID, error) {
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

func examRevisionIDs(request operationRequest) (model.ExamID, model.ExamRevisionID, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return "", "", err
	}
	raw, err := request.params.RequireExamRevisionId()
	if err != nil {
		return "", "", err
	}
	revisionID, err := model.ParseExamRevisionID(raw)
	if err != nil {
		return "", "", invalidRequestError("exam_revision_id", err)
	}
	return examID, revisionID, nil
}

func examRevisionResponseFromSummary(summary application.ExamRevisionSummary) examRevisionResponse {
	return examRevisionResponse{
		ID: summary.ID.String(), ExamID: summary.ExamID.String(), Number: summary.Number,
		SourceDraftRevision: summary.SourceDraftRevision, Title: summary.Title,
		PolicySchemaVersion: summary.PolicySchemaVersion, PolicyDigest: summary.PolicyDigest,
		StarterWorkspaceDigest: summary.StarterWorkspaceDigest, ContentDigest: summary.ContentDigest,
		ResourceCount: summary.ResourceCount, StarterWorkspaceEntryCount: summary.StarterWorkspaceEntries,
		StarterWorkspaceTotalBytes: summary.StarterWorkspaceBytes, PublishedByUserID: summary.PublishedByUserID.String(),
		PublishedAt: model.TimeUTC(summary.PublishedAt).Format(time.RFC3339Nano), BaseRevisionID: summary.BaseRevisionID.String(),
		PublicationKind: string(summary.Kind),
	}
}

func encodeExamRevisionCursor(cursor examRevisionCursor) string {
	cursor.Version = examRevisionCursorVersion
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeExamRevisionCursor(raw string) (examRevisionCursor, error) {
	var cursor examRevisionCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, errors.New("invalid Exam Revision cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cursor); err != nil || cursor.Version != examRevisionCursorVersion || cursor.Number < 1 || !model.ExamRevisionID(cursor.RevisionID).IsValid() {
		return cursor, errors.New("invalid Exam Revision cursor")
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return cursor, errors.New("invalid Exam Revision cursor")
	}
	return cursor, nil
}
