// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserSettingsOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, userSettingsResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/users/me/settings", Auth: AuthSessionRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserSettingsOK",
				SuccessSchema:    "UserSettingsResponse",
				PublicErrorCodes: sessionErrorCodes("user_settings.unavailable"),
			},
			{
				Key: "PUT /api/v1/users/me/settings", Auth: AuthSessionRequired,
				Idempotency:    IdempotencyRequired,
				RequestBodyRef: "#/components/requestBodies/ReplaceUserSettings",
				RequestSchema:  "UserSettingsReplaceRequest",
				SuccessStatus:  "200", SuccessRef: "#/components/responses/UserSettingsReplaced",
				SuccessSchema: "UserSettingsReplacementResponse",
				PublicErrorCodes: sessionMutationErrorCodes(
					"request.invalid", "user_settings.invalid", "user_settings.format_unsupported",
					"user_settings.revision_conflict", "user_settings.unavailable", "audit.unavailable",
					"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
				),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "UserSettingsResponse", DTO: reflect.TypeOf(userSettingsResponse{}),
				Required: []string{"source", "format_version", "revision", "writable", "updated_at"},
			},
			{
				Name: "UserSettingsReplaceRequest", DTO: reflect.TypeOf(userSettingsReplaceRequest{}),
				Required: []string{"source", "format_version", "expected_revision"},
			},
			{
				Name: "UserSettingsReplacementResponse", DTO: reflect.TypeOf(userSettingsReplacementResponse{}),
				Required: []string{"revision", "format_version", "updated_at", "changed"},
			},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	response := document.Components.Responses["UserSettingsOK"]
	if response.Headers["Cache-Control"].Ref != "#/components/headers/PrivateNoStore" {
		t.Fatalf("UserSettingsOK does not require private no-store: %#v", response)
	}
	replaced := document.Components.Responses["UserSettingsReplaced"]
	if replaced.Headers["Cache-Control"].Ref != "#/components/headers/PrivateNoStore" {
		t.Fatalf("UserSettingsReplaced does not require private no-store: %#v", replaced)
	}
}
