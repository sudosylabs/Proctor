// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAffiliationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/users/{user_id}/affiliations", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/AffiliationListOK", SuccessSchema: "AffiliationListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/users/{user_id}/affiliations", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/CreateAffiliation", RequestSchema: "CreateAffiliationRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/AffiliationCreated", SuccessSchema: "AffiliationResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "affiliation.invalid", "affiliation.conflict", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/affiliations/{affiliation_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/AffiliationEnded", SuccessSchema: "AffiliationResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "affiliation.student_has_active_enrollment", "affiliation.conflict", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "AffiliationResponse", DTO: reflect.TypeOf(affiliationResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "user_id", "kind", "start_at"},
			},
			{Name: "CreateAffiliationRequest", DTO: reflect.TypeOf(createAffiliationRequest{}), Required: []string{"kind"}},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, affiliationResource(&affiliationHTTPApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// AffiliationListResponse is a frozen legacy bare-array compatibility shape,
	// not an ordinary DTO contract and not a pattern for new collection routes.
	document := readOpenAPIDocument(t)
	list := document.Components.Schemas["AffiliationListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/AffiliationResponse" {
		t.Fatalf("AffiliationListResponse = %#v", list)
	}
}
