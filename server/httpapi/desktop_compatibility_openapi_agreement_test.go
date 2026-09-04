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
	"slices"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestDesktopCompatibilityOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	replaceCodes := principalMutationContractCodes(
		"authentication.strong_required",
		"authentication.reauthentication_required",
		"idempotency.key_required",
		"idempotency.invalid_key",
		"idempotency.conflict",
		"idempotency.in_progress",
		"request.invalid",
		"desktop_compatibility_policy.invalid",
		"desktop_compatibility_policy.revision_conflict",
		"desktop_compatibility_policy.unavailable",
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key:              "GET /api/v1/system/ping",
				Auth:             AuthPublic,
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/SystemPingOK",
				SuccessSchema:    "SystemPingResponse",
				PublicErrorCodes: []string{"request.invalid"},
			},
			{
				Key:              "GET /api/v1/desktop-compatibility-policy",
				Auth:             AuthPrincipalRequired,
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/DesktopCompatibilityPolicyOK",
				SuccessSchema:    "DesktopCompatibilityPolicyResponse",
				PublicErrorCodes: principalContractCodes("desktop_compatibility_policy.unavailable"),
			},
			{
				Key:              "PUT /api/v1/desktop-compatibility-policy",
				Auth:             AuthStrongRecentSessionRequired,
				Idempotency:      IdempotencyRequired,
				RequestBodyRef:   "#/components/requestBodies/ReplaceDesktopCompatibilityPolicy",
				RequestSchema:    "DesktopCompatibilityPolicyRequest",
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/DesktopCompatibilityPolicyOK",
				SuccessSchema:    "DesktopCompatibilityPolicyResponse",
				PublicErrorCodes: replaceCodes,
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name:     "SystemPingResponse",
				DTO:      reflect.TypeOf(systemPingResponse{}),
				Required: []string{"schema_version", "server_time", "availability", "reason", "compatibility", "compatibility_reason"},
			},
			{
				Name:     "DesktopCompatibilityPolicyRequest",
				DTO:      reflect.TypeOf(desktopCompatibilityPolicyRequest{}),
				Required: []string{"expected_revision", "minimum_desktop_release", "revoked_desktop_build_ids", "administrator_message", "availability", "retry_at"},
				NonNullable: []string{
					"minimum_desktop_release",
					"revoked_desktop_build_ids",
					"administrator_message",
					"availability",
				},
			},
			{
				Name:     "DesktopCompatibilityPolicyResponse",
				DTO:      reflect.TypeOf(desktopCompatibilityPolicyResponse{}),
				Required: []string{"revision", "minimum_desktop_release", "revoked_desktop_build_ids", "administrator_message", "availability", "retry_at", "created_at", "updated_at"},
			},
		},
		OperationSelector: func(_ string, path string) bool {
			return path == "/api/v1/system/ping" || path == "/api/v1/desktop-compatibility-policy"
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(
		model.APIURLSuffix,
		systemResource(nil, BuildInfo{}),
		desktopCompatibilityPolicyResource(nil),
	); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	if err := json.Unmarshal(document.Paths["/api/v1/system/ping"]["get"], &operation); err != nil {
		t.Fatal(err)
	}
	parameters := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		if parameter.In != "query" || !parameter.Required {
			t.Fatalf("ping parameter = %#v", parameter)
		}
		parameters = append(parameters, parameter.Name)
	}
	slices.Sort(parameters)
	if !slices.Equal(parameters, []string{
		"architecture",
		"desktop_build_id",
		"desktop_release",
		"platform",
		"realtime_protocol",
	}) {
		t.Fatalf("ping parameters = %#v", parameters)
	}
	if document.Components.Responses["SystemPingOK"].Headers["Cache-Control"].Ref !=
		"#/components/headers/NoStore" {
		t.Fatal("SystemPingOK does not require no-store")
	}
	if document.Components.Responses["DesktopCompatibilityPolicyOK"].Headers["Cache-Control"].Ref !=
		"#/components/headers/PrivateNoStore" {
		t.Fatal("DesktopCompatibilityPolicyOK does not require private no-store")
	}
}
