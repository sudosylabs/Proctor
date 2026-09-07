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
	"slices"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type ExamExportsApplication interface {
	CreateExamExport(context.Context, application.Invocation, application.CreateExamExportCommand) (*model.ExamExport, error)
	GetExamExport(context.Context, application.Invocation, application.ExamExportQuery) (*model.ExamExport, error)
	OpenExamExport(context.Context, application.Invocation, application.ExamExportQuery) (*application.ExamExportDownload, error)
}

type examExportsHTTPModule struct {
	application ExamExportsApplication
	submission  bool
}

func examExportsResource(app ExamExportsApplication) resource {
	var routes []routeDefinition
	for _, submission := range []bool{false, true} {
		m := examExportsHTTPModule{application: app, submission: submission}
		parts := []pathPart{literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id")}
		if submission {
			parts = append(parts, literal("submissions"), canonicalID("submission_id"))
		}
		parts = append(parts, literal("exports"))
		collection := apiPath(parts...)
		member := apiPath(append(slices.Clone(parts), canonicalID("exam_export_id"))...)
		content := apiPath(append(slices.Clone(member.parts), literal("content"))...)
		create := sessionRoute(http.MethodPost, collection, examExportMutationErrors(), m.create)
		create.idempotency = IdempotencyRequired
		protocol := "sitting-export-content"
		if submission {
			protocol = "submission-export-content"
		}
		routes = append(routes, create, sessionRoute(http.MethodGet, member, examExportReadErrors(), m.get), protocolRoute(protocol, RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, content, append(examExportReadErrors(), "exam.export.expired", "exam.export.not_ready"), m.open))
	}
	return newResource("exam-exports", routes...)
}

func examExportReadErrors() []string {
	return academicReadErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "exam.export.not_found", "exam.export.conflict", "exam.export.unavailable")
}
func examExportMutationErrors() []string {
	return academicMutationErrorCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "exam.export.not_found", "exam.export.conflict", "exam.export.unavailable", "exam.export.policy_unconfigured", "exam.export.limit", "exam.export.source_retired", "exam.export.scope_changed", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
}

type createExamExportRequest struct {
	Categories []model.RetentionCategory `json:"categories"`
}

func (b *createExamExportRequest) UnmarshalJSON(data []byte) error {
	type wire createExamExportRequest
	var value wire
	if err := decodeDuplicateFreeExamIntegrityReviewObject(data, &value); err != nil {
		return err
	}
	*b = createExamExportRequest(value)
	return nil
}

type examExportResponse struct {
	ID                   string                    `json:"id"`
	ExamID               string                    `json:"exam_id"`
	SittingID            string                    `json:"exam_sitting_id"`
	SubmissionID         string                    `json:"submission_id,omitempty"`
	Categories           []model.RetentionCategory `json:"categories"`
	State                model.ExamExportState     `json:"state"`
	PolicyRevision       int64                     `json:"policy_revision"`
	CreatedAt            string                    `json:"created_at"`
	ExpiresAt            string                    `json:"expires_at"`
	ConstructionDeadline string                    `json:"construction_deadline"`
	SubmissionCount      int                       `json:"submission_count"`
	FileCount            int                       `json:"file_count"`
	SourceBytes          int64                     `json:"source_bytes"`
	ArchiveSizeBytes     int64                     `json:"archive_size_bytes,omitempty"`
	ArchiveSHA256        string                    `json:"archive_sha256,omitempty"`
	ReadyAt              string                    `json:"ready_at,omitempty"`
}

func exportResponse(e *model.ExamExport) examExportResponse {
	r := examExportResponse{ID: e.ID.String(), ExamID: e.Scope.ExamID.String(), SittingID: e.Scope.SittingID.String(), SubmissionID: e.Scope.SubmissionID.String(), Categories: slices.Clone(e.Categories), State: e.State, PolicyRevision: e.PolicyRevision,
		CreatedAt: model.TimeUTC(e.CreatedAt).Format(time.RFC3339Nano), ExpiresAt: model.TimeUTC(e.ExpiresAt).Format(time.RFC3339Nano), ConstructionDeadline: model.TimeUTC(e.SourceExpiresAt).Format(time.RFC3339Nano), SubmissionCount: e.SubmissionCount, FileCount: e.FileCount, SourceBytes: e.SourceBytes, ArchiveSizeBytes: e.ArchiveSizeBytes, ArchiveSHA256: e.ArchiveSHA256}
	if e.ReadyAt.Valid {
		r.ReadyAt = model.TimeUTC(e.ReadyAt.Time).Format(time.RFC3339Nano)
	}
	return r
}

func (m examExportsHTTPModule) scope(request operationRequest) (model.RetentionHoldScope, error) {
	if m.submission {
		return recordsSubmissionScope(request)
	}
	exam, sitting, err := examSittingIDs(request)
	return model.RetentionHoldScope{ExamID: exam, SittingID: sitting}, err
}

func (m examExportsHTTPModule) query(request operationRequest) (application.ExamExportQuery, error) {
	scope, err := m.scope(request)
	if err != nil {
		return application.ExamExportQuery{}, err
	}
	raw, err := request.params.RequireExamExportID()
	if err != nil {
		return application.ExamExportQuery{}, err
	}
	id, err := model.ParseExamExportID(raw)
	if err != nil {
		return application.ExamExportQuery{}, invalidRequestError("exam_export_id", err)
	}
	return application.ExamExportQuery{Scope: scope, ExportID: id}, nil
}

func (m examExportsHTTPModule) create(request operationRequest) (operationResult, error) {
	scope, err := m.scope(request)
	if err != nil {
		return operationResult{}, err
	}
	var body createExamExportRequest
	if err = request.decodeJSON(&body, "createExamExport"); err != nil {
		return operationResult{}, err
	}
	categories := slices.Clone(body.Categories)
	if len(categories) == 2 && categories[0] == model.RetentionCategoryIntegrity && categories[1] == model.RetentionCategoryWork {
		categories[0], categories[1] = categories[1], categories[0]
	}
	if !model.ValidExamExportCategories(categories) {
		return operationResult{}, invalidRequestError("categories", errors.New("select distinct work and/or integrity categories"))
	}
	value, err := m.application.CreateExamExport(request.context, request.invocation(), application.CreateExamExportCommand{Scope: scope, Categories: categories, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if value.Validate() != nil || value.Scope != scope {
		return operationResult{}, application.NewError("exam.export.unavailable")
	}
	return jsonResult(http.StatusAccepted, exportResponse(value)).withHeaders(privateNoStoreHeaders()), nil
}

func (m examExportsHTTPModule) get(request operationRequest) (operationResult, error) {
	query, err := m.query(request)
	if err != nil {
		return operationResult{}, err
	}
	value, err := m.application.GetExamExport(request.context, request.invocation(), query)
	if err != nil {
		return operationResult{}, err
	}
	if value.Validate() != nil || value.ID != query.ExportID || value.Scope != query.Scope {
		return operationResult{}, application.NewError("exam.export.unavailable")
	}
	return jsonResult(http.StatusOK, exportResponse(value)).withHeaders(privateNoStoreHeaders()), nil
}

func (m examExportsHTTPModule) open(request operationRequest) (protocolResult, error) {
	query, err := m.query(request)
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenExamExport(request.context, request.invocation(), query)
	if err != nil {
		return protocolResult{}, err
	}
	if opened == nil {
		return protocolResult{}, application.NewError("exam.export.unavailable")
	}
	if opened.Export.Validate() != nil || opened.Export.State != model.ExamExportReady || opened.Export.ID != query.ExportID || opened.Export.Scope != query.Scope || opened.Body == nil {
		if opened.Body != nil {
			_ = opened.Body.Close()
		}
		return protocolResult{}, application.NewError("exam.export.unavailable")
	}
	headers := privateNoStoreHeaders()
	headers.Set("Content-Type", "application/zip")
	headers.Set("Content-Disposition", `attachment; filename="exam-export-`+query.ExportID.String()+`.zip"`)
	headers.Set("ETag", `"`+opened.Export.ArchiveSHA256+`"`)
	return binaryDownloadProtocolResult(opened.Body, opened.Export.ArchiveSizeBytes).withHeaders(headers), nil
}
