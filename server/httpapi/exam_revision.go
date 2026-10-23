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
	"strconv"

	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamRevisionApplication interface {
	PublishExamRevision(context.Context, invocation.Invocation, exam.PublishRevisionCommand) (store.ExamRevisionSummary, error)
	GetExamRevision(context.Context, invocation.Invocation, exam.GetRevisionQuery) (store.ExamRevisionSummary, error)
	ListExamRevisions(context.Context, invocation.Invocation, exam.ListRevisionsQuery) (exam.RevisionPage, error)
}

type publishExamRevisionRequest struct {
	ExpectedDraftRevision int64 `json:"expected_revision"`
}

type examRevisionResponse struct {
	ID                         string                     `json:"id"`
	ExamID                     string                     `json:"exam_id"`
	Number                     int64                      `json:"number"`
	SourceDraftRevision        int64                      `json:"source_draft_revision"`
	Title                      string                     `json:"title"`
	PolicySchemaVersion        int                        `json:"policy_schema_version"`
	PolicyDigest               string                     `json:"policy_digest"`
	Capacity                   examCapacityPolicyResponse `json:"capacity"`
	StarterWorkspaceDigest     string                     `json:"starter_workspace_digest"`
	ContentDigest              string                     `json:"content_digest"`
	ResourceCount              int                        `json:"resource_count"`
	StarterWorkspaceEntryCount int                        `json:"starter_workspace_entry_count"`
	StarterWorkspaceTotalBytes int64                      `json:"starter_workspace_total_bytes"`
	PublishedByUserID          string                     `json:"published_by_user_id"`
	PublishedAt                string                     `json:"published_at"`
	BaseRevisionID             string                     `json:"base_revision_id,omitempty"`
	PublicationKind            string                     `json:"publication_kind"`
}

type examRevisionListResponse struct {
	Items      []examRevisionResponse `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type examRevisionCursor struct {
	Number     int64  `json:"number"`
	RevisionID string `json:"revision_id"`
}

type examRevisionHTTPModule struct{ application ExamRevisionApplication }

func examRevisionResource(application ExamRevisionApplication) apiResource {
	module := examRevisionHTTPModule{application: application}
	collection := apiPath(literal("exams"), canonicalID("exam_id"), literal("revisions"))
	member := apiPath(literal("exams"), canonicalID("exam_id"), literal("revisions"), canonicalID("exam_revision_id"))
	return newResource(
		"exam-revisions",
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, collection, []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.conflict", "exam.draft.revision_conflict", "exam.revision.no_changes", "exam.revision.capacity_exceeded", "exam.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}, module.publish),
		principalRoute(http.MethodGet, collection, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"}, module.list),
		principalRoute(http.MethodGet, member, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"}, module.get),
	)
}

func (m examRevisionHTTPModule) publish(request operationRequest) (operationResult, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	var body publishExamRevisionRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if body.ExpectedDraftRevision < 1 {
		return operationResult{}, invalidRequestError(errors.New("must be positive"))
	}
	summary, err := m.application.PublishExamRevision(request.context, request.invocation(), exam.PublishRevisionCommand{
		ExamID: examID, ExpectedDraftRevision: body.ExpectedDraftRevision, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, examRevisionResponseFromSummary(summary)), nil
}

func (m examRevisionHTTPModule) get(request operationRequest) (operationResult, error) {
	examID, revisionID, err := examRevisionIDs(request)
	if err != nil {
		return operationResult{}, err
	}
	summary, err := m.application.GetExamRevision(request.context, request.invocation(), exam.GetRevisionQuery{ExamID: examID, RevisionID: revisionID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, examRevisionResponseFromSummary(summary)), nil
}

func (m examRevisionHTTPModule) list(request operationRequest) (operationResult, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return operationResult{}, err
	}
	query := exam.ListRevisionsQuery{ExamID: examID, Limit: 50}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 200 {
			return operationResult{}, invalidRequestError(errors.New("must be between 1 and 200"))
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeExamRevisionCursor(raw)
		if decodeErr != nil {
			return operationResult{}, invalidRequestError(decodeErr)
		}
		query.BeforeNumber = cursor.Number
		query.BeforeRevisionID = model.ExamRevisionID(cursor.RevisionID)
	}
	page, err := m.application.ListExamRevisions(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	response := examRevisionListResponse{Items: make([]examRevisionResponse, 0, len(page.Items))}
	for _, item := range page.Items {
		response.Items = append(response.Items, examRevisionResponseFromSummary(item))
	}
	if page.HasMore && len(page.Items) == 0 {
		return operationResult{}, unpresentableResult(errors.New("revision page made no progress"))
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		response.NextCursor, err = encodeExamRevisionCursor(examRevisionCursor{Number: last.Number, RevisionID: last.ID.String()})
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

func examRevisionExamID(request operationRequest) (model.ExamID, error) {
	id, err := request.params.RequireExamID()
	if err != nil {
		return "", err
	}
	return id, nil
}

func examRevisionIDs(request operationRequest) (model.ExamID, model.ExamRevisionID, error) {
	examID, err := examRevisionExamID(request)
	if err != nil {
		return "", "", err
	}
	revisionID, err := request.params.RequireExamRevisionID()
	if err != nil {
		return "", "", err
	}
	return examID, revisionID, nil
}

func examRevisionResponseFromSummary(summary store.ExamRevisionSummary) examRevisionResponse {
	return examRevisionResponse{
		ID: summary.ID.String(), ExamID: summary.ExamID.String(), Number: summary.Number,
		SourceDraftRevision: summary.SourceDraftRevision, Title: summary.Title,
		PolicySchemaVersion: summary.PolicySchemaVersion, PolicyDigest: summary.PolicyDigest,
		Capacity:               examCapacityPolicyResponseFromModel(summary.Capacity),
		StarterWorkspaceDigest: summary.StarterWorkspaceDigest, ContentDigest: summary.ContentDigest,
		ResourceCount: summary.ResourceCount, StarterWorkspaceEntryCount: summary.StarterWorkspaceEntries,
		StarterWorkspaceTotalBytes: summary.StarterWorkspaceBytes, PublishedByUserID: summary.PublishedByUserID.String(),
		PublishedAt: model.FormatRFC3339(summary.PublishedAt), BaseRevisionID: summary.BaseRevisionID.String(),
		PublicationKind: string(summary.Kind),
	}
}

func encodeExamRevisionCursor(cursor examRevisionCursor) (string, error) {
	return encodeOpaqueCursor(cursor, examRevisionCursorSpec())
}

func decodeExamRevisionCursor(raw string) (examRevisionCursor, error) {
	return decodeOpaqueCursor(raw, examRevisionCursorSpec())
}

func examRevisionCursorSpec() opaqueCursorSpec[examRevisionCursor] {
	return opaqueCursorSpec[examRevisionCursor]{
		label: "Exam Revision", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"number", "revision_id"},
		validate: func(cursor examRevisionCursor) error {
			if cursor.Number < 1 || !model.ExamRevisionID(cursor.RevisionID).IsValid() {
				return errors.New("invalid Exam Revision keyset")
			}
			return nil
		},
	}
}
