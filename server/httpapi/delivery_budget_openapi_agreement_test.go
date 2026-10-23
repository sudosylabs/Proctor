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

func TestDeliveryBudgetOpenAPIAgreesWithRuntime(t *testing.T) {
	base := "/api/v1/exam-attempts/{exam_attempt_id}/delivery-limits"
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET " + base, Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/DeliveryBudgetOK", SuccessSchema: "DeliveryBudgetSnapshot", PublicErrorCodes: []string{"exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress"}},
		{Key: "POST " + base + "/stop-details", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/StopDeliveryDetails", RequestSchema: "StopDeliveryDetails", SuccessStatus: "200", SuccessRef: "#/components/responses/StopDeliveryDetailsOK", SuccessSchema: "StopDeliveryDetailsResult", PublicErrorCodes: []string{"audit.unavailable", "exam.delivery.control_rate_limited", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.registered_desktop_required", "exam.attempt.connection_closed", "exam.attempt.connection_lost", "exam.attempt.sitting_not_open", "exam.attempt.unavailable", "exam.delivery.upload_expired", "exam.delivery.declaration_conflict", "exam.delivery.sequence_limit", "exam.delivery.detail_budget_exhausted", "exam.delivery.metadata_capacity", "exam.delivery.summary_rate_limited", "idempotency.conflict", "idempotency.in_progress", "idempotency.key_required", "idempotency.key_invalid"}},
	}, Schemas: []openAPIAgreementSchema{
		{Name: "StopDeliveryDetailsResult", DTO: reflect.TypeOf(model.StopDeliveryDetailsResult{}), Required: []string{"budget", "native", "browser"}, Nullable: stopDetailsNullablePaths()},
		{Name: "DeliveryQuotaUsage", DTO: reflect.TypeOf(model.DeliveryQuotaUsage{}), Required: []string{"retained_records", "retained_bytes", "allocated_positions", "record_limit", "byte_limit", "position_limit", "summary_only", "stop_reason"}, Nullable: []string{"stop_reason"}},
		{Name: "DeliveryFamilyBudget", DTO: reflect.TypeOf(model.DeliveryFamilyBudget{}), Required: []string{"participation", "attempt", "pending_bytes", "pending_byte_limit"}, Nullable: []string{"participation.stop_reason", "attempt.stop_reason"}},
		{Name: "DeliveryBudgetSnapshot", DTO: reflect.TypeOf(model.DeliveryBudgetSnapshot{}), Required: []string{"participation_id", "generation", "native", "browser", "browser_flag_groups", "browser_evidence_records", "browser_evidence_bytes", "explicit_missing_intervals", "control_metadata_bytes", "server_time"}, Nullable: []string{"native.participation.stop_reason", "native.attempt.stop_reason", "browser.participation.stop_reason", "browser.attempt.stop_reason"}},
		{Name: "StopDeliveryDetails", DTO: reflect.TypeOf(model.StopDeliveryDetails{}), Required: []string{"family", "reason"}},
	}}
	runtime := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtime.collectResources(model.APIURLSuffix, deliveryBudgetResource(nil)))
	assertOpenAPIAgreement(t, suite, runtime.Routes())
}

func deliveryStatusNullablePaths(prefix string, browser bool) []string {
	fields := []string{"budget_scope", "summary_only_reason", "summary"}
	if browser {
		fields = append(fields, "predecessor_source_session_id", "runtime_reset_reason")
	}
	for i := range fields {
		fields[i] = prefix + fields[i]
	}
	return fields
}
func deliveryBudgetNullablePaths(prefix string) []string {
	return []string{prefix + "native.participation.stop_reason", prefix + "native.attempt.stop_reason", prefix + "browser.participation.stop_reason", prefix + "browser.attempt.stop_reason"}
}
func deliveryRecoveryNullablePaths() []string {
	fields := deliveryBudgetNullablePaths("delivery.budget.")
	fields = append(fields, deliveryStatusNullablePaths("delivery.native_status.", false)...)
	return append(fields, deliveryStatusNullablePaths("delivery.browser_status.", true)...)
}
func stopDetailsNullablePaths() []string {
	fields := append([]string{"native"}, deliveryBudgetNullablePaths("budget.")...)
	fields = append(fields, deliveryStatusNullablePaths("native.", false)...)
	return append(fields, deliveryStatusNullablePaths("browser.", true)...)
}
