// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicPeriodOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, academicPeriodResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-periods", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodListOK", SuccessSchema: "AcademicPeriodListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/academic-periods", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateAcademicPeriod", RequestSchema: "CreateAcademicPeriodRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/AcademicPeriodCreated", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.invalid", "academic_period.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodOK", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateAcademicPeriod", RequestSchema: "UpdateAcademicPeriodRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicPeriodOK", SuccessSchema: "AcademicPeriodResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.invalid", "academic_period.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/academic-periods/{academic_period_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/AcademicPeriodArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_period.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "AcademicPeriodResponse", DTO: reflect.TypeOf(academicPeriodResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "institution_id", "name", "display_name", "description", "start_at", "end_at"}},
			{Name: "CreateAcademicPeriodRequest", DTO: reflect.TypeOf(createAcademicPeriodRequest{}), Required: []string{"name", "display_name", "start_at", "end_at"}},
			{Name: "UpdateAcademicPeriodRequest", DTO: reflect.TypeOf(updateAcademicPeriodRequest{})},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var listOperation openAPIOperation
	if err := json.Unmarshal(document.Paths[model.APIURLSuffix+"/academic-periods"]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	if got := academicStructureQueryParameterNames(listOperation.Parameters); !reflect.DeepEqual(got, []string{"limit", "q"}) {
		t.Fatalf("Academic Period list query parameters = %v, want [limit q]", got)
	}
	list := document.Components.Schemas["AcademicPeriodListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/AcademicPeriodResponse" {
		t.Fatalf("AcademicPeriodListResponse = %#v", list)
	}
	if archived := document.Components.Responses["AcademicPeriodArchived"]; archived.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("AcademicPeriodArchived does not require no-store: %#v", archived)
	}
}

func academicStructureQueryParameterNames(parameters []openAPIParameter) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter.In == "query" {
			names = append(names, parameter.Name)
		}
	}
	sort.Strings(names)
	return names
}
