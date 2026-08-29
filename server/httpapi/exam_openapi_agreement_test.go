// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
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
				Key: "GET /api/v1/exams", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamListOK", SuccessSchema: "ExamListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "exam.invalid", "exam.unavailable", "administration.unavailable"),
			},
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
			{
				Key: "PUT /api/v1/exams/{exam_id}/draft/browser-policy", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ConfigureExamDraftBrowserPolicy", RequestSchema: "ConfigureExamDraftBrowserPolicyRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "PUT /api/v1/exams/{exam_id}/draft/execution-profile", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ConfigureExamDraftExecutionProfile", RequestSchema: "ConfigureExamDraftExecutionProfileRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamOK", SuccessSchema: "ExamResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.draft.revision_conflict", "exam.draft.no_changes", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/exams/{exam_id}/draft/execution-images", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExecutionImageListOK", SuccessSchema: "ExecutionImageListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "exam.unavailable", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/exams/{exam_id}/archive", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ArchiveExam", RequestSchema: "ArchiveExamRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIdentityOK", SuccessSchema: "ExamIdentityResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict", "exam.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/exams/{exam_id}/managers", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamManagerListOK", SuccessSchema: "ExamManagerListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/exams/{exam_id}/managers", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/AddExamManager", RequestSchema: "AddExamManagerRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamManagerChanged", SuccessSchema: "ExamManagerChangeResponse",
				PublicErrorCodes: examManagerAdditionContractCodes(),
			},
			{
				Key: "DELETE /api/v1/exams/{exam_id}/managers/{user_id}", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/RemoveExamManager", RequestSchema: "RemoveExamManagerRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamManagerChanged", SuccessSchema: "ExamManagerChangeResponse",
				PublicErrorCodes: examManagerRemovalContractCodes(),
			},
			{
				Key: "PUT /api/v1/exams/{exam_id}/owner", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/TransferExamOwnership", RequestSchema: "TransferExamOwnershipRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamManagerChanged", SuccessSchema: "ExamManagerChangeResponse",
				PublicErrorCodes: examOwnerTransferContractCodes(),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "CreateExamRequest", DTO: reflect.TypeOf(createExamRequest{}), Required: []string{"academic_unit_id", "title"}},
			{Name: "EditExamDraftTextRequest", DTO: reflect.TypeOf(editExamDraftTextRequest{}), Required: []string{"expected_draft_revision"}},
			{Name: "ConfigureExamDraftFocusLossRequest", DTO: reflect.TypeOf(configureExamDraftFocusLossRequest{}), Required: []string{"expected_draft_revision", "enabled", "minimum_duration_milliseconds", "incident_count", "window_milliseconds", "outcome"}},
			{Name: "ConfigureExamDraftBrowserPolicyRequest", DTO: reflect.TypeOf(configureExamDraftBrowserPolicyRequest{}), Required: []string{"expected_draft_revision", "browser_policy"}},
			{Name: "ConfigureExamDraftExecutionProfileRequest", DTO: reflect.TypeOf(configureExamDraftExecutionProfileRequest{}), Required: []string{"expected_draft_revision", "enabled", "image", "network"}},
			{Name: "ArchiveExamRequest", DTO: reflect.TypeOf(archiveExamRequest{}), Required: []string{"expected_exam_revision"}},
			{Name: "AddExamManagerRequest", DTO: reflect.TypeOf(addExamManagerRequest{}), Required: []string{"user_id", "expected_exam_revision"}},
			{Name: "RemoveExamManagerRequest", DTO: reflect.TypeOf(removeExamManagerRequest{}), Required: []string{"expected_exam_revision"}},
			{Name: "TransferExamOwnershipRequest", DTO: reflect.TypeOf(transferExamOwnershipRequest{}), Required: []string{"user_id", "expected_exam_revision"}},
			{Name: "ExamManagerListResponse", DTO: reflect.TypeOf(examManagerListResponse{}), Required: []string{"items"}},
			{Name: "ExamManagerResponse", DTO: reflect.TypeOf(examManagerResponse{}), Required: []string{"user_id", "granted_by_user_id", "granted_at", "is_creator", "is_owner"}},
			{Name: "ExamManagerChangeResponse", DTO: reflect.TypeOf(examManagerChangeResponse{}), Required: []string{"exam", "manager"}, Nullable: []string{"exam.archived_at"}},
			{Name: "ExamListResponse", DTO: reflect.TypeOf(examListResponse{}), Required: []string{"items"}, Nullable: []string{"items.archived_at", "items.sitting_summary"}},
			{Name: "ExamSummaryResponse", DTO: reflect.TypeOf(examSummaryResponse{}), Required: []string{"id", "academic_unit_id", "academic_unit_display_name", "creator_user_id", "owner_user_id", "title", "updated_at", "archived_at", "revision", "manager_count", "sitting_count", "matching_sitting_count", "sitting_summary"}, Nullable: []string{"archived_at", "sitting_summary"}},
			{Name: "ExamResponse", DTO: reflect.TypeOf(examResponse{}), Required: []string{"exam", "draft", "owner_user_id", "manager_count"}, Nullable: []string{"exam.archived_at"}},
			{Name: "ExamIdentityResponse", DTO: reflect.TypeOf(examIdentityResponse{}), Required: []string{"id", "academic_unit_id", "creator_user_id", "owner_user_id", "created_at", "updated_at", "archived_at", "revision"}, Nullable: []string{"archived_at"}},
			{Name: "ExamDraftResponse", DTO: reflect.TypeOf(examDraftResponse{}), Required: []string{"exam_id", "title", "instructions_markdown", "policy", "execution_profile", "browser_policy", "capacity", "updated_at", "revision", "resource_count", "has_starter_workspace"}},
			{Name: "ExecutionProfile", DTO: reflect.TypeOf(executionProfileResponse{}), Required: []string{"enabled", "image", "network"}},
			{Name: "BrowserPolicy", DTO: reflect.TypeOf(browserPolicyDocument{}), Required: []string{"schema_version", "enabled"}},
			{Name: "BrowserPolicyRule", DTO: reflect.TypeOf(browserPolicyRuleDocument{}), Required: []string{"rule_id", "origin", "path_prefix", "host_match", "allow_redirects", "blocked_navigation_outcome"}},
			{Name: "ExecutionImageListResponse", DTO: reflect.TypeOf(executionImageListResponse{}), Required: []string{"items"}},
			{Name: "ExecutionImage", DTO: reflect.TypeOf(executionImageResponse{}), Required: []string{"id", "networks"}},
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

	document := readOpenAPIDocument(t)
	var listOperation openAPIOperation
	if err := json.Unmarshal(document.Paths[model.APIURLSuffix+"/exams"]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	if got := academicStructureQueryParameterNames(listOperation.Parameters); !reflect.DeepEqual(got, []string{"academic_unit_id", "archive_state", "cursor", "ends_after", "limit", "q", "sitting_state", "starts_before"}) {
		t.Fatalf("Exam list query parameters = %v", got)
	}
}

func examManagerAdditionContractCodes() []string {
	return principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
		"exam.manager.exists", "exam.manager.ineligible", "exam.manager.candidate_conflict", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
}

func examManagerRemovalContractCodes() []string {
	return principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
		"exam.manager.not_found", "exam.manager.owner_protected", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
}

func examOwnerTransferContractCodes() []string {
	return principalMutationContractCodes("request.invalid", "resource.not_found", "exam.invalid", "exam.archived", "exam.revision_conflict",
		"exam.manager.not_found", "exam.manager.ineligible", "exam.manager.candidate_conflict", "exam.owner.no_changes", "exam.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
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
