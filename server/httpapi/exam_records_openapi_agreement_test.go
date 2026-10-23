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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestExamRecordsOpenAPIAgreesWithRuntime(t *testing.T) {
	completion := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/records-completion"
	waiver := "/api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/submissions/{submission_id}/review-waiver"
	holds := "/api/v1/exams/{exam_id}/retention-holds"
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET " + completion, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ExamRecordsSnapshotOK", SuccessSchema: "ExamRecordsSnapshotResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"}},
		{Key: "POST " + completion, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CompleteExamRecords", RequestSchema: "CompleteExamRecordsRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ExamRecordsCompletionOK", SuccessSchema: "ExamRecordsCompletionResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "GET " + waiver, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/SubmissionReviewWaiverOK", SuccessSchema: "SubmissionReviewWaiverEnvelope", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"}},
		{Key: "POST " + waiver, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/WaiveSubmissionReview", RequestSchema: "WaiveSubmissionReviewRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/SubmissionReviewWaiverOK", SuccessSchema: "SubmissionReviewWaiverEnvelope", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "GET " + holds, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionHoldPageOK", SuccessSchema: "RetentionHoldPageResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.unavailable"}},
		{Key: "POST " + holds, Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/CreateRetentionHold", RequestSchema: "CreateRetentionHoldRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/RetentionHoldCreated", SuccessSchema: "RetentionHoldResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "POST " + holds + "/{retention_hold_id}/release", Auth: AuthStrongRecentSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ReleaseRetentionHold", RequestSchema: "ReleaseRetentionHoldRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/RetentionHoldOK", SuccessSchema: "RetentionHoldResponse", PublicErrorCodes: []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.invalid", "exam.conflict", "exam.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"}},
	}, Schemas: []openAPIAgreementSchema{
		{Name: "CompleteExamRecordsRequest", DTO: reflect.TypeOf(completeExamRecordsRequest{}), Required: []string{"expected_revision", "acknowledged_evidence_revision"}, NonNullable: []string{"expected_revision", "acknowledged_evidence_revision"}},
		{Name: "WaiveSubmissionReviewRequest", DTO: reflect.TypeOf(waiveSubmissionReviewRequest{}), Required: []string{"expected_revision", "expected_review_revision", "expected_discrepancy_count", "reason_code", "private_reason"}, NonNullable: []string{"expected_revision", "expected_review_revision", "expected_discrepancy_count", "expected_delivery_inventory_revision"}},
		{Name: "CreateRetentionHoldRequest", DTO: reflect.TypeOf(createRetentionHoldRequest{}), Required: []string{"reason_code", "private_reason"}, NonNullable: []string{"exam_sitting_id", "submission_id"}},
		{Name: "ReleaseRetentionHoldRequest", DTO: reflect.TypeOf(releaseRetentionHoldRequest{}), Required: []string{"expected_revision", "reason_code", "private_reason"}, NonNullable: []string{"expected_revision", "exam_sitting_id", "submission_id"}},
		{Name: "ExamRecordsCompletionResponse", DTO: reflect.TypeOf(examRecordsCompletionResponse{}), Required: []string{"exam_sitting_id", "revision", "evidence_revision", "completed_evidence_revision", "current"}},
		{Name: "ExamRecordsSnapshotResponse", DTO: reflect.TypeOf(examRecordsSnapshotResponse{}), Required: []string{"completion", "sitting_state", "submission_count", "pending_reviews"}},
		{Name: "SubmissionReviewWaiverResponse", DTO: reflect.TypeOf(submissionReviewWaiverResponse{}), Required: []string{"submission_id", "revision", "review_revision", "discrepancy_count", "actor_user_id", "recorded_at", "reason_code", "private_reason", "inventory_invalidated", "delivery_inventory_revision"}},
		{Name: "SubmissionReviewWaiverEnvelope", DTO: reflect.TypeOf(submissionReviewWaiverEnvelope{}), Required: []string{"waiver"}},
		{Name: "RetentionHoldResponse", DTO: reflect.TypeOf(retentionHoldResponse{}), Required: []string{"id", "exam_id", "revision", "created_at", "created_by_user_id", "reason_code", "private_reason", "work_retired_submission_count", "integrity_retired_submission_count"}},
		{Name: "RetentionHoldPageResponse", DTO: reflect.TypeOf(retentionHoldPageResponse{}), Required: []string{"items"}},
	}, OperationSelector: func(_ string, path string) bool {
		return strings.Contains(path, "/records-completion") || strings.Contains(path, "/review-waiver") || strings.Contains(path, "/retention-holds")
	}}
	api := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, api.collectResources(model.APIURLSuffix, examRecordsResource(nil)))
	assertOpenAPIAgreement(t, suite, api.Routes())
	doc := readOpenAPIDocument(t)
	for _, name := range []string{"ExamRecordsSnapshotOK", "ExamRecordsCompletionOK", "SubmissionReviewWaiverOK", "RetentionHoldPageOK", "RetentionHoldOK", "RetentionHoldCreated"} {
		require.Equal(t, "#/components/headers/PrivateNoStore", doc.Components.Responses[name].Headers["Cache-Control"].Ref, "%s lacks private no-store", name)
	}
}
