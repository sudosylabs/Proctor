// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamAttemptOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	managerBase := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts"
	managerMember := managerBase + "/{exam_attempt_id}"
	candidateBase := "/api/v1/exam-attempts/{exam_attempt_id}"
	presentation := candidateBase + "/presentation"
	workspace := candidateBase + "/workspace"
	resourceContent := candidateBase + "/resources/{exam_resource_id}/content"
	workspaceContent := candidateBase + "/workspace/files/{attempt_workspace_entry_id}/content"
	managerCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "administration.unavailable")
	candidateCodes := personalAccessTokenSessionCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + managerBase, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerListOK", SuccessSchema: "ExamAttemptManagerListResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerMember, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerOK", SuccessSchema: "ExamAttemptManagerResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + presentation, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamPresentationOK", SuccessSchema: "CandidateExamPresentationResponse", PublicErrorCodes: candidateCodes},
			{Key: "GET " + workspace, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamWorkspaceListOK", SuccessSchema: "CandidateExamWorkspaceListResponse", PublicErrorCodes: candidateCodes},
			{Key: "GET " + resourceContent, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: candidateCodes},
			{Key: "GET " + workspaceContent, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: candidateCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ExamAttemptManagerResponse", DTO: reflect.TypeOf(examAttemptManagerResponse{}), Required: []string{"id", "exam_id", "exam_sitting_id", "candidate_user_id", "admission_revision_id", "state", "created_at", "updated_at", "revision", "workspace"}},
			{Name: "ExamAttemptWorkspaceResponse", DTO: reflect.TypeOf(examAttemptWorkspaceResponse{}), Required: []string{"id", "cursor", "created_at", "updated_at"}},
			{Name: "ExamAttemptParticipationResponse", DTO: reflect.TypeOf(examAttemptParticipationResponse{}), Required: []string{"id", "state", "generation", "renewal_sequence", "started_at", "updated_at", "lease_expires_at"}},
			{Name: "ExamAttemptConnectionResponse", DTO: reflect.TypeOf(examAttemptConnectionResponse{}), Required: []string{"id", "state", "opened_at"}},
			{Name: "ExamAttemptManagerListResponse", DTO: reflect.TypeOf(examAttemptManagerListResponse{}), Required: []string{"items"}},
			{Name: "CandidateExamPresentationResponse", DTO: reflect.TypeOf(candidateExamPresentationResponse{}), Required: []string{"attempt_id", "exam_sitting_id", "admission_revision_id", "current_revision_id", "title", "instructions_markdown", "resources"}},
			{Name: "CandidateExamResourceResponse", DTO: reflect.TypeOf(candidateExamResourceResponse{}), Required: []string{"id", "display_name", "description_markdown", "position", "media_type", "size", "sha256"}},
			{Name: "CandidateExamWorkspaceItemResponse", DTO: reflect.TypeOf(candidateExamWorkspaceItemResponse{}), Required: []string{"id", "kind", "path"}},
			{Name: "CandidateExamWorkspaceListResponse", DTO: reflect.TypeOf(candidateExamWorkspaceListResponse{}), Required: []string{"items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examAttemptResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	for _, path := range []string{presentation, workspace, resourceContent, workspaceContent} {
		operation := decodeExamContentOpenAPIOperation(t, document, path, "get")
		for _, expected := range []struct{ name, ref string }{
			{name: candidateAttemptCredentialHeader, ref: "#/components/parameters/CandidateAttemptCredential"},
			{name: candidateAttemptConnectionHeader, ref: "#/components/parameters/CandidateAttemptConnectionID"},
		} {
			if !hasOpenAPIHeaderParameter(operation, expected.name) && !hasOpenAPIParameterRef(operation, expected.ref) {
				t.Errorf("GET %s does not document %s", path, expected.name)
			}
		}
	}
	for _, path := range []string{resourceContent, workspaceContent} {
		assertProtectedExamContentResponse(t, document, path, "CandidateExamProtectedContent")
		operation := decodeExamContentOpenAPIOperation(t, document, path, "get")
		if operation.Responses["304"].Ref != "#/components/responses/CandidateExamContentNotModified" {
			t.Errorf("GET %s 304 response = %#v", path, operation.Responses["304"])
		}
	}
	assertProtectedExamCacheControl(t, "CandidateExamProtectedContent", "private, no-store")
	if _, ok := document.Components.Responses["CandidateExamProtectedContent"].Headers["Content-Disposition"]; ok {
		t.Error("candidate protected response documents forbidden Content-Disposition")
	}
	assertExamAttemptQueryParameters(t, document, managerBase, []string{"state", "limit", "cursor"})
	assertExamAttemptQueryParameters(t, document, workspace, []string{"limit", "cursor"})
}

func hasOpenAPIParameterRef(operation openAPIOperation, ref string) bool {
	for _, parameter := range operation.Parameters {
		if parameter.Ref == ref {
			return true
		}
	}
	return false
}

func assertExamAttemptQueryParameters(t *testing.T, document openAPIDocument, path string, want []string) {
	t.Helper()
	operation := decodeExamContentOpenAPIOperation(t, document, path, "get")
	got := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		if parameter.In == "query" {
			got = append(got, parameter.Name)
		}
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("GET %s omits query parameter %q: %v", path, name, got)
		}
	}
	if len(got) != len(want) {
		encoded, _ := json.Marshal(got)
		t.Errorf("GET %s query parameters = %s, want exactly %v", path, encoded, want)
	}
}
