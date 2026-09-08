// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestNativeDeliveryOpenAPIAgreesWithRuntime(t *testing.T) {
	base := "/api/v1/exam-attempts/{exam_attempt_id}/security-streams/{stream_id}"
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "POST /api/v1/exam-attempts/{exam_attempt_id}/security-batches", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/NativeSecurityBatch", RequestSchema: "NativeSecurityBatch", SuccessStatus: "200", SuccessRef: "#/components/responses/NativeSecurityAcknowledgementOK", SuccessSchema: "NativeSecurityAcknowledgement", PublicErrorCodes: nativeDeliveryAppendErrorCodes()},
		{Key: "GET " + base, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/NativeSecurityStreamStatusOK", SuccessSchema: "NativeSecurityStreamStatus", PublicErrorCodes: nativeDeliveryErrorCodes()},
		{Key: "GET " + base + "/receipts/{batch_sequence}", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/NativeBatchReceiptOK", SuccessSchema: "NativeBatchReceipt", PublicErrorCodes: nativeDeliveryErrorCodes()},
		{Key: "POST " + base + "/gaps", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/DeclareDeliveryGaps", RequestSchema: "DeclareDeliveryGaps", SuccessStatus: "200", SuccessRef: "#/components/responses/DeliveryGapReceiptOK", SuccessSchema: "DeliveryGapReceipt", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
		{Key: "POST " + base + "/seal", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/FinalDeliveryDeclaration", RequestSchema: "FinalDeliveryDeclaration", SuccessStatus: "200", SuccessRef: "#/components/responses/NativeSecurityStreamStatusOK", SuccessSchema: "NativeSecurityStreamStatus", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
		{Key: "POST " + base + "/summary", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UnretainedDeliverySummary", RequestSchema: "UnretainedDeliverySummary", SuccessStatus: "200", SuccessRef: "#/components/responses/NativeSecurityStreamStatusOK", SuccessSchema: "NativeSecurityStreamStatus", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
	}, Schemas: []openAPIAgreementSchema{
		{Name: "NativeSecurityBatch", DTO: reflect.TypeOf(model.NativeSecurityBatch{}), Required: []string{"stream_id", "batch_sequence", "prior_acknowledgement", "participation_id", "generation", "security_session_id", "policy_digest", "application_release_id", "matrix_id", "records"}},
		{Name: "NativeOccurrence", DTO: reflect.TypeOf(model.NativeOccurrence{}), Required: []string{"kind", "occurrence_id", "condition_id", "detector_id", "detector_version", "capability_id", "mode", "status", "first_observed_at", "last_confirmed_at", "repeat_count", "certainty", "gap_count", "source_ranges"}},
		{Name: "NativeSourceReset", DTO: reflect.TypeOf(model.NativeSourceReset{}), Required: []string{"kind", "reset_id", "source_id", "previous_source_instance_id", "previous_final_sequence", "new_source_instance_id", "reason", "occurred_at"}},
		{Name: "NativeSourceGap", DTO: reflect.TypeOf(model.NativeSourceGap{}), Required: []string{"kind", "source_id", "source_instance_id", "first_missing_sequence", "last_missing_sequence", "reason", "occurred_at"}},
		{Name: "NativeCoverageTransition", DTO: reflect.TypeOf(model.NativeCoverageTransition{}), Required: []string{"kind", "source", "occurred_at", "reason"}},
		{Name: "NativeSecurityAcknowledgement", DTO: reflect.TypeOf(model.NativeSecurityAcknowledgement{}), Required: []string{"receipt", "highest_contiguous_batch_sequence", "settled_through_batch_sequence", "highest_seen_batch_sequence", "missing_batch_ranges", "missing_ranges_truncated", "server_time"}},

		{Name: "NativeBatchReceipt", DTO: reflect.TypeOf(model.NativeBatchReceipt{}), Required: []string{"stream_id", "batch_sequence", "request_digest", "received_at"}},
		{Name: "DeclareDeliveryGaps", DTO: reflect.TypeOf(model.DeclareDeliveryGaps{}), Required: []string{"declaration_id", "expected_declaration_revision", "allocated_through_sequence", "ranges", "reason"}},
		{Name: "FinalDeliveryDeclaration", DTO: reflect.TypeOf(model.FinalDeliveryDeclaration{}), Required: []string{"declaration_id", "expected_declaration_revision", "final_sequence"}},
		{Name: "DeliveryGapReceipt", DTO: reflect.TypeOf(model.DeliveryGapReceipt{}), Required: []string{"declaration_id", "request_digest", "declaration_revision", "settled_through_sequence"}},
		{Name: "UnretainedDeliverySummary", DTO: reflect.TypeOf(model.UnretainedDeliverySummary{}), Required: []string{"summary_sequence", "unretained_record_count", "count_complete", "first_unretained_at", "last_unretained_at"}, Nullable: []string{"first_unretained_at", "last_unretained_at"}},
		{Name: "DeliveryClosure", DTO: reflect.TypeOf(model.DeliveryClosure{}), Required: []string{"closed_at", "close_reason", "known_at_close", "final_sequence", "final_declaration_id", "final_boundary_origin", "unknown_tail", "upload_expires_at"}, Nullable: []string{"closed_at", "close_reason", "known_at_close", "final_sequence", "final_declaration_id", "final_boundary_origin", "upload_expires_at"}},
		{Name: "NativeSecurityStreamStatus", DTO: reflect.TypeOf(model.NativeSecurityStreamStatus{}), Required: []string{"highest_contiguous_batch_sequence", "settled_through_batch_sequence", "highest_seen_batch_sequence", "missing_batch_ranges", "missing_ranges_truncated", "server_time", "stream_id", "allocated_through_sequence", "terminal_missing_through_sequence", "closure", "declaration_revision", "detail_mode", "budget_scope", "summary_only_reason", "summary"}, Nullable: []string{"budget_scope", "summary_only_reason", "summary", "summary.first_unretained_at", "summary.last_unretained_at", "closure.closed_at", "closure.close_reason", "closure.known_at_close", "closure.final_sequence", "closure.final_declaration_id", "closure.final_boundary_origin", "closure.upload_expires_at"}},
	}}
	runtime := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtime.collectResources(model.APIURLSuffix, nativeDeliveryResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtime.Routes())
}
