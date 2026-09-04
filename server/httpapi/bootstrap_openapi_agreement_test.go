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
					"installation.bootstrap_denied",
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
				Required: []string{"institution", "administrator", "password", "bootstrap_secret"},
			},
			{
				Name: "InstallationStateResponse", DTO: reflect.TypeOf(installationStateResponse{}),
				Required: []string{"initialized_at", "institution_id", "administrator_user_id"},
			},
			{Name: "InstallationBootstrapResult", DTO: reflect.TypeOf(installationBootstrapResponse{})},
			{
				Name: "InitialAccessPolicyResponse", DTO: reflect.TypeOf(initialAccessPolicyResponse{}),
				Required: []string{
					"id", "revision", "local_login_enabled", "public_registration_enabled",
					"invitation_admission_enabled", "invitation_local_credential_enabled",
					"desktop_authorization_enabled",
				},
			},
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
