// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthenticationMethodOpenAPIAgreesWithRuntime(t *testing.T) {
	listCodes := []string{"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous", "authentication.method.unavailable"}
	suite := openAPIAgreementSuite{Operations: []openAPIAgreementOperation{
		{Key: "GET /api/v1/authentication-methods", Auth: AuthSessionRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AuthenticationMethodListOK", SuccessSchema: "AuthenticationMethodResponse", PublicErrorCodes: listCodes},
		{Key: "PUT /api/v1/authentication-methods/password", Auth: AuthStrongRecentSessionRequired, RequestBodyRef: "#/components/requestBodies/EnrollPassword", RequestSchema: "EnrollPasswordRequest", SuccessStatus: "204", SuccessRef: "#/components/responses/SensitiveNoContent", PublicErrorCodes: strongRecentAuthenticationMethodCodes("request.invalid", "authentication.password.invalid", "authentication.method.disabled", "authentication.method.conflict", "authentication.method.unavailable", "audit.unavailable")},
		{Key: "DELETE /api/v1/authentication-methods/password", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/SensitiveNoContent", PublicErrorCodes: authenticationMethodRemovalCodes()},
		{Key: "POST /api/v1/authentication-methods/providers/{provider_id}/connect", Auth: AuthStrongRecentSessionRequired, RequestBodyRef: "#/components/requestBodies/BeginProviderConnection", RequestSchema: "BeginProviderConnectionRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ProviderConnectionCreated", SuccessSchema: "ProviderConnectionResponse", PublicErrorCodes: strongRecentAuthenticationMethodCodes("request.invalid", "authentication.external.provider_not_found", "authentication.external.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.internal", "audit.unavailable")},
		{Key: "DELETE /api/v1/authentication-methods/providers/{external_identity_id}", Auth: AuthStrongRecentSessionRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/SensitiveNoContent", PublicErrorCodes: authenticationMethodRemovalCodes()},
	}, Schemas: []openAPIAgreementSchema{
		{Name: "EnrollPasswordRequest", DTO: reflect.TypeOf(enrollPasswordRequest{}), Required: []string{"password"}},
		{Name: "BeginProviderConnectionRequest", DTO: reflect.TypeOf(beginProviderConnectionRequest{})},
		{Name: "AuthenticationMethodResponse", DTO: reflect.TypeOf(authenticationMethodResponse{}), Required: []string{"password", "providers"}},
		{Name: "AuthenticationProviderMethodResponse", DTO: reflect.TypeOf(authenticationProviderMethodResponse{}), Required: []string{"id", "provider_id", "display_name", "type"}},
		{Name: "ProviderConnectionResponse", DTO: reflect.TypeOf(providerConnectionResponse{}), Required: []string{"redirect_url", "expires_at"}},
	}, OperationSelector: func(_ string, path string) bool {
		return strings.HasPrefix(path, model.APIURLSuffix+"/authentication-methods")
	}}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, authenticationMethodResource(nil, browserCookies{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
	document := readOpenAPIDocument(t)
	for _, schemaName := range []string{"AuthenticationMethodResponse", "AuthenticationProviderMethodResponse"} {
		schema := document.Components.Schemas[schemaName]
		for _, forbidden := range []string{"subject", "password_hash", "token", "credential"} {
			if _, exists := schema.Properties[forbidden]; exists {
				t.Fatalf("%s exposes %q", schemaName, forbidden)
			}
		}
	}
}
