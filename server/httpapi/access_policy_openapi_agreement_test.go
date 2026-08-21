// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAccessPolicyOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	preflightCodes := principalContractCodes(
		"authentication.csrf.invalid", "authentication.strong_required", "authentication.reauthentication_required",
		"request.invalid", "access_policy.invalid", "access_policy.revision_conflict", "access_policy.unavailable",
	)
	replaceCodes := principalMutationContractCodes(
		"authentication.strong_required", "authentication.reauthentication_required",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress",
		"request.invalid", "access_policy.invalid", "access_policy.revision_conflict", "access_policy.blocked", "access_policy.unavailable",
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/discovery", Auth: AuthPublic, SuccessStatus: "200", SuccessRef: "#/components/responses/PublicAccessDiscoveryOK", SuccessSchema: "PublicAccessDiscoveryResponse", PublicErrorCodes: []string{"access_policy.unavailable"}},
			{Key: "GET /api/v1/access-policy", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AccessPolicyOK", SuccessSchema: "AccessPolicyResponse", PublicErrorCodes: principalContractCodes("access_policy.unavailable")},
			{Key: "POST /api/v1/access-policy/preflight", Auth: AuthStrongRecentSessionRequired, RequestBodyRef: "#/components/requestBodies/PreflightAccessPolicy", RequestSchema: "AccessPolicySettingsRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/AccessPolicyPreflightOK", SuccessSchema: "AccessPolicyPreflightResponse", PublicErrorCodes: preflightCodes},
			{Key: "PUT /api/v1/access-policy", Auth: AuthStrongRecentSessionRequired, Idempotency: IdempotencyRequired, RequestBodyRef: "#/components/requestBodies/ReplaceAccessPolicy", RequestSchema: "AccessPolicySettingsRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/AccessPolicyOK", SuccessSchema: "AccessPolicyResponse", PublicErrorCodes: replaceCodes},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "AccessPolicySettingsRequest", DTO: reflect.TypeOf(accessPolicySettingsRequest{}), Required: []string{"expected_revision", "revoke_existing_sessions", "local_login_enabled", "public_registration_enabled", "invitation_admission_enabled", "invitation_local_credential_enabled", "desktop_authorization_enabled", "provider_admissions"}, NonNullable: []string{"revoke_existing_sessions", "local_login_enabled", "public_registration_enabled", "invitation_admission_enabled", "invitation_local_credential_enabled", "desktop_authorization_enabled", "provider_admissions"}},
			{Name: "AccessPolicyResponse", DTO: reflect.TypeOf(accessPolicyResponse{}), Required: []string{"id", "revision", "created_at", "updated_at", "local_login_enabled", "public_registration_enabled", "invitation_admission_enabled", "invitation_local_credential_enabled", "desktop_authorization_enabled", "provider_admissions", "history", "available_providers", "durable_mail"}},
			{Name: "AccessPolicyTransitionResponse", DTO: reflect.TypeOf(accessPolicyTransitionResponse{}), Required: []string{"from_revision", "to_revision", "actor_user_id", "changed_fields", "changed_at", "outcome"}},
			{Name: "AccessPolicyBlockerResponse", DTO: reflect.TypeOf(accessPolicyBlockerResponse{}), Required: []string{"code"}},
			{Name: "AccessPolicyPreflightResponse", DTO: reflect.TypeOf(accessPolicyPreflightResponse{}), Required: []string{"blockers"}},
			{Name: "PublicInstitutionPresentationResponse", DTO: reflect.TypeOf(publicInstitutionPresentationResponse{}), Required: []string{"id", "name", "display_name"}},
			{Name: "PublicAccessCapabilitiesResponse", DTO: reflect.TypeOf(publicAccessCapabilitiesResponse{}), Required: []string{"local_login", "public_registration", "invitation_admission", "desktop_authorization"}},
			{Name: "DesktopAuthorizationCompatibilityResponse", DTO: reflect.TypeOf(desktopAuthorizationCompatibilityResponse{}), Required: []string{"protocol", "minimum_version", "maximum_version"}},
			{Name: "PublicAccessDiscoveryResponse", DTO: reflect.TypeOf(publicAccessDiscoveryResponse{}), Required: []string{"discovery_version", "canonical_origin", "initialized", "capabilities", "providers", "desktop_authorization"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, accessPolicyResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	providerAdmissions := document.Components.Schemas["AccessPolicySettingsRequest"].Properties["provider_admissions"]
	var providerGrammar struct {
		MaxProperties int `json:"maxProperties"`
		PropertyNames struct {
			Pattern string `json:"pattern"`
		} `json:"propertyNames"`
	}
	if json.Unmarshal(providerAdmissions, &providerGrammar) != nil ||
		providerGrammar.MaxProperties != model.AccessPolicyProviderMaxCount ||
		providerGrammar.PropertyNames.Pattern != `^[a-z0-9][a-z0-9._-]{0,63}$` {
		t.Fatalf("AccessPolicy provider ID grammar = %s", providerAdmissions)
	}
	providerAdmissionsResponse := document.Components.Schemas["AccessPolicyResponse"].Properties["provider_admissions"]
	providerGrammar = struct {
		MaxProperties int `json:"maxProperties"`
		PropertyNames struct {
			Pattern string `json:"pattern"`
		} `json:"propertyNames"`
	}{}
	if json.Unmarshal(providerAdmissionsResponse, &providerGrammar) != nil ||
		providerGrammar.MaxProperties != model.AccessPolicyProviderMaxCount {
		t.Fatalf("AccessPolicy response provider bound = %s", providerAdmissionsResponse)
	}
	var providerProjection struct {
		MaxItems int `json:"maxItems"`
	}
	availableProviders := document.Components.Schemas["AccessPolicyResponse"].Properties["available_providers"]
	if json.Unmarshal(availableProviders, &providerProjection) != nil ||
		providerProjection.MaxItems != model.AccessPolicyProviderMaxCount {
		t.Fatalf("AccessPolicy provider projection bound = %s", availableProviders)
	}
	for _, responseName := range []string{"AccessPolicyOK", "AccessPolicyPreflightOK", "PublicAccessDiscoveryOK"} {
		if document.Components.Responses[responseName].Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
			t.Errorf("%s does not require no-store", responseName)
		}
	}
	for _, forbidden := range []string{"provider_admissions", "durable_mail", "invitation_local_credential_enabled", "client_secret", "redirect_uri", "claims", "recipient"} {
		if _, exists := document.Components.Schemas["PublicAccessDiscoveryResponse"].Properties[forbidden]; exists {
			t.Errorf("PublicAccessDiscoveryResponse exposes forbidden field %q", forbidden)
		}
	}
}
