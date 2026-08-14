// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/sudosylabs/proctor/server/model"
)

func TestExamOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "POST /api/v1/exams", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/CreateExam", RequestSchema: "CreateExamRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamCreated", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/exams/{exam_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "exam.unavailable", "administration.unavailable"),
			},
			{
				Key: "PATCH /api/v1/exams/{exam_id}/draft", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/EditExamDraftText", RequestSchema: "EditExamDraftTextRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "PUT /api/v1/exams/{exam_id}/draft/policies/focus-loss", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ConfigureExamDraftFocusLoss", RequestSchema: "ConfigureExamDraftFocusLossRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "CreateExamRequest", DTO: reflect.TypeOf(createExamRequest{}), Required: []string{"academic_unit_id", "title"}},
			{Name: "EditExamDraftTextRequest", DTO: reflect.TypeOf(editExamDraftTextRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ConfigureExamDraftFocusLossRequest", DTO: reflect.TypeOf(configureExamDraftFocusLossRequest{}), Required: []string{"expected_draft_revision", "enabled", "minimum_duration_milliseconds", "incident_count", "window_milliseconds", "outcome"}},
			{Name: "ExamResponse", DTO: reflect.TypeOf(examResponse{}), Required: []string{"exam", "draft", "owner_user_id", "manager_count"}, Nullable: []string{"exam.archived_at"}},
			{Name: "ExamIdentityResponse", DTO: reflect.TypeOf(examIdentityResponse{}), Required: []string{"id", "academic_unit_id", "creator_user_id", "owner_user_id", "created_at", "updated_at", "archived_at", "revision"}, Nullable: []string{"archived_at"}},
			{Name: "ExamDraftResponse", DTO: reflect.TypeOf(examDraftResponse{}), Required: []string{"exam_id", "title", "instructions_markdown", "policy", "updated_at", "revision", "resource_count", "has_starter_workspace"}},
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

func TestEditExamDraftTextOpenAPISchemaRequiresANonNullAuthoredValue(t *testing.T) {
	t.Parallel()
	document, err := openapi3.NewLoader().LoadFromFile(openAPIDocumentPath(t))
	if err != nil {
		t.Fatal(err)
	}
	schema := document.Components.Schemas["EditExamDraftTextRequest"]
	if schema == nil || schema.Value == nil {
		t.Fatal("EditExamDraftTextRequest schema is missing")
	}
	for _, invalid := range []map[string]any{
		{"expected_draft_revision": float64(1)},
		{"expected_draft_revision": float64(1), "title": nil, "instructions_markdown": nil},
	} {
		if err := schema.Value.VisitJSON(invalid); err == nil {
			t.Fatalf("schema accepted %#v", invalid)
		}
	}
	for _, valid := range []map[string]any{
		{"expected_draft_revision": float64(1), "title": "Algorithms"},
		{"expected_draft_revision": float64(1), "instructions_markdown": ""},
	} {
		if err := schema.Value.VisitJSON(valid); err != nil {
			t.Fatalf("schema rejected %#v: %v", valid, err)
		}
	}
}
