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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestDesktopCompatibilityOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key:              "GET /api/v1/system/ping",
				Auth:             AuthPublic,
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/SystemPingOK",
				SuccessSchema:    "SystemPingResponse",
				PublicErrorCodes: []string{"request.invalid", "desktop_compatibility_policy.revision_conflict", "desktop_compatibility_policy.unavailable"},
			},
			{
				Key:              "GET /api/v1/desktop-compatibility-policy",
				Auth:             AuthPrincipalRequired,
				SuccessStatus:    "200",
				SuccessRef:       "#/components/responses/DesktopCompatibilityPolicyOK",
				SuccessSchema:    "DesktopCompatibilityPolicyResponse",
				PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "desktop_compatibility_policy.unavailable", "desktop_compatibility_policy.revision_conflict", "resource.not_found"},
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
				PublicErrorCodes: []string{"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "request.invalid", "desktop_compatibility_policy.invalid", "desktop_compatibility_policy.revision_conflict", "desktop_compatibility_policy.unavailable", "resource.not_found"},
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
				Required: []string{"expected_revision", "minimum_desktop_release", "revoked_desktop_build_ids", "administrator_message", "availability"},
				NonNullable: []string{
					"retry_at",
					"minimum_desktop_release",
					"revoked_desktop_build_ids",
					"administrator_message",
					"availability",
				},
			},
			{
				Name:     "DesktopCompatibilityPolicyResponse",
				DTO:      reflect.TypeOf(desktopCompatibilityPolicyResponse{}),
				Required: []string{"revision", "minimum_desktop_release", "revoked_desktop_build_ids", "administrator_message", "availability", "created_at", "updated_at"},
			},
		},
		OperationSelector: func(_ string, path string) bool {
			return path == "/api/v1/system/ping" || path == "/api/v1/desktop-compatibility-policy"
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	require.NoError(t, runtimeAPI.collectResources(
		model.APIURLSuffix,
		systemResource(nil, BuildInfo{}),
		desktopCompatibilityPolicyResource(nil),
	))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var operation openAPIOperation
	require.NoError(t, json.Unmarshal(document.Paths["/api/v1/system/ping"]["get"], &operation))
	parameters := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		require.Equal(t, "query", parameter.In)
		require.True(t, parameter.Required)
		parameters = append(parameters, parameter.Name)
	}
	slices.Sort(parameters)
	require.True(t, slices.Equal(parameters, []string{
		"architecture",
		"desktop_build_id",
		"desktop_release",
		"platform",
		"realtime_protocol",
	}), "ping parameters = %#v", parameters)
	require.Equal(t, "#/components/headers/NoStore", document.Components.Responses["SystemPingOK"].Headers["Cache-Control"].Ref, "SystemPingOK does not require no-store")
	require.Equal(t, "#/components/headers/PrivateNoStore", document.Components.Responses["DesktopCompatibilityPolicyOK"].Headers["Cache-Control"].Ref, "DesktopCompatibilityPolicyOK does not require private no-store")
}
