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
	"testing"
)

func TestSecurityPreflightOpenAPIAgreesWithRuntime(t *testing.T) {
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET /api/v1/exam-attempts/{exam_attempt_id}/security-policy", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/SecurityPolicyRecoveryOK", SuccessSchema: "SecurityPolicyRecovery", PublicErrorCodes: securityPreflightErrorCodes()},
		{Key: "POST /api/v1/exam-sittings/{exam_sitting_id}/security-preflights", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/PrepareSecurityPreflightRequest", RequestSchema: "PrepareSecurityPreflightRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/SecurityPolicyResponseOK", SuccessSchema: "SecurityPolicyResponse", PublicErrorCodes: securityPreflightErrorCodes()},
		{Key: "POST /api/v1/security-preflights/{security_preflight_id}/report", Auth: AuthSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/SecurityPreflightReport", RequestSchema: "SecurityPreflightReport", SuccessStatus: "200", SuccessRef: "#/components/responses/SecurityPreflightResultOK", SuccessSchema: "SecurityPreflightResult", PublicErrorCodes: securityPreflightErrorCodes()},
	}, Schemas: []openAPIAgreementSchema{
		{Name: "SecurityCoverageResult", DTO: reflect.TypeOf(model.SecurityCoverageResult{}), Required: []string{"processed_control_sequence", "processed_control_digest", "coverage_result", "source_reset_receipts", "security_interaction_allowed", "execution_state", "delivery_watermark_rejections"}, Nullable: []string{"processed_control_digest"}},
		{Name: "SecurityPolicyRecovery", DTO: reflect.TypeOf(securityPolicyRecoveryResponse{}), Required: []string{"server_time", "security", "frozen_attempt_configuration", "current_sources", "current_coverage", "source_reset_receipts", "security_coverage"}, Nullable: []string{"security_coverage.processed_control_digest"}},
		{Name: "AdmittedSecurity", DTO: reflect.TypeOf(model.AdmittedSecurity{}), Required: []string{"policy", "policy_content_digest", "preflight_id", "preflight_report_digest", "preflight_policy_digest", "participation_id", "generation", "security_session_id", "delivery_stream_id", "renewal_interval_seconds"}},
		{Name: "PrepareSecurityPreflightRequest", DTO: reflect.TypeOf(prepareSecurityPreflightRequest{}), Required: []string{"native_registry_digest", "source_manifest_digest", "configuration_manifest_fingerprint"}},
		{Name: "SecurityPolicyResponse", DTO: reflect.TypeOf(securityPolicyResponse{}), Required: []string{"server_time", "policy", "policy_content_digest", "capability_matrix_digest", "catalog_bindings", "preflight_challenge", "frozen_attempt_configuration", "browser_activity_disclosure"}, Nullable: []string{"frozen_attempt_configuration"}},
		{Name: "EffectiveExamSecurityPolicy", DTO: reflect.TypeOf(model.EffectiveExamSecurityPolicy{}), Required: []string{"policy_id", "revision", "ordinal", "digest", "institution_id", "exam_revision_id", "sitting_id", "scope", "issued_at", "active_from", "application_release_id", "matrix_id", "target_tuple", "registry_digest", "focus_loss_mode", "connection_loss_mode", "resolved_exception_refs", "evidence_class_id", "visibility_class_id", "retention_class_id", "capabilities"}},
		{Name: "SecurityPreflightChallenge", DTO: reflect.TypeOf(model.SecurityPreflightChallenge{}), Required: []string{"preflight_id", "challenge", "issued_at", "expires_at"}},
		{Name: "SecurityPreflightReport", DTO: reflect.TypeOf(model.SecurityPreflightReport{}), Required: []string{"challenge", "policy_digest", "policy_content_digest", "capability_matrix_digest", "security_session_id", "source_manifest_digest", "selected_source_categories", "sources", "coverage", "baseline", "posture", "reported_at"}},
		{Name: "SecurityPreflightResult", DTO: reflect.TypeOf(model.SecurityPreflightResult{}), Required: []string{"preflight_id", "report_digest", "admission", "reason_codes", "server_time", "expires_at"}},
		{Name: "SecurityCatalogBinding", DTO: reflect.TypeOf(model.SecurityCatalogBinding{}), Required: []string{"kind", "catalog_id", "revision", "digest"}},
		{Name: "BrowserActivityDisclosure", DTO: reflect.TypeOf(model.BrowserActivityDisclosure{}), Required: []string{"notice_id", "audience", "retention_policy_revision", "browser_activity_retention_days", "retention_anchor", "may_create_integrity_evidence"}},
		{Name: "FrozenAttemptConfiguration", DTO: reflect.TypeOf(model.AttemptConfiguration{}), Required: []string{"manifest_fingerprint", "registry_fingerprint", "desktop_build", "desktop_target", "user_settings_revision", "presentation", "approved_commands", "approved_keybindings", "attempt_configuration_revision", "digest"}},
	}}
	runtime := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtime.collectResources(model.APIURLSuffix, securityPreflightResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtime.Routes())
}
