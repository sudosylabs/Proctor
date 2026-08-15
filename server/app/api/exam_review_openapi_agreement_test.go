// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamIntegrityReviewOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	base := "/api/v1/submissions/{submission_id}"
	flags := base + "/integrity-flags"
	evidence := flags + "/{integrity_flag_id}/evidence"
	discrepancies := base + "/integrity-discrepancies"
	review := base + "/review"
	decision := review + "/decisions/{integrity_flag_id}"
	finalize := review + "/finalize"
	release := review + "/release"
	result := "/api/v1/exam-attempts/{exam_attempt_id}/result"
	readCodes := principalContractCodes("request.invalid", "resource.not_found",
		"exam.integrity_review.invalid", "exam.integrity_review.unavailable", "administration.unavailable")
	mutationCodes := principalMutationContractCodes("request.invalid", "resource.not_found",
		"exam.integrity_review.invalid", "exam.integrity_review.revision_conflict",
		"exam.integrity_review.state_conflict", "exam.integrity_review.incomplete",
		"exam.integrity_review.too_large", "exam.integrity_review.unavailable",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict",
		"idempotency.in_progress", "administration.unavailable")
	studentCodes := personalAccessTokenSessionCodes("request.invalid", "resource.not_found",
		"exam.integrity_review.invalid", "exam.integrity_review.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET " + flags, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityFlagListOK", SuccessSchema: "ExamIntegrityFlagListResponse", PublicErrorCodes: readCodes},
			{Key: "GET " + evidence, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityEvidenceListOK", SuccessSchema: "ExamIntegrityEvidenceListResponse", PublicErrorCodes: readCodes},
			{Key: "GET " + discrepancies, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityDiscrepancyListOK", SuccessSchema: "ExamIntegrityDiscrepancyListResponse", PublicErrorCodes: readCodes},
			{Key: "GET " + review, Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityReviewOK", SuccessSchema: "ExamIntegrityReviewResponse", PublicErrorCodes: readCodes},
			{Key: "PUT " + review, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UpdateExamIntegrityReview", RequestSchema: "UpdateExamIntegrityReviewRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityReviewMutationOK", SuccessSchema: "ExamIntegrityReviewMutationResponse", PublicErrorCodes: mutationCodes},
			{Key: "PUT " + decision, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/SaveExamIntegrityDecision", RequestSchema: "SaveExamIntegrityDecisionRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityReviewMutationOK", SuccessSchema: "ExamIntegrityReviewMutationResponse", PublicErrorCodes: mutationCodes},
			{Key: "POST " + finalize, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/TerminalExamIntegrityReview", RequestSchema: "TerminalExamIntegrityReviewRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityReviewMutationOK", SuccessSchema: "ExamIntegrityReviewMutationResponse", PublicErrorCodes: mutationCodes},
			{Key: "POST " + release, Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/TerminalExamIntegrityReview", RequestSchema: "TerminalExamIntegrityReviewRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamIntegrityReviewMutationOK", SuccessSchema: "ExamIntegrityReviewMutationResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET " + result, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/StudentExamResultOK", SuccessSchema: "StudentExamResultResponse", PublicErrorCodes: studentCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "SaveExamIntegrityDecisionRequest", DTO: reflect.TypeOf(saveExamIntegrityDecisionRequest{}), Required: []string{"expected_review_revision", "expected_decision_revision", "outcome", "private_rationale"}},
			{Name: "UpdateExamIntegrityReviewRequest", DTO: reflect.TypeOf(updateExamIntegrityReviewRequest{}), Required: []string{"expected_review_revision", "manager_notes", "student_remarks_markdown"}},
			{Name: "TerminalExamIntegrityReviewRequest", DTO: reflect.TypeOf(terminalExamIntegrityReviewRequest{}), Required: []string{"submission_review_id", "expected_review_revision"}},
			{Name: "ExamIntegrityReviewDecisionResponse", DTO: reflect.TypeOf(examIntegrityReviewDecisionResponse{}), Required: []string{"id", "integrity_flag_id", "outcome", "revision", "actor_user_id", "private_rationale", "decided_at"}},
			{Name: "ExamSubmissionReviewResponse", DTO: reflect.TypeOf(examSubmissionReviewResponse{}), Required: []string{"id", "submission_id", "state", "release_state", "revision", "created_by_user_id", "manager_notes", "student_remarks_markdown", "flag_count", "evidence_count", "discrepancy_count", "created_at", "updated_at"}},
			{Name: "ExamIntegrityReviewResponse", DTO: reflect.TypeOf(examIntegrityReviewResponse{}), Required: []string{"submission_id", "exam_id", "exam_sitting_id", "exam_attempt_id", "candidate_user_id", "integrity_state", "unresolved_integrity_count", "decisions"}},
			{Name: "ExamIntegrityReviewMutationResponse", DTO: reflect.TypeOf(examIntegrityReviewMutationResponse{}), Required: []string{"submission_id", "exam_attempt_id", "candidate_user_id", "review"}},
			{Name: "ExamIntegrityFlagResponse", DTO: reflect.TypeOf(examIntegrityFlagResponse{}), Required: []string{"id", "exam_attempt_id", "generation", "policy_kind", "state", "created_at", "evidence_count", "evidence_overflow_count", "unresolved_missing_count"}},
			{Name: "ExamIntegrityFlagListResponse", DTO: reflect.TypeOf(examIntegrityFlagListResponse{}), Required: []string{"items"}},
			{Name: "ExamIntegrityEvidenceResponse", DTO: reflect.TypeOf(examIntegrityEvidenceResponse{}), Required: []string{"id", "exam_attempt_id", "participation_id", "integrity_flag_id", "generation", "policy_kind", "sequence", "duration_milliseconds", "missing_before", "observed_at", "recorded_at"}},
			{Name: "ExamIntegrityEvidenceListResponse", DTO: reflect.TypeOf(examIntegrityEvidenceListResponse{}), Required: []string{"items"}},
			{Name: "ExamIntegrityDiscrepancyResponse", DTO: reflect.TypeOf(examIntegrityDiscrepancyResponse{}), Required: []string{"id", "exam_attempt_id", "participation_id", "generation", "kind", "schema_version", "focus_loss_signal_id", "sequence", "duration_milliseconds", "missing_before", "received_at"}},
			{Name: "ExamIntegrityDiscrepancyListResponse", DTO: reflect.TypeOf(examIntegrityDiscrepancyListResponse{}), Required: []string{"items"}},
			{Name: "StudentExamResultResponse", DTO: reflect.TypeOf(studentExamResultResponse{}), Required: []string{"submission_review_id", "submission_id", "exam_attempt_id", "student_remarks_markdown", "released_at"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, examIntegrityReviewResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	for _, path := range []string{flags, evidence, discrepancies} {
		assertExamAttemptQueryParameters(t, document, path, []string{"limit", "cursor"})
	}
	for _, response := range []string{"ExamIntegrityFlagListOK", "ExamIntegrityEvidenceListOK",
		"ExamIntegrityDiscrepancyListOK", "ExamIntegrityReviewOK", "ExamIntegrityReviewMutationOK", "StudentExamResultOK"} {
		assertProtectedExamCacheControl(t, response, "no-store")
	}
}
