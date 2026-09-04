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
	"io"
	"net/http"
	"time"

	application "github.com/sudosylabs/proctor/server/app"
)

type StudentProgressionApplication interface {
	DryRunStudentProgression(context.Context, application.Invocation, application.DryRunStudentProgressionCommand) (application.OnboardingImportView, error)
	GetStudentProgression(context.Context, application.Invocation, string) (application.OnboardingImportView, []application.OnboardingImportRowView, error)
	CommitStudentProgression(context.Context, application.Invocation, application.CommitStudentProgressionCommand) (application.OnboardingImportView, error)
	CancelStudentProgression(context.Context, application.Invocation, string) (application.OnboardingImportView, error)
	StudentProgressionReport(context.Context, application.Invocation, string, io.Writer) error
}

type studentProgressionModule struct{ progressions StudentProgressionApplication }

type studentProgressionDryRunRequest struct {
	SourcePeriodID      string `json:"source_period_id"`
	SourceClassID       string `json:"source_class_id"`
	DestinationPeriodID string `json:"destination_period_id"`
	DestinationClassID  string `json:"destination_class_id"`
	EffectiveAt         string `json:"effective_at"`
}

type studentProgressionCommitRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	PreviewDigest    string `json:"preview_digest"`
}

func studentProgressionResource(progressions StudentProgressionApplication) resource {
	module := studentProgressionModule{progressions: progressions}
	mutationCodes := academicRelationshipMutationErrorCodes("student_progression.target_conflict", "student_progression.lineage_conflict",
		"student_progression.effective_date_conflict", "student_progression.roster_too_large", "student_progression.conflict", "student_progression.unavailable")
	readCodes := append(academicRelationshipReadErrorCodes(), "student_progression.unavailable")
	return newResource("student-progressions",
		principalRoute(http.MethodPost, apiPath(literal("student-progressions")), mutationCodes, module.dryRun),
		principalRoute(http.MethodGet, apiPath(literal("student-progressions"), canonicalID("student_progression_id")), readCodes, module.get),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, apiPath(literal("student-progressions"), canonicalID("student_progression_id"), literal("commit")),
			append(mutationCodes, "idempotency.key_required", "idempotency.invalid_key"), module.commit),
		principalRoute(http.MethodPost, apiPath(literal("student-progressions"), canonicalID("student_progression_id"), literal("cancel")), mutationCodes, module.cancel),
		protocolRoute("student-progression-report", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet,
			apiPath(literal("student-progressions"), canonicalID("student_progression_id"), literal("report")),
			append(readCodes, "student_progression.conflict"), module.report),
	)
}

func (m studentProgressionModule) dryRun(request operationRequest) (operationResult, error) {
	var body studentProgressionDryRunRequest
	if err := request.decodeJSON(&body, "dryRunStudentProgression"); err != nil {
		return operationResult{}, invalidRequestError("studentProgressionModule.dryRun", err)
	}
	effectiveAt, err := time.Parse(time.RFC3339Nano, body.EffectiveAt)
	if err != nil || effectiveAt.IsZero() {
		return operationResult{}, invalidRequestError("studentProgressionModule.dryRun.effective_at", err)
	}
	view, err := m.progressions.DryRunStudentProgression(request.context, request.invocation(), application.DryRunStudentProgressionCommand{
		SourcePeriodID: body.SourcePeriodID, SourceClassID: body.SourceClassID, DestinationPeriodID: body.DestinationPeriodID,
		DestinationClassID: body.DestinationClassID, EffectiveAt: effectiveAt.UnixMilli(),
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusAccepted, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m studentProgressionModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireStudentProgressionID()
	if err != nil {
		return operationResult{}, err
	}
	view, rows, err := m.progressions.GetStudentProgression(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, onboardingImportResponseFromView(view, rows)).withHeaders(noStoreHeaders()), nil
}

func (m studentProgressionModule) commit(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireStudentProgressionID()
	if err != nil {
		return operationResult{}, err
	}
	var body studentProgressionCommitRequest
	if err = request.decodeJSON(&body, "commitStudentProgression"); err != nil {
		return operationResult{}, invalidRequestError("studentProgressionModule.commit", err)
	}
	view, err := m.progressions.CommitStudentProgression(request.context, request.invocation(), application.CommitStudentProgressionCommand{
		ID: id, ExpectedRevision: body.ExpectedRevision, PreviewDigest: body.PreviewDigest, IdempotencyKey: request.idempotencyKey,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusAccepted, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m studentProgressionModule) cancel(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireStudentProgressionID()
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.progressions.CancelStudentProgression(request.context, request.invocation(), id)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, onboardingImportResponseFromView(view, nil)).withHeaders(noStoreHeaders()), nil
}

func (m studentProgressionModule) report(request operationRequest) (protocolResult, error) {
	id, err := request.params.RequireStudentProgressionID()
	if err != nil {
		return protocolResult{}, err
	}
	var body bytes.Buffer
	if err = m.progressions.StudentProgressionReport(request.context, request.invocation(), id, &body); err != nil {
		return protocolResult{}, err
	}
	headers := noStoreHeaders()
	headers.Set("Content-Type", "text/csv; charset=utf-8")
	headers.Set("X-Content-Type-Options", "nosniff")
	return binaryDownloadProtocolResult(io.NopCloser(bytes.NewReader(body.Bytes())), int64(body.Len())).withHeaders(headers), nil
}

type unavailableStudentProgressionApplication struct{}

func (unavailableStudentProgressionApplication) DryRunStudentProgression(context.Context, application.Invocation, application.DryRunStudentProgressionCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("student_progression.unavailable")
}
func (unavailableStudentProgressionApplication) GetStudentProgression(context.Context, application.Invocation, string) (application.OnboardingImportView, []application.OnboardingImportRowView, error) {
	return application.OnboardingImportView{}, nil, application.NewError("student_progression.unavailable")
}
func (unavailableStudentProgressionApplication) CommitStudentProgression(context.Context, application.Invocation, application.CommitStudentProgressionCommand) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("student_progression.unavailable")
}
func (unavailableStudentProgressionApplication) CancelStudentProgression(context.Context, application.Invocation, string) (application.OnboardingImportView, error) {
	return application.OnboardingImportView{}, application.NewError("student_progression.unavailable")
}
func (unavailableStudentProgressionApplication) StudentProgressionReport(context.Context, application.Invocation, string, io.Writer) error {
	return application.NewError("student_progression.unavailable")
}
