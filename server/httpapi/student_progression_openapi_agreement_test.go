// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestStudentProgressionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable", "student_progression.unavailable")
	mutationCodes := principalMutationContractCodes("request.invalid", "resource.not_found", "student_progression.target_conflict", "student_progression.lineage_conflict",
		"student_progression.effective_date_conflict", "student_progression.roster_too_large", "student_progression.conflict", "student_progression.unavailable", "administration.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST /api/v1/student-progressions", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/DryRunStudentProgression", RequestSchema: "StudentProgressionDryRunRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/StudentProgressionAccepted", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/student-progressions/{student_progression_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/StudentProgressionOK", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: readCodes},
			{Key: "POST /api/v1/student-progressions/{student_progression_id}/commit", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CommitStudentProgression", RequestSchema: "StudentProgressionCommitRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/StudentProgressionAccepted", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: append(mutationCodes, "idempotency.key_required", "idempotency.invalid_key")},
			{Key: "POST /api/v1/student-progressions/{student_progression_id}/cancel", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/StudentProgressionOK", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/student-progressions/{student_progression_id}/report", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/StudentProgressionReport", ExceptionalSuccess: true, PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable", "student_progression.conflict", "student_progression.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "StudentProgressionDryRunRequest", DTO: reflect.TypeOf(studentProgressionDryRunRequest{}), Required: []string{"source_period_id", "source_class_id", "destination_period_id", "destination_class_id", "effective_at"}},
			{Name: "StudentProgressionCommitRequest", DTO: reflect.TypeOf(studentProgressionCommitRequest{}), Required: []string{"expected_revision", "preview_digest"}},
		},
		OperationSelector: func(_ string, path string) bool { return strings.HasPrefix(path, "/api/v1/student-progressions") },
	}
	runtimeAPI := newRoutingTestAPI("/api/v1")
	if err := runtimeAPI.collectResources("/api/v1", studentProgressionResource(unavailableStudentProgressionApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
