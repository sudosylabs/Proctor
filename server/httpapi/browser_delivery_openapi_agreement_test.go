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

func TestBrowserDeliveryOpenAPIAgreesWithRuntime(t *testing.T) {
	base := "/api/v1/exam-attempts/{exam_attempt_id}/browser-activity/sources"
	required := []string{"highest_contiguous_sequence", "settled_through_sequence", "highest_seen_sequence", "allocated_through_sequence", "terminal_missing_through_sequence", "missing_ranges", "missing_ranges_truncated", "source_session_id", "attempt_id", "participation_id", "generation", "policy_revision_id", "policy_digest", "predecessor_source_session_id", "start_transition", "runtime_reset_reason", "started_at", "closure", "declaration_revision", "detail_mode", "budget_scope", "summary_only_reason", "remaining_correction_starts", "remaining_runtime_reset_starts", "summary", "server_time"}
	nullable := []string{"predecessor_source_session_id", "runtime_reset_reason", "budget_scope", "summary_only_reason", "summary", "summary.first_unretained_at", "summary.last_unretained_at", "closure.closed_at", "closure.close_reason", "closure.known_at_close", "closure.final_sequence", "closure.final_declaration_id", "closure.final_boundary_origin", "closure.upload_expires_at"}
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET " + base, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceListOK", SuccessSchema: "BrowserSourceList", PublicErrorCodes: nativeDeliveryErrorCodes()},
		{Key: "GET " + base + "/{source_session_id}", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceStatusOK", SuccessSchema: "BrowserSourceStatus", PublicErrorCodes: nativeDeliveryErrorCodes()},

		{Key: "GET " + base + "/{source_session_id}/receipts", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserReceiptPageOK", SuccessSchema: "BrowserReceiptPage", PublicErrorCodes: nativeDeliveryErrorCodes()},
		{Key: "POST " + base + "/{source_session_id}/gaps", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/DeclareDeliveryGaps", RequestSchema: "DeclareDeliveryGaps", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserDeliveryGapResultOK", SuccessSchema: "BrowserDeliveryGapResult", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
		{Key: "POST " + base + "/{source_session_id}/seal", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/FinalDeliveryDeclaration", RequestSchema: "FinalDeliveryDeclaration", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceStatusOK", SuccessSchema: "BrowserSourceStatus", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
		{Key: "POST " + base + "/{source_session_id}/summary", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UnretainedDeliverySummary", RequestSchema: "UnretainedDeliverySummary", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserDeliverySummaryResultOK", SuccessSchema: "BrowserDeliverySummaryResult", PublicErrorCodes: nativeDeliveryDeclarationErrorCodes()},
		{Key: "POST " + base + "/{source_session_id}/events", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/BrowserActivityBatch", RequestSchema: "BrowserActivityBatch", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserActivityAcknowledgementOK", SuccessSchema: "BrowserActivityAcknowledgement", PublicErrorCodes: browserDeliveryAppendErrorCodes()},
	}, Schemas: []openAPIAgreementSchema{{Name: "BrowserSourceStatus", DTO: reflect.TypeOf(model.BrowserSourceStatus{}), Required: required, Nullable: nullable},
		{Name: "BrowserIntegrityEvidence", DTO: reflect.TypeOf(model.BrowserIntegrityEvidence{}), Required: []string{"source_session_id", "policy_revision_id", "policy_digest", "rule_id", "event"}},
		{Name: "BrowserActivityBatch", DTO: reflect.TypeOf(model.BrowserActivityBatch{}), Required: []string{"source_session_id", "participation_id", "generation", "policy_revision_id", "policy_digest", "events"}},
		{Name: "BrowserActivityAcknowledgement", DTO: reflect.TypeOf(browserDeliveryAcknowledgementResponse{}), Required: []string{"source_session_id", "receipts", "highest_contiguous_sequence", "settled_through_sequence", "highest_seen_sequence", "allocated_through_sequence", "terminal_missing_through_sequence", "missing_ranges", "missing_ranges_truncated", "server_time"}},
		{Name: "BrowserReceiptPage", DTO: reflect.TypeOf(model.BrowserReceiptPage{}), Required: []string{"receipts", "next_sequence"}, Nullable: []string{"next_sequence"}},
		{Name: "BrowserDeliveryGapResult", DTO: reflect.TypeOf(model.BrowserDeliveryGapResult{}), Required: []string{"receipt", "status"}, Nullable: prefixedBrowserNullable("status", nullable)},
		{Name: "BrowserDeliverySummaryResult", DTO: reflect.TypeOf(model.BrowserDeliverySummaryResult{}), Required: []string{"summary", "status"}, Nullable: append(prefixedBrowserNullable("status", nullable), "summary.first_unretained_at", "summary.last_unretained_at")},
		{Name: "DeclareDeliveryGaps", DTO: reflect.TypeOf(model.DeclareDeliveryGaps{}), Required: []string{"declaration_id", "expected_declaration_revision", "allocated_through_sequence", "ranges", "reason"}},
		{Name: "FinalDeliveryDeclaration", DTO: reflect.TypeOf(model.FinalDeliveryDeclaration{}), Required: []string{"declaration_id", "expected_declaration_revision", "final_sequence"}},
		{Name: "UnretainedDeliverySummary", DTO: reflect.TypeOf(model.UnretainedDeliverySummary{}), Required: []string{"summary_sequence", "unretained_record_count", "count_complete", "first_unretained_at", "last_unretained_at"}, Nullable: []string{"first_unretained_at", "last_unretained_at"}},
	}}
	runtime := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtime.collectResources(model.APIURLSuffix, browserDeliveryResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtime.Routes())
}

func prefixedBrowserNullable(prefix string, fields []string) []string {
	result := make([]string, len(fields))
	for i, field := range fields {
		result[i] = prefix + "." + field
	}
	return result
}
