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
	reallow := managerMember + "/reallow"
	candidateBase := "/api/v1/exam-attempts/{exam_attempt_id}"
	presentation := candidateBase + "/presentation"
	workspace := candidateBase + "/workspace"
	workspaceChanges := workspace + "/changes"
	workspaceDirectories := workspace + "/directories"
	workspaceFiles := workspace + "/files"
	workspaceEntry := workspace + "/entries/{attempt_workspace_entry_id}"
	resourceContent := candidateBase + "/resources/{exam_resource_id}/content"
	workspaceContent := candidateBase + "/workspace/files/{attempt_workspace_entry_id}/content"
	candidateSubmissions := candidateBase + "/submissions"
	managerSubmission := managerMember + "/submissions/{submission_id}"
	managerSubmissionManifest := managerSubmission + "/manifest"
	managerSubmissionContent := managerSubmission + "/files/{attempt_workspace_entry_id}/content"
	managerCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "administration.unavailable")
	reallowCodes := principalMutationContractCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.suspension_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.conflict", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
	candidateCodes := personalAccessTokenSessionCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.unavailable")
	workspaceMutationCodes := func(specific ...string) []string {
		common := []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid",
			"exam.attempt.sitting_unavailable", "exam.attempt.state_conflict", "exam.attempt.connection_closed",
			"exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.invalid_key",
			"idempotency.conflict", "idempotency.in_progress"}
		return personalAccessTokenSessionMutationCodes(append(common, specific...)...)
	}
	directoryMutationCodes := workspaceMutationCodes("exam.attempt.workspace.path_conflict",
		"exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit")
	createFileMutationCodes := workspaceMutationCodes("exam.attempt.workspace.path_conflict",
		"exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit", "exam.attempt.workspace.size_limit",
		"exam.attempt.workspace.object_conflict")
	moveMutationCodes := workspaceMutationCodes("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict")
	replaceMutationCodes := workspaceMutationCodes("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict",
		"exam.attempt.workspace.content_conflict", "exam.attempt.workspace.size_limit", "exam.attempt.workspace.object_conflict")
	deleteMutationCodes := workspaceMutationCodes("exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict",
		"exam.attempt.workspace.content_conflict", "exam.attempt.workspace.directory_not_empty")
	submissionMutationCodes := workspaceMutationCodes("exam.attempt.workspace.cursor_conflict", "exam.attempt.focus_loss_conflict",
		"exam.attempt.connection_lost")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + managerBase, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerListOK", SuccessSchema: "ExamAttemptManagerListResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerMember, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerOK", SuccessSchema: "ExamAttemptManagerResponse", PublicErrorCodes: managerCodes},
			{Key: "POST " + reallow, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ReallowExamAttempt", RequestSchema: "ReallowExamAttemptRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptReallowed", SuccessSchema: "ExamAttemptReallowResponse", PublicErrorCodes: reallowCodes},
			{Key: "GET " + presentation, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamPresentationOK", SuccessSchema: "CandidateExamPresentationResponse", PublicErrorCodes: candidateCodes},
			{Key: "GET " + workspace, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamWorkspaceListOK", SuccessSchema: "CandidateExamWorkspaceListResponse", PublicErrorCodes: candidateCodes},
			{Key: "GET " + workspaceChanges, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateWorkspaceJournalOK", SuccessSchema: "CandidateWorkspaceJournalResponse", PublicErrorCodes: candidateCodes},
			{Key: "POST " + workspaceDirectories, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateCandidateWorkspaceDirectory", RequestSchema: "CreateCandidateWorkspaceDirectoryRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/CandidateWorkspaceMutationCreated", SuccessSchema: "CandidateWorkspaceMutationResponse", PublicErrorCodes: directoryMutationCodes},
			{Key: "POST " + workspaceFiles, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, SuccessStatus: "201", SuccessRef: "#/components/responses/CandidateWorkspaceMutationCreated", SuccessSchema: "CandidateWorkspaceMutationResponse", PublicErrorCodes: createFileMutationCodes},
			{Key: "PATCH " + workspaceEntry, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/MoveCandidateWorkspaceEntry", RequestSchema: "MoveCandidateWorkspaceEntryRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateWorkspaceMutationOK", SuccessSchema: "CandidateWorkspaceMutationResponse", PublicErrorCodes: moveMutationCodes},
			{Key: "DELETE " + workspaceEntry, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/DeleteCandidateWorkspaceEntry", RequestSchema: "DeleteCandidateWorkspaceEntryRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateWorkspaceMutationOK", SuccessSchema: "CandidateWorkspaceMutationResponse", PublicErrorCodes: deleteMutationCodes},
			{Key: "PUT " + workspaceContent, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateWorkspaceMutationOK", SuccessSchema: "CandidateWorkspaceMutationResponse", PublicErrorCodes: replaceMutationCodes},
			{Key: "GET " + resourceContent, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: candidateCodes},
			{Key: "GET " + workspaceContent, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/CandidateExamProtectedContent", ExceptionalSuccess: true, PublicErrorCodes: candidateCodes},
			{Key: "POST " + candidateSubmissions, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/SubmitExamAttempt", RequestSchema: "SubmitExamAttemptRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/ExamSubmissionReceiptCreated",
				SuccessSchema: "ExamSubmissionReceiptResponse", PublicErrorCodes: submissionMutationCodes},
			{Key: "GET " + managerSubmission, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionManagerOK", SuccessSchema: "ExamSubmissionManagerResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerSubmissionManifest, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionManifestOK", SuccessSchema: "ExamSubmissionManifestResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerSubmissionContent, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionFileContent", ExceptionalSuccess: true, PublicErrorCodes: managerCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ExamAttemptManagerResponse", DTO: reflect.TypeOf(examAttemptManagerResponse{}), Required: []string{"id", "exam_id", "exam_sitting_id", "candidate_user_id", "admission_revision_id", "state", "created_at", "updated_at", "revision", "workspace"}},
			{Name: "ExamAttemptWorkspaceResponse", DTO: reflect.TypeOf(examAttemptWorkspaceResponse{}), Required: []string{"id", "cursor", "created_at", "updated_at"}},
			{Name: "ExamAttemptParticipationResponse", DTO: reflect.TypeOf(examAttemptParticipationResponse{}), Required: []string{"id", "state", "generation", "renewal_sequence", "started_at", "updated_at", "lease_expires_at"}},
			{Name: "ExamAttemptConnectionResponse", DTO: reflect.TypeOf(examAttemptConnectionResponse{}), Required: []string{"id", "state", "opened_at"}},
			{Name: "ExamAttemptSuspensionResponse", DTO: reflect.TypeOf(examAttemptSuspensionResponse{}), Required: []string{"id", "participation_id", "flag_id", "generation", "state", "source", "candidate_reason", "started_at"}},
			{Name: "ExamAttemptManagerListResponse", DTO: reflect.TypeOf(examAttemptManagerListResponse{}), Required: []string{"items"}},
			{Name: "ReallowExamAttemptRequest", DTO: reflect.TypeOf(reallowExamAttemptRequest{}), Required: []string{"suspension_id", "expected_attempt_revision", "reason"}},
			{Name: "ExamAttemptReallowResponse", DTO: reflect.TypeOf(examAttemptReallowResponse{}), Required: []string{"exam_attempt_id", "exam_sitting_id", "state", "attempt_revision", "suspension_id", "suspension_state", "candidate_reason", "reallowed_by_user_id"}},
			{Name: "CandidateExamPresentationResponse", DTO: reflect.TypeOf(candidateExamPresentationResponse{}), Required: []string{"attempt_id", "exam_sitting_id", "admission_revision_id", "current_revision_id", "title", "instructions_markdown", "focus_loss_collection_enabled", "resources"}},
			{Name: "CandidateExamResourceResponse", DTO: reflect.TypeOf(candidateExamResourceResponse{}), Required: []string{"id", "display_name", "description_markdown", "position", "media_type", "size", "sha256"}},
			{Name: "CandidateExamWorkspaceItemResponse", DTO: reflect.TypeOf(candidateExamWorkspaceItemResponse{}), Required: []string{"id", "kind", "path"}},
			{Name: "CandidateExamWorkspaceListResponse", DTO: reflect.TypeOf(candidateExamWorkspaceListResponse{}), Required: []string{"workspace_id", "workspace_cursor", "items", "refresh_required"}},
			{Name: "CreateCandidateWorkspaceDirectoryRequest", DTO: reflect.TypeOf(createCandidateWorkspaceDirectoryRequest{}), Required: []string{"participation_id", "generation", "path"}},
			{Name: "MoveCandidateWorkspaceEntryRequest", DTO: reflect.TypeOf(moveCandidateWorkspaceEntryRequest{}), Required: []string{"participation_id", "generation", "expected_path", "destination_path"}},
			{Name: "DeleteCandidateWorkspaceEntryRequest", DTO: reflect.TypeOf(deleteCandidateWorkspaceEntryRequest{}), Required: []string{"participation_id", "generation", "expected_path"}},
			{Name: "CandidateWorkspaceMutationResponse", DTO: reflect.TypeOf(candidateWorkspaceMutationResponse{}), Required: []string{"workspace_id", "workspace_cursor", "operation"}},
			{Name: "CandidateWorkspaceJournalEntryResponse", DTO: reflect.TypeOf(candidateWorkspaceJournalEntryResponse{}), Required: []string{"cursor", "entry_id", "kind", "operation", "changed_at"}},
			{Name: "CandidateWorkspaceJournalResponse", DTO: reflect.TypeOf(candidateWorkspaceJournalResponse{}), Required: []string{"workspace_id", "current_cursor", "entries", "has_more", "refresh_required"}},
			{Name: "SubmitExamAttemptRequest", DTO: reflect.TypeOf(submitExamAttemptRequest{}), Required: []string{"participation_id", "generation", "expected_workspace_cursor", "final_focus_loss_sequence"}},
			{Name: "ExamSubmissionReceiptResponse", DTO: reflect.TypeOf(examSubmissionReceiptResponse{}), Required: []string{"submission_id", "exam_attempt_id", "state", "workspace_cursor", "manifest_digest", "submitted_at"}},
			{Name: "ExamSubmissionManagerResponse", DTO: reflect.TypeOf(examSubmissionManagerResponse{}), Required: []string{"submission_id", "exam_id", "exam_sitting_id", "exam_attempt_id", "workspace_id", "manifest_schema_version", "workspace_cursor", "manifest_digest", "manifest_entry_count", "manifest_total_file_bytes", "final_focus_loss_sequence", "integrity_state", "unresolved_integrity_count", "submitted_at"}},
			{Name: "ExamSubmissionManifestItemResponse", DTO: reflect.TypeOf(examSubmissionManifestItemResponse{}), Required: []string{"entry_id", "kind", "path"}},
			{Name: "ExamSubmissionManifestResponse", DTO: reflect.TypeOf(examSubmissionManifestResponse{}), Required: []string{"submission_id", "workspace_cursor", "manifest_digest", "items"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examAttemptResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	for _, path := range []string{presentation, workspace, workspaceChanges, workspaceDirectories, workspaceFiles, workspaceEntry, resourceContent, workspaceContent, candidateSubmissions} {
		method := "get"
		if path == workspaceDirectories || path == workspaceFiles {
			method = "post"
		}
		if path == candidateSubmissions {
			method = "post"
		}
		if path == workspaceEntry {
			method = "patch"
		}
		operation := decodeExamContentOpenAPIOperation(t, document, path, method)
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
	assertProtectedExamContentResponse(t, document, managerSubmissionContent, "ExamSubmissionFileContent")
	managerContentOperation := decodeExamContentOpenAPIOperation(t, document, managerSubmissionContent, "get")
	if managerContentOperation.Responses["304"].Ref != "#/components/responses/ExamSubmissionFileNotModified" {
		t.Errorf("GET %s 304 response = %#v", managerSubmissionContent, managerContentOperation.Responses["304"])
	}
	assertProtectedExamCacheControl(t, "ExamSubmissionFileContent", "private, no-store")
	if _, ok := document.Components.Responses["ExamSubmissionFileContent"].Headers["Content-Disposition"]; ok {
		t.Error("Submission protected response documents forbidden Content-Disposition")
	}
	assertProtectedExamCacheControl(t, "CandidateExamProtectedContent", "private, no-store")
	if _, ok := document.Components.Responses["CandidateExamProtectedContent"].Headers["Content-Disposition"]; ok {
		t.Error("candidate protected response documents forbidden Content-Disposition")
	}
	assertExamAttemptQueryParameters(t, document, managerBase, []string{"state", "limit", "cursor"})
	assertExamAttemptQueryParameters(t, document, workspace, []string{"limit", "cursor"})
	assertExamAttemptQueryParameters(t, document, workspaceChanges, []string{"after_cursor", "limit"})
	assertExamAttemptQueryParameters(t, document, managerSubmissionManifest, []string{"limit", "cursor"})
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
