// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type OnboardingImportApplication interface {
	UploadOnboardingImport(context.Context, application.Invocation, application.UploadOnboardingImportCommand) (application.OnboardingImportView, error)
	GetOnboardingImport(context.Context, application.Invocation, string) (application.OnboardingImportView, []application.OnboardingImportRowView, error)
	CommitOnboardingImport(context.Context, application.Invocation, application.CommitOnboardingImportCommand) (application.OnboardingImportView, error)
	CancelOnboardingImport(context.Context, application.Invocation, string) (application.OnboardingImportView, error)
	OnboardingImportReport(context.Context, application.Invocation, string, io.Writer) error
}

type onboardingImportModule struct{ imports OnboardingImportApplication }

type onboardingImportCommitRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	PreviewDigest    string `json:"preview_digest"`
	Policy           string `json:"policy"`
}

type onboardingImportResponse struct {
	ID                  string                        `json:"id"`
	Mode                string                        `json:"mode"`
	State               string                        `json:"state"`
	ScopeType           string                        `json:"scope_type"`
	ScopeID             string                        `json:"scope_id"`
	RoleID              string                        `json:"role_id,omitempty"`
	SourcePeriodID      string                        `json:"source_period_id,omitempty"`
	SourceClassID       string                        `json:"source_class_id,omitempty"`
	DestinationPeriodID string                        `json:"destination_period_id,omitempty"`
	DestinationClassID  string                        `json:"destination_class_id,omitempty"`
	EffectiveAt         string                        `json:"effective_at,omitempty"`
	PreviewDigest       string                        `json:"preview_digest,omitempty"`
	IgnoredHeaders      []string                      `json:"ignored_headers,omitempty"`
	Rows                []onboardingImportRowResponse `json:"rows,omitempty"`
	TotalRows           int                           `json:"total_rows"`
	ValidRows           int                           `json:"valid_rows"`
	InvalidRows         int                           `json:"invalid_rows"`
	SucceededRows       int                           `json:"succeeded_rows"`
	NoOpRows            int                           `json:"no_op_rows"`
	FailedRows          int                           `json:"failed_rows"`
	SkippedRows         int                           `json:"skipped_rows"`
	CommitPolicy        string                        `json:"commit_policy,omitempty"`
	ParseJobID          string                        `json:"parse_job_id"`
	ExecutionJobID      string                        `json:"execution_job_id,omitempty"`
	CreatedAt           string                        `json:"created_at"`
	UpdatedAt           string                        `json:"updated_at"`
	ExpiresAt           string                        `json:"expires_at"`
	Revision            int64                         `json:"revision"`
	FailureCode         string                        `json:"failure_code,omitempty"`
}

type onboardingImportRowResponse struct {
	RowNumber    int    `json:"row"`
	Reference    string `json:"reference"`
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	PreviewCode  string `json:"preview_code,omitempty"`
	PublicCode   string `json:"public_code,omitempty"`
	InvitationID string `json:"invitation_id,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

func onboardingImportResource(imports OnboardingImportApplication) resource {
	module := onboardingImportModule{imports: imports}
	codes := academicRelationshipMutationErrorCodes("onboarding_import.conflict", "onboarding_import.invalid_file", "onboarding_import.unavailable")
	return newResource("onboarding-imports",
		idempotentProtocolRoute(IdempotencyNone, application.MaximumOnboardingImportBytes, "onboarding-import-csv", RouteProtocolStreamingUpload, AuthPrincipalRequired,
			http.MethodPost, apiPath(literal("onboarding-imports")), codes, module.upload),
		principalRoute(http.MethodGet, apiPath(literal("onboarding-imports"), canonicalID("onboarding_import_id")),
			append(academicRelationshipReadErrorCodes(), "onboarding_import.unavailable"), module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("onboarding-imports"), canonicalID("onboarding_import_id"), literal("commit")),
			append(codes, "idempotency.key_required", "idempotency.invalid_key"), module.commit),
		principalRoute(http.MethodPost, apiPath(literal("onboarding-imports"), canonicalID("onboarding_import_id"), literal("cancel")), codes, module.cancel),
		protocolRoute("onboarding-import-report", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet,
			apiPath(literal("onboarding-imports"), canonicalID("onboarding_import_id"), literal("report")), append(academicRelationshipReadErrorCodes(), "onboarding_import.conflict", "onboarding_import.unavailable"), module.report),
	)
}

func (m onboardingImportModule) upload(request operationRequest) (protocolResult, error) {
	query := request.request.URL.Query()
	view, err := m.imports.UploadOnboardingImport(request.context, request.invocation(), application.UploadOnboardingImportCommand{
		Mode: model.OnboardingImportMode(query.Get("mode")), ScopeType: model.RoleScopeType(query.Get("scope_type")), ScopeID: query.Get("scope_id"), RoleID: query.Get("role_id"), Body: request.request.Body,
	})
	if err != nil {
		return protocolResult{}, err
	}
	return streamingUploadProtocolResult(http.StatusAccepted, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m onboardingImportModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireOnboardingImportID()
	if err != nil {
		return operationResult{}, err
	}
	view, rows, err := m.imports.GetOnboardingImport(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, onboardingImportResponseFromView(view, rows)).withHeaders(noStoreHeaders()), nil
}

func (m onboardingImportModule) commit(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireOnboardingImportID()
	if err != nil {
		return operationResult{}, err
	}
	var body onboardingImportCommitRequest
	if err = request.decodeJSON(&body, "commitOnboardingImport"); err != nil {
		return operationResult{}, invalidRequestError("onboardingImportModule.commit", err)
	}
	view, err := m.imports.CommitOnboardingImport(request.context, request.invocation(), application.CommitOnboardingImportCommand{ID: id,
		ExpectedRevision: body.ExpectedRevision, PreviewDigest: body.PreviewDigest, Policy: model.OnboardingImportCommitPolicy(body.Policy), IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusAccepted, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m onboardingImportModule) cancel(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireOnboardingImportID()
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.imports.CancelOnboardingImport(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m onboardingImportModule) report(request operationRequest) (protocolResult, error) {
	id, err := request.params.RequireOnboardingImportID()
	if err != nil {
		return protocolResult{}, err
	}
	var body bytes.Buffer
	if err = m.imports.OnboardingImportReport(request.context, request.invocation(), id, &body); err != nil {
		return protocolResult{}, err
	}
	headers := noStoreHeaders()
	headers.Set("Content-Type", "text/csv; charset=utf-8")
	headers.Set("X-Content-Type-Options", "nosniff")
	return binaryDownloadProtocolResult(io.NopCloser(bytes.NewReader(body.Bytes())), int64(body.Len())).withHeaders(headers), nil
}

func onboardingImportResponseFromView(view application.OnboardingImportView, rows []application.OnboardingImportRowView) onboardingImportResponse {
	response := onboardingImportResponse{ID: view.ID.String(), Mode: string(view.Mode), State: string(view.State), ScopeType: string(view.ScopeType), ScopeID: view.ScopeID,
		RoleID: view.RoleID.String(), SourcePeriodID: view.SourcePeriodID.String(), SourceClassID: view.SourceClassID.String(), DestinationPeriodID: view.DestinationPeriodID.String(), DestinationClassID: view.DestinationClassID.String(),
		PreviewDigest: view.PreviewDigest, IgnoredHeaders: append([]string(nil), view.IgnoredHeaders...), TotalRows: view.TotalRows, ValidRows: view.ValidRows,
		InvalidRows: view.InvalidRows, SucceededRows: view.SucceededRows, NoOpRows: view.NoOpRows, FailedRows: view.FailedRows, SkippedRows: view.SkippedRows,
		CommitPolicy: string(view.CommitPolicy), ParseJobID: view.ParseJobID.String(), ExecutionJobID: view.ExecutionJobID.String(), CreatedAt: view.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: view.UpdatedAt.Format(time.RFC3339Nano), ExpiresAt: view.ExpiresAt.Format(time.RFC3339Nano), Revision: view.Revision, FailureCode: view.FailureCode}
	if !view.EffectiveAt.IsZero() {
		response.EffectiveAt = model.TimeUTC(view.EffectiveAt).Format(time.RFC3339Nano)
	}
	if rows != nil {
		response.Rows = make([]onboardingImportRowResponse, 0, len(rows))
		for _, row := range rows {
			response.Rows = append(response.Rows, onboardingImportRowResponse{RowNumber: row.RowNumber, Reference: row.Reference, Operation: row.Operation, Status: string(row.Status), PreviewCode: row.PreviewCode, PublicCode: row.PublicCode, InvitationID: row.InvitationID.String(), ResourceID: row.ResourceID})
		}
	}
	return response
}

type unavailableOnboardingImportApplication struct{}

func (unavailableOnboardingImportApplication) UploadOnboardingImport(context.Context, application.Invocation, application.UploadOnboardingImportCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("onboarding_import.unavailable")
}
func (unavailableOnboardingImportApplication) GetOnboardingImport(context.Context, application.Invocation, string) (application.OnboardingImportView, []application.OnboardingImportRowView, error) {
	return application.OnboardingImportView{}, nil, application.NewError("onboarding_import.unavailable")
}
func (unavailableOnboardingImportApplication) CommitOnboardingImport(context.Context, application.Invocation, application.CommitOnboardingImportCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("onboarding_import.unavailable")
}
func (unavailableOnboardingImportApplication) CancelOnboardingImport(context.Context, application.Invocation, string) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("onboarding_import.unavailable")
}
func (unavailableOnboardingImportApplication) OnboardingImportReport(context.Context, application.Invocation, string, io.Writer) error {
	return application.NewError("onboarding_import.unavailable")
}
