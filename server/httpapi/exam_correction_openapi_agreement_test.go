// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamSittingCorrectionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	base := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}"
	stages := base + "/correction-resource-stages"
	corrections := base + "/corrections"
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "POST " + stages, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamSittingCorrectionResourceStageCreated", SuccessSchema: "ExamSittingCorrectionResourceStageResponse",
				PublicErrorCodes: examSittingCorrectionContractCodes("exam.sitting.correction.invalid_content", "exam.sitting.correction.stage_invalid"),
			},
			{
				Key: "POST " + corrections, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ApplyExamSittingCorrection", RequestSchema: "ApplyExamSittingCorrectionRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamSittingCorrectionCreated", SuccessSchema: "ExamSittingCorrectionResponse",
				PublicErrorCodes: examSittingCorrectionContractCodes("exam.sitting.correction.no_changes", "exam.sitting.correction.manifest_invalid", "exam.sitting.correction.stage_invalid", "exam.resource.limit"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ExamSittingCorrectionResourceStageMetadata", DTO: reflect.TypeOf(examSittingCorrectionStageMetadata{}), Required: []string{"base_revision_id", "target_kind", "media_type", "size", "sha256"}},
			{Name: "ExamSittingCorrectionResourceStageResponse", DTO: reflect.TypeOf(examSittingCorrectionStageResponse{}), Required: []string{"stage_id", "resource_id", "media_type", "size", "sha256", "expires_at"}},
			{Name: "ApplyExamSittingCorrectionRequest", DTO: reflect.TypeOf(applyExamSittingCorrectionRequest{}), Required: []string{"expected_sitting_revision", "expected_current_revision_id", "candidate_summary", "acknowledgement_required", "reason", "resources"}, NonNullable: []string{"instructions_markdown", "browser_policy"}},
			{Name: "ExamSittingCorrectionResourceRequest", DTO: reflect.TypeOf(examSittingCorrectionResourceRequest{}), Required: []string{"resource_id", "display_name", "description_markdown"}},
			{Name: "ExamSittingCorrectionResponse", DTO: reflect.TypeOf(examSittingCorrectionResponse{}), Required: []string{"exam_id", "exam_sitting_id", "previous_revision_id", "revision_id", "revision_number", "sitting_revision", "sitting_state", "effective_at"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examSittingCorrectionResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
	document := readOpenAPIDocument(t)
	assertExamMultipartOperation(t, document, stages, "post", "#/components/schemas/ExamSittingCorrectionResourceStageMultipart")
}

func examSittingCorrectionContractCodes(specific ...string) []string {
	common := []string{
		"request.invalid", "resource.not_found", "exam.sitting.correction.invalid",
		"exam.sitting.correction.conflict", "exam.sitting.correction.unavailable",
		"exam.archived", "exam.sitting.revision_conflict", "exam.sitting.state_conflict", "exam.sitting.deadline_reached",
	}
	common = append(common, specific...)
	return principalMutationContractCodes(append(common,
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")...)
}
