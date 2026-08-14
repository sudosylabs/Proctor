// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "POST /api/v1/exams", Auth: AuthPrincipalRequired, Idempotency: IdempotencyOptional,
				RequestBodyRef: "#/components/requestBodies/CreateExam", RequestSchema: "CreateExamRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamCreated", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/exams/{exam_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "exam.unavailable", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "CreateExamRequest", DTO: reflect.TypeOf(createExamRequest{}), Required: []string{"academic_unit_id", "title"}},
			{Name: "ExamResponse", DTO: reflect.TypeOf(examResponse{}), Required: []string{"exam", "draft", "owner_user_id", "manager_count"}},
			{Name: "ExamIdentityResponse", DTO: reflect.TypeOf(examIdentityResponse{}), Required: []string{"id", "academic_unit_id", "creator_user_id", "owner_user_id", "create_at", "update_at", "delete_at", "revision"}},
			{Name: "ExamDraftResponse", DTO: reflect.TypeOf(examDraftResponse{}), Required: []string{"exam_id", "title", "instructions_markdown", "policy", "update_at", "revision", "resource_count", "has_starter_workspace"}},
			{Name: "ExamPolicySet", DTO: reflect.TypeOf(examPolicyResponse{}), Required: []string{"schema_version", "connection_loss", "focus_loss"}},
			{Name: "ConnectionLossPolicy", DTO: reflect.TypeOf(examConnectionPolicyResponse{}), Required: []string{"outcome"}},
			{Name: "FocusLossPolicy", DTO: reflect.TypeOf(examFocusPolicyResponse{}), Required: []string{"enabled", "minimum_duration_milliseconds", "incident_count", "window_milliseconds", "outcome"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examResource(&examHTTPApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
