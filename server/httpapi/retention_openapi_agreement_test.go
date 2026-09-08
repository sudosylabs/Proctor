// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"github.com/sudosylabs/proctor/server/model"
	"reflect"
	"strings"
	"testing"
)

func TestRetentionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	read := principalContractCodes("request.invalid", "retention.invalid", "retention.unavailable", "retention_policy.unavailable", "resource.not_found")
	mutate := principalMutationContractCodes("request.invalid", "retention.invalid", "retention.conflict", "retention.unavailable", "retention_policy.unavailable", "retention_policy.revision_conflict", "resource.not_found", "idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/retention/control", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionControlOK", SuccessSchema: "RetentionControlResponse", PublicErrorCodes: read},
			{Key: "PUT /api/v1/retention/control", Auth: AuthStrongRecentSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ChangeRetentionControl", RequestSchema: "RetentionControlRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionControlOK", SuccessSchema: "RetentionControlResponse", PublicErrorCodes: append(append([]string(nil), mutate...), "authentication.strong_required", "authentication.reauthentication_required")},
			{Key: "POST /api/v1/retention/previews", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateRetentionPreview", RequestSchema: "RetentionPreviewRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/RetentionPreviewOK", SuccessSchema: "RetentionPreviewResponse", PublicErrorCodes: mutate},
			{Key: "GET /api/v1/retention/previews/{retention_preview_id}", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionPreviewOK", SuccessSchema: "RetentionPreviewResponse", PublicErrorCodes: read},
			{Key: "GET /api/v1/retention/records", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionRecordsOK", SuccessSchema: "RetentionRecordsResponse", PublicErrorCodes: read},
			{Key: "GET /api/v1/users/me/retention-notices", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionNoticesOK", SuccessSchema: "RetentionNoticesResponse", PublicErrorCodes: principalContractCodes("request.invalid", "retention.invalid", "retention.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "RetentionControlRequest", DTO: reflect.TypeOf(retentionControlRequest{}), Required: []string{"expected_revision", "expected_policy_revision", "state"}, NonNullable: []string{"expected_revision", "expected_policy_revision", "state", "preview_id"}},
			{Name: "RetentionPreviewRequest", DTO: reflect.TypeOf(retentionPreviewRequest{}), Required: []string{"expected_policy_revision"}},
			{Name: "RetentionControlResponse", DTO: reflect.TypeOf(retentionControlResponse{}), Required: []string{"revision", "state", "updated_at"}},
			{Name: "RetentionPreviewResponse", DTO: reflect.TypeOf(retentionPreviewResponse{}), Required: []string{"id", "policy_revision", "created_at", "expires_at", "work", "integrity", "browser_activity", "security_operational", "audit", "receipts"}},
			{Name: "RetentionPreviewCounts", DTO: reflect.TypeOf(retentionPreviewCountsResponse{}), Required: []string{"total", "eligible", "awaiting_deadline", "incomplete", "held", "unconfigured", "supporting_work", "export_protected", "retired"}},
			{Name: "RetentionExpiryCounts", DTO: reflect.TypeOf(retentionExpiryCountsResponse{}), Required: []string{"total", "eligible", "awaiting_deadline", "unconfigured", "unfinished", "referenced", "held", "purge_pending", "source_protected"}},
			{Name: "RetentionRecordsResponse", DTO: reflect.TypeOf(retentionRecordsResponse{}), Required: []string{"policy_revision", "as_of", "items"}},
			{Name: "RetentionRecord", DTO: reflect.TypeOf(retentionRecordResponse{}), Required: []string{"exam_id", "exam_sitting_id", "submission_id", "category", "completion_revision", "completion_current", "has_integrity", "held", "shared_published_objects", "blocker"}},
			{Name: "RetentionRetirement", DTO: reflect.TypeOf(retirementResponse{}), Required: []string{"id", "state", "policy_revision", "control_revision", "completion_revision", "scheduled_at", "retire_after", "purge_pending", "purge_verified"}},
			{Name: "RetentionNoticeResponse", DTO: reflect.TypeOf(retentionNoticeResponse{}), Required: []string{"retirement_id", "exam_id", "exam_sitting_id", "submission_id", "category", "state", "created_at", "retire_after", "delivery_state"}},
			{Name: "RetentionNoticesResponse", DTO: reflect.TypeOf(retentionNoticesResponse{}), Required: []string{"items"}},
		},
		OperationSelector: func(_ string, path string) bool {
			return strings.HasPrefix(path, "/api/v1/retention/") || path == "/api/v1/users/me/retention-notices"
		},
	}
	api := newRoutingTestAPI(model.APIURLSuffix)
	if err := api.collectResources(model.APIURLSuffix, retentionResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, api.Routes())
	document := readOpenAPIDocument(t)
	for _, name := range []string{"RetentionControlOK", "RetentionPreviewOK", "RetentionRecordsOK", "RetentionNoticesOK"} {
		if document.Components.Responses[name].Headers["Cache-Control"].Ref != "#/components/headers/PrivateNoStore" {
			t.Fatalf("%s lacks no-store", name)
		}
	}
}
