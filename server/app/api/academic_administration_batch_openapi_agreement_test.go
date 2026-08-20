// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"
)

func TestAcademicAdministrationBatchOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{{
			Key: "POST /api/v1/academic-administration-batches", Auth: AuthPrincipalRequired,
			Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/RunAcademicAdministrationBatch",
			RequestSchema: "AcademicAdministrationBatchRequest", SuccessStatus: "200",
			SuccessRef: "#/components/responses/AcademicAdministrationBatchOK", SuccessSchema: "AcademicAdministrationBatchResponse",
			PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "authentication.strong_required",
				"authentication.reauthentication_required", "idempotency.key_required", "idempotency.invalid_key", "administration.unavailable"),
		}},
		Schemas: []openAPIAgreementSchema{
			{Name: "AcademicAdministrationBatchItemRequest", DTO: reflect.TypeOf(academicAdministrationBatchItemRequest{}), Required: []string{"key"}},
			{Name: "AcademicAdministrationBatchRequest", DTO: reflect.TypeOf(academicAdministrationBatchRequest{}), Required: []string{"operation", "scope_type", "scope_id", "items"}},
			{Name: "AcademicAdministrationBatchItemResponse", DTO: reflect.TypeOf(academicAdministrationBatchItemResponse{}), Required: []string{"index", "status"}},
			{Name: "AcademicAdministrationBatchResponse", DTO: reflect.TypeOf(academicAdministrationBatchResponse{}), Required: []string{"operation", "items", "succeeded", "no_op", "failed"}},
		},
		OperationSelector: func(_ string, path string) bool { return path == "/api/v1/academic-administration-batches" },
	}
	runtimeAPI := newRoutingTestAPI("/api/v1")
	if err := runtimeAPI.collectResources("/api/v1", academicAdministrationBatchResource(unavailableAcademicAdministrationBatchApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
