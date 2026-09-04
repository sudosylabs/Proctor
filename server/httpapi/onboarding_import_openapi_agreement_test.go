// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestOnboardingImportOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	readCodes := principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable", "onboarding_import.unavailable")
	mutationCodes := principalMutationContractCodes("request.invalid", "resource.not_found", "onboarding_import.conflict", "onboarding_import.invalid_file", "onboarding_import.unavailable", "administration.unavailable")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST /api/v1/onboarding-imports", Auth: AuthPrincipalRequired, SuccessStatus: "202", SuccessRef: "#/components/responses/OnboardingImportAccepted", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/onboarding-imports/{onboarding_import_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/OnboardingImportOK", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: readCodes},
			{Key: "POST /api/v1/onboarding-imports/{onboarding_import_id}/commit", Auth: AuthPrincipalRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CommitOnboardingImport", RequestSchema: "OnboardingImportCommitRequest", SuccessStatus: "202", SuccessRef: "#/components/responses/OnboardingImportAccepted", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "onboarding_import.conflict", "onboarding_import.invalid_file", "onboarding_import.unavailable", "idempotency.key_required", "idempotency.invalid_key", "administration.unavailable")},
			{Key: "POST /api/v1/onboarding-imports/{onboarding_import_id}/cancel", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/OnboardingImportOK", SuccessSchema: "OnboardingImportResponse", PublicErrorCodes: mutationCodes},
			{Key: "GET /api/v1/onboarding-imports/{onboarding_import_id}/report", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/OnboardingImportReport", ExceptionalSuccess: true, PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable", "onboarding_import.conflict", "onboarding_import.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "OnboardingImportCommitRequest", DTO: reflect.TypeOf(onboardingImportCommitRequest{}), Required: []string{"expected_revision", "preview_digest", "policy"}},
			{Name: "OnboardingImportRowResponse", DTO: reflect.TypeOf(onboardingImportRowResponse{}), Required: []string{"row", "reference", "operation", "status"}},
			{Name: "OnboardingImportResponse", DTO: reflect.TypeOf(onboardingImportResponse{}), Required: []string{"id", "mode", "state", "scope_type", "scope_id", "total_rows", "valid_rows", "invalid_rows", "succeeded_rows", "no_op_rows", "failed_rows", "skipped_rows", "parse_job_id", "created_at", "updated_at", "expires_at", "revision"}},
		},
		OperationSelector: func(_ string, path string) bool { return strings.HasPrefix(path, "/api/v1/onboarding-imports") },
	}
	runtimeAPI := newRoutingTestAPI("/api/v1")
	if err := runtimeAPI.collectResources("/api/v1", onboardingImportResource(unavailableOnboardingImportApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
