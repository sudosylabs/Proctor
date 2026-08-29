// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

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
	candidateStatuses := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/candidate-statuses"
	managerMember := managerBase + "/{exam_attempt_id}"
	managerBrowserActivity := managerMember + "/browser-activity"
	managerEnd := managerMember + "/end"
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
	correctionAcknowledgement := candidateBase + "/corrections/{exam_revision_id}/acknowledgement"
	managerSubmission := managerMember + "/submissions/{submission_id}"
	managerSubmissionManifest := managerSubmission + "/manifest"
	managerSubmissionContent := managerSubmission + "/files/{attempt_workspace_entry_id}/content"
	managerCodes := principalContractCodes("request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "administration.unavailable")
	reallowCodes := principalMutationContractCodes("request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.suspension_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.conflict", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "administration.unavailable")
	managerEndCodes := principalMutationContractCodes("request.invalid", "resource.not_found",
		"exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.management_authority_conflict", "exam.attempt.conflict",
		"exam.attempt.unavailable", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict",
		"idempotency.in_progress", "administration.unavailable")
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
		"exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.browser_activity_conflict",
		"exam.attempt.connection_lost")
	correctionAcknowledgementCodes := personalAccessTokenSessionMutationCodes("audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid",
		"exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.state_conflict",
		"exam.attempt.sitting_unavailable", "exam.attempt.connection_closed", "exam.attempt.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + managerBase, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerListOK", SuccessSchema: "ExamAttemptManagerListResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + candidateStatuses, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSittingCandidateStatusesOK", SuccessSchema: "ExamSittingCandidateStatusesResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerMember, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamAttemptManagerOK", SuccessSchema: "ExamAttemptManagerResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerBrowserActivity, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserActivityListOK", SuccessSchema: "BrowserActivityListResponse", PublicErrorCodes: managerCodes},
			{Key: "POST " + managerEnd, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/EndExamAttemptByManager", RequestSchema: "EndExamAttemptByManagerRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamSubmissionReceiptOK",
				SuccessSchema: "ExamSubmissionReceiptResponse", PublicErrorCodes: managerEndCodes},
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
			{Key: "PUT " + correctionAcknowledgement, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/AcknowledgeExamCorrection", RequestSchema: "AcknowledgeExamCorrectionRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExamCorrectionAcknowledged",
				SuccessSchema: "AcknowledgeExamCorrectionResponse", PublicErrorCodes: correctionAcknowledgementCodes},
			{Key: "GET " + managerSubmission, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionManagerOK", SuccessSchema: "ExamSubmissionManagerResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerSubmissionManifest, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionManifestOK", SuccessSchema: "ExamSubmissionManifestResponse", PublicErrorCodes: managerCodes},
			{Key: "GET " + managerSubmissionContent, Auth: AuthPrincipalRequired, SuccessStatus: "200",
				SuccessRef: "#/components/responses/ExamSubmissionFileContent", ExceptionalSuccess: true, PublicErrorCodes: managerCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ExamSittingCandidateStatusesResponse", DTO: reflect.TypeOf(sittingCandidateStatusListResponse{}), Required: []string{"server_time", "exam_id", "exam_sitting_id", "sitting_state", "sitting_revision", "items"}, Nullable: []string{"items.attempt", "items.attempt.submission", "items.presence.last_lease_renewed_at", "items.presence.lease_expires_at", "items.suspension"}},
			{Name: "SittingCandidateStatusItemResponse", DTO: reflect.TypeOf(sittingCandidateStatusItemResponse{}), Required: []string{"candidate", "current_class_membership", "attempt", "presence", "suspension", "integrity_attention_count"}, Nullable: []string{"attempt", "attempt.submission", "presence.last_lease_renewed_at", "presence.lease_expires_at", "suspension"}},
			{Name: "SittingCandidateIdentityResponse", DTO: reflect.TypeOf(sittingCandidateIdentityResponse{}), Required: []string{"user_id", "username", "display_name"}},
			{Name: "SittingCandidateAttemptResponse", DTO: reflect.TypeOf(sittingCandidateAttemptResponse{}), Required: []string{"id", "state", "revision", "created_at", "updated_at", "submission"}, Nullable: []string{"submission"}},
			{Name: "SittingCandidateSubmissionResponse", DTO: reflect.TypeOf(sittingCandidateSubmissionResponse{}), Required: []string{"id", "submitted_at", "provenance"}},
			{Name: "SittingCandidatePresenceResponse", DTO: reflect.TypeOf(sittingCandidatePresenceResponse{}), Required: []string{"state", "last_lease_renewed_at", "lease_expires_at"}, Nullable: []string{"last_lease_renewed_at", "lease_expires_at"}},
			{Name: "SittingCandidateSuspensionResponse", DTO: reflect.TypeOf(sittingCandidateSuspensionResponse{}), Required: []string{"id", "candidate_reason", "reallow_available"}},
			{Name: "ExamAttemptManagerResponse", DTO: reflect.TypeOf(examAttemptManagerResponse{}), Required: []string{"id", "exam_id", "exam_sitting_id", "candidate_user_id", "admission_revision_id", "state", "created_at", "updated_at", "revision", "workspace"}},
			{Name: "ExamAttemptWorkspaceResponse", DTO: reflect.TypeOf(examAttemptWorkspaceResponse{}), Required: []string{"id", "cursor", "created_at", "updated_at"}},
			{Name: "ExamAttemptParticipationResponse", DTO: reflect.TypeOf(examAttemptParticipationResponse{}), Required: []string{"id", "state", "generation", "renewal_sequence", "started_at", "updated_at", "lease_expires_at"}},
			{Name: "ExamAttemptConnectionResponse", DTO: reflect.TypeOf(examAttemptConnectionResponse{}), Required: []string{"id", "state", "opened_at"}},
			{Name: "ExamAttemptSuspensionResponse", DTO: reflect.TypeOf(examAttemptSuspensionResponse{}), Required: []string{"id", "participation_id", "flag_id", "generation", "state", "source", "candidate_reason", "started_at"}},
			{Name: "ExamAttemptManagerListResponse", DTO: reflect.TypeOf(examAttemptManagerListResponse{}), Required: []string{"items"}},
			{Name: "ReallowExamAttemptRequest", DTO: reflect.TypeOf(reallowExamAttemptRequest{}), Required: []string{"suspension_id", "expected_attempt_revision", "reason"}},
			{Name: "EndExamAttemptByManagerRequest", DTO: reflect.TypeOf(endExamAttemptByManagerRequest{}), Required: []string{"expected_attempt_revision", "reason"}},
			{Name: "ExamAttemptReallowResponse", DTO: reflect.TypeOf(examAttemptReallowResponse{}), Required: []string{"exam_attempt_id", "exam_sitting_id", "state", "attempt_revision", "suspension_id", "suspension_state", "candidate_reason", "reallowed_by_user_id"}},
			{Name: "CandidateExamPresentationResponse", DTO: reflect.TypeOf(candidateExamPresentationResponse{}), Required: []string{"attempt_id", "exam_sitting_id", "title", "instructions_markdown", "candidate_runtime_capabilities", "browser_policy", "live_corrections", "resources"}, Nullable: []string{"browser_policy", "live_corrections.acknowledged_at"}},
			{Name: "CandidateBrowserPolicy", DTO: reflect.TypeOf(candidateBrowserPolicyResponse{}), Required: []string{"schema_version", "enabled", "start_rule_id", "rules", "policy_revision_id", "policy_digest"}},
			{Name: "CandidateLiveCorrection", DTO: reflect.TypeOf(candidateLiveCorrectionResponse{}), Required: []string{"revision_id", "revision_number", "effective_at", "summary", "changed_areas", "acknowledgement_required", "acknowledgement_state", "acknowledged_at"}, Nullable: []string{"acknowledged_at"}},
			{Name: "CandidateExamResourceResponse", DTO: reflect.TypeOf(candidateExamResourceResponse{}), Required: []string{"id", "display_name", "description_markdown", "position", "media_type", "size", "sha256"}},
			{Name: "CandidateExamWorkspaceItemResponse", DTO: reflect.TypeOf(candidateExamWorkspaceItemResponse{}), Required: []string{"id", "kind", "path"}},
			{Name: "CandidateExamWorkspaceListResponse", DTO: reflect.TypeOf(candidateExamWorkspaceListResponse{}), Required: []string{"workspace_id", "workspace_cursor", "items", "refresh_required"}},
			{Name: "CreateCandidateWorkspaceDirectoryRequest", DTO: reflect.TypeOf(createCandidateWorkspaceDirectoryRequest{}), Required: []string{"participation_id", "generation", "path"}},
			{Name: "MoveCandidateWorkspaceEntryRequest", DTO: reflect.TypeOf(moveCandidateWorkspaceEntryRequest{}), Required: []string{"participation_id", "generation", "expected_path", "destination_path"}},
			{Name: "DeleteCandidateWorkspaceEntryRequest", DTO: reflect.TypeOf(deleteCandidateWorkspaceEntryRequest{}), Required: []string{"participation_id", "generation", "expected_path"}},
			{Name: "CandidateWorkspaceMutationResponse", DTO: reflect.TypeOf(candidateWorkspaceMutationResponse{}), Required: []string{"workspace_id", "workspace_cursor", "operation"}},
			{Name: "CandidateWorkspaceJournalEntryResponse", DTO: reflect.TypeOf(candidateWorkspaceJournalEntryResponse{}), Required: []string{"cursor", "entry_id", "kind", "operation", "changed_at"}},
			{Name: "CandidateWorkspaceJournalResponse", DTO: reflect.TypeOf(candidateWorkspaceJournalResponse{}), Required: []string{"workspace_id", "current_cursor", "entries", "has_more", "refresh_required"}},
			{Name: "BrowserActivityListResponse", DTO: reflect.TypeOf(browserActivityListResponse{}), Required: []string{"items"}, Nullable: []string{"items.location", "items.matched_rule_id", "items.block_reason"}},
			{Name: "BrowserActivityItemResponse", DTO: reflect.TypeOf(browserActivityItemResponse{}), Required: []string{"source_session_id", "generation", "sequence", "kind", "policy_revision_id", "client_occurred_at", "location", "matched_rule_id", "block_reason", "received_at"}, Nullable: []string{"location", "matched_rule_id", "block_reason"}},
			{Name: "BrowserActivitySubmission", DTO: reflect.TypeOf(browserActivitySubmissionRequest{}), Required: []string{"state"}},
			{Name: "SubmitExamAttemptRequest", DTO: reflect.TypeOf(submitExamAttemptRequest{}), Required: []string{"participation_id", "generation", "expected_current_revision_id", "expected_workspace_cursor", "final_focus_loss_sequence", "browser_activity"}, NonNullable: []string{"browser_activity.final_sequence"}},
			{Name: "AcknowledgeExamCorrectionRequest", DTO: reflect.TypeOf(acknowledgeExamCorrectionRequest{}), Required: []string{"participation_id", "generation", "expected_current_revision_id"}},
			{Name: "AcknowledgeExamCorrectionResponse", DTO: reflect.TypeOf(acknowledgeExamCorrectionResponse{}), Required: []string{"revision_id", "acknowledgement_state", "acknowledged_at"}},
			{Name: "ExamSubmissionReceiptResponse", DTO: reflect.TypeOf(examSubmissionReceiptResponse{}), Required: []string{"submission_id", "exam_attempt_id", "exam_revision_id", "state", "workspace_cursor", "manifest_digest", "submitted_at"}},
			{Name: "ExamSubmissionManagerResponse", DTO: reflect.TypeOf(examSubmissionManagerResponse{}), Required: []string{"submission_id", "exam_id", "exam_sitting_id", "exam_attempt_id", "exam_revision_id", "workspace_id", "manifest_schema_version", "workspace_cursor", "manifest_digest", "manifest_entry_count", "manifest_total_file_bytes", "final_focus_loss_sequence", "integrity_state", "unresolved_integrity_count", "submitted_at"}},
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
	for _, path := range []string{presentation, workspace, workspaceChanges, workspaceDirectories, workspaceFiles, workspaceEntry, resourceContent, workspaceContent, candidateSubmissions, correctionAcknowledgement} {
		method := "get"
		if path == workspaceDirectories || path == workspaceFiles {
			method = "post"
		}
		if path == candidateSubmissions {
			method = "post"
		}
		if path == correctionAcknowledgement {
			method = "put"
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
	assertExamAttemptQueryParameters(t, document, managerBrowserActivity, []string{"limit", "cursor"})
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
