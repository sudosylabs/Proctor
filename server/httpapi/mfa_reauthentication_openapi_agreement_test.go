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

func TestMFAReauthenticationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "POST /api/v1/auth/reauthenticate/password", Auth: AuthMFARecoverySessionRequired,
				RequestBodyRef: "#/components/requestBodies/PasswordReauthentication", RequestSchema: "PasswordReauthenticationRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/PasswordReauthenticationOK", SuccessSchema: "SessionResponse",
				PublicErrorCodes: sessionMutationErrorCodes("service.busy", "request.invalid", "authentication.invalid_credentials",
					"authentication.reauthentication_method_required", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.internal", "audit.unavailable")},
			{Key: "POST /api/v1/auth/reauthenticate/external", Auth: AuthMFARecoverySessionRequired,
				RequestBodyRef: "#/components/requestBodies/ExternalReauthentication", RequestSchema: "ExternalReauthenticationRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/ExternalReauthenticationStarted", SuccessSchema: "ExternalReauthenticationResponse",
				PublicErrorCodes: sessionMutationErrorCodes("request.invalid", "authentication.reauthentication_method_required",
					"authentication.external.request.invalid", "authentication.external.provider_not_found", "authentication.rate_limited",
					"authentication.rate_limit_unavailable", "authentication.external.unavailable", "authentication.external.rejected", "authentication.internal", "audit.unavailable")},
			{Key: "POST /api/v1/users/{user_id}/mfa/reset", Auth: AuthStrongRecentSessionRequired,
				RequestBodyRef: "#/components/requestBodies/ResetUserMFA", RequestSchema: "ResetUserMFARequest",
				SuccessStatus: "204", SuccessRef: "#/components/responses/SensitiveNoContent",
				PublicErrorCodes: principalMutationContractCodes("authentication.strong_required", "authentication.reauthentication_required",
					"request.invalid", "resource.not_found", "authentication.mfa.reset_invalid", "authentication.mfa.disabled", "authentication.mfa.not_found",
					"authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "PasswordReauthenticationRequest", DTO: reflect.TypeOf(passwordReauthenticationRequest{}), Required: []string{"password"}},
			{Name: "ExternalReauthenticationRequest", DTO: reflect.TypeOf(externalReauthenticationRequest{}), Required: []string{"task"}},
			{Name: "ExternalReauthenticationResponse", DTO: reflect.TypeOf(externalReauthenticationResponse{}), Required: []string{"redirect_url"}},
			{Name: "ResetUserMFARequest", DTO: reflect.TypeOf(resetUserMFARequest{}), Required: []string{"identity_verified", "reason", "verification_reference"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, sessionReauthenticationResource(nil, browserCookies{}), mfaAdministrationResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}
