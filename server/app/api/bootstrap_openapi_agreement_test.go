// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestBootstrapOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/bootstrap", Auth: AuthPublic,
				SuccessStatus: "200", SuccessRef: "#/components/responses/InstallationStatusOK", SuccessSchema: "InstallationStatusResponse",
				PublicErrorCodes: []string{"installation.unavailable"},
			},
			{
				Key: "POST /api/v1/bootstrap", Auth: AuthPublic,
				RequestBodyRef: "#/components/requestBodies/BootstrapInstallation", RequestSchema: "BootstrapInstallationRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/InstallationBootstrapCreated", SuccessSchema: "InstallationBootstrapResult",
				PublicErrorCodes: []string{
					"request.invalid", "installation.already_initialized", "installation.unavailable",
					"authentication.password.invalid", "authentication.rate_limited",
				},
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "InstallationStatusResponse", DTO: reflect.TypeOf(installationStatusResponse{}),
				Required: []string{"initialized"},
			},
			{
				Name: "BootstrapInstallationRequest", DTO: reflect.TypeOf(bootstrapRequest{}),
				Required: []string{"institution", "administrator", "password"},
			},
			{
				Name: "InstallationStateResponse", DTO: reflect.TypeOf(installationStateResponse{}),
				Required: []string{"initialized_at", "institution_id", "administrator_user_id"},
			},
			{Name: "InstallationBootstrapResult", DTO: reflect.TypeOf(installationBootstrapResponse{})},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, bootstrapResource(nil)); err != nil {
		t.Fatal(err)
	}

	// AuthPublic requires an explicitly empty OpenAPI security array; the
	// shared evaluator rejects an omitted, null, or credentialed requirement.
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
