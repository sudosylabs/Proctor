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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestBrowserDeliveryOpenAPIAgreesWithRuntime(t *testing.T) {
	base := "/api/v1/exam-attempts/{exam_attempt_id}/browser-activity/sources"
	required := []string{"highest_contiguous_sequence", "settled_through_sequence", "highest_seen_sequence", "allocated_through_sequence", "terminal_missing_through_sequence", "missing_ranges", "missing_ranges_truncated", "source_session_id", "attempt_id", "participation_id", "generation", "policy_revision_id", "policy_digest", "predecessor_source_session_id", "start_transition", "runtime_reset_reason", "started_at", "closure", "declaration_revision", "detail_mode", "budget_scope", "summary_only_reason", "remaining_correction_starts", "remaining_runtime_reset_starts", "summary", "server_time"}
	nullable := []string{"predecessor_source_session_id", "runtime_reset_reason", "budget_scope", "summary_only_reason", "summary"}
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET " + base, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceListOK", SuccessSchema: "BrowserSourceList", PublicErrorCodes: []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "GET " + base + "/{source_session_id}", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceStatusOK", SuccessSchema: "BrowserSourceStatus", PublicErrorCodes: []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}},

		{Key: "GET " + base + "/{source_session_id}/receipts", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserReceiptPageOK", SuccessSchema: "BrowserReceiptPage", PublicErrorCodes: []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "POST " + base + "/{source_session_id}/gaps", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/DeclareDeliveryGaps", RequestSchema: "DeclareDeliveryGaps", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserDeliveryGapResultOK", SuccessSchema: "BrowserDeliveryGapResult", PublicErrorCodes: []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}},
		{Key: "POST " + base + "/{source_session_id}/seal", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/FinalDeliveryDeclaration", RequestSchema: "FinalDeliveryDeclaration", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserSourceStatusOK", SuccessSchema: "BrowserSourceStatus", PublicErrorCodes: []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}},
		{Key: "POST " + base + "/{source_session_id}/summary", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/UnretainedDeliverySummary", RequestSchema: "UnretainedDeliverySummary", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserDeliverySummaryResultOK", SuccessSchema: "BrowserDeliverySummaryResult", PublicErrorCodes: []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}},
		{Key: "POST " + base + "/{source_session_id}/events", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/BrowserActivityBatch", RequestSchema: "BrowserActivityBatch", SuccessStatus: "200", SuccessRef: "#/components/responses/BrowserActivityAcknowledgementOK", SuccessSchema: "BrowserActivityAcknowledgement", PublicErrorCodes: []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid", "exam.delivery.batch_conflict", "exam.delivery.replay_window_exceeded", "exam.delivery.pending_capacity", "exam.delivery.append_rate_limited", "exam.attempt.browser_activity_conflict"}},
	}, Schemas: []openAPIAgreementSchema{{Name: "BrowserSourceStatus", DTO: reflect.TypeOf(model.BrowserSourceStatus{}), Required: required, Nullable: nullable},
		{Name: "BrowserIntegrityEvidence", DTO: reflect.TypeOf(model.BrowserIntegrityEvidence{}), Required: []string{"source_session_id", "policy_revision_id", "policy_digest", "rule_id", "event"}},
		{Name: "BrowserActivityBatch", DTO: reflect.TypeOf(model.BrowserActivityBatch{}), Required: []string{"source_session_id", "participation_id", "generation", "policy_revision_id", "policy_digest", "events"}},
		{Name: "BrowserActivityAcknowledgement", DTO: reflect.TypeOf(browserDeliveryAcknowledgementResponse{}), Required: []string{"source_session_id", "receipts", "highest_contiguous_sequence", "settled_through_sequence", "highest_seen_sequence", "allocated_through_sequence", "terminal_missing_through_sequence", "missing_ranges", "missing_ranges_truncated", "server_time"}},
		{Name: "BrowserReceiptPage", DTO: reflect.TypeOf(listResponse[model.BrowserEventReceipt]{}), Required: []string{"items"}},
		{Name: "BrowserDeliveryGapResult", DTO: reflect.TypeOf(browserDeliveryGapResponse{}), Required: []string{"receipt", "status"}, Nullable: prefixedBrowserNullable("status", nullable)},
		{Name: "BrowserDeliverySummaryResult", DTO: reflect.TypeOf(model.BrowserDeliverySummaryResult{}), Required: []string{"summary", "status"}, Nullable: prefixedBrowserNullable("status", nullable)},
		{Name: "DeclareDeliveryGaps", DTO: reflect.TypeOf(model.DeclareDeliveryGaps{}), Required: []string{"declaration_id", "expected_revision", "allocated_through_sequence", "ranges", "reason"}},
		{Name: "FinalDeliveryDeclaration", DTO: reflect.TypeOf(model.FinalDeliveryDeclaration{}), Required: []string{"declaration_id", "expected_revision", "final_sequence"}},
		{Name: "UnretainedDeliverySummary", DTO: reflect.TypeOf(model.UnretainedDeliverySummary{}), Required: []string{"summary_sequence", "unretained_record_count", "count_complete"}},
	}}
	runtime := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtime.collectResources(model.APIURLSuffix, browserDeliveryResource(nil)))
	assertOpenAPIAgreement(t, suite, runtime.Routes())
}

func prefixedBrowserNullable(prefix string, fields []string) []string {
	result := make([]string, len(fields))
	for i, field := range fields {
		result[i] = prefix + "." + field
	}
	return result
}
