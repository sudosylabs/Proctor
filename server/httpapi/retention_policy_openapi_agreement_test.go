// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRetentionPolicyOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	replaceCodes := principalMutationContractCodes(
		"authentication.strong_required",
		"authentication.reauthentication_required",
		"idempotency.key_required",
		"idempotency.invalid_key",
		"idempotency.conflict",
		"idempotency.in_progress",
		"request.invalid",
		"retention_policy.invalid",
		"retention_policy.revision_conflict",
		"retention_policy.unavailable",
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key:              "GET /api/v1/retention-policy",
				Auth:             AuthPrincipalRequired,
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/RetentionPolicyOK",
				SuccessSchema:    "RetentionPolicyResponse",
				PublicErrorCodes: principalContractCodes("retention_policy.unavailable"),
			},
			{
				Key:              "PUT /api/v1/retention-policy",
				Auth:             AuthStrongRecentSessionRequired,
				Idempotency:      IdempotencyRequired,
				RequestBodyRef:   "#/components/requestBodies/ReplaceRetentionPolicy",
				RequestSchema:    "RetentionPolicyRequest",
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/RetentionPolicyOK",
				SuccessSchema:    "RetentionPolicyResponse",
				PublicErrorCodes: replaceCodes,
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "RetentionPolicyRequest", DTO: reflect.TypeOf(retentionPolicyRequest{}),
				Required:    []string{"expected_revision", "submission_retention_days", "integrity_retention_days", "browser_activity_retention_days", "security_operational_retention_days", "audit_retention_days", "export_retention_days", "deletion_grace_days"},
				NonNullable: []string{"expected_revision", "submission_retention_days", "integrity_retention_days", "browser_activity_retention_days", "security_operational_retention_days", "audit_retention_days", "export_retention_days", "deletion_grace_days", "candidate_notices"},
			},
			{
				Name: "RetentionPolicyResponse", DTO: reflect.TypeOf(retentionPolicyResponse{}),
				Required: []string{"revision", "submission_retention_days", "integrity_retention_days", "browser_activity_retention_days", "security_operational_retention_days", "audit_retention_days", "export_retention_days", "deletion_grace_days", "candidate_notices", "automatic_deletion_enabled", "created_at", "updated_at"},
			},
		},
		OperationSelector: func(_ string, path string) bool {
			return path == "/api/v1/retention-policy"
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(
		model.APIURLSuffix,
		retentionPolicyResource(nil),
	); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	if document.Components.Responses["RetentionPolicyOK"].Headers["Cache-Control"].Ref != "#/components/headers/PrivateNoStore" {
		t.Fatal("RetentionPolicyOK must require private no-store")
	}
	for _, name := range []string{"RetentionPolicyRequest", "RetentionPolicyResponse"} {
		for _, field := range []string{"submission_retention_days", "integrity_retention_days", "browser_activity_retention_days", "security_operational_retention_days", "audit_retention_days", "export_retention_days", "deletion_grace_days"} {
			var bounds struct {
				Minimum *int `json:"minimum"`
				Maximum *int `json:"maximum"`
			}
			if err := json.Unmarshal(document.Components.Schemas[name].Properties[field], &bounds); err != nil {
				t.Fatal(err)
			}
			maximum := 36500
			if field == "export_retention_days" {
				maximum = 7
			}
			if bounds.Minimum == nil || *bounds.Minimum != 0 || bounds.Maximum == nil || *bounds.Maximum != maximum {
				t.Fatalf("%s.%s must accept 0 through %d", name, field, maximum)
			}
		}
	}
}
