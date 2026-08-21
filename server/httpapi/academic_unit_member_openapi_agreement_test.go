// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicUnitMemberOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/academic-units/{academic_unit_id}/members", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitMemberListOK", SuccessSchema: "AcademicUnitMemberListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/academic-units/{academic_unit_id}/members", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/CreateAcademicUnitMember", RequestSchema: "CreateAcademicUnitMemberRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/AcademicUnitMemberCreated", SuccessSchema: "AcademicUnitMemberResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit_member.invalid", "academic_unit_member.conflict", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/academic-unit-members/{academic_unit_member_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitMemberEnded", SuccessSchema: "AcademicUnitMemberResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit_member.conflict", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "AcademicUnitMemberResponse", DTO: reflect.TypeOf(academicUnitMemberResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "academic_unit_id", "user_id", "start_at"},
			},
			{
				Name: "CreateAcademicUnitMemberRequest", DTO: reflect.TypeOf(createAcademicUnitMemberRequest{}),
				Required: []string{"user_id"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, academicUnitMemberResource(&academicUnitMemberHTTPApplication{})); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// Relationship history remains an explicit wire-level compatibility rule:
	// clients can select an instant or request the complete historical roster.
	document := readOpenAPIDocument(t)
	var listOperation struct {
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(document.Paths["/api/v1/academic-units/{academic_unit_id}/members"]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	queryParameters := map[string]bool{}
	for _, parameter := range listOperation.Parameters {
		if parameter.In == "query" {
			queryParameters[parameter.Name] = true
		}
	}
	if !queryParameters["active_at"] || !queryParameters["history"] {
		t.Fatalf("list query parameters = %#v, want active_at and history", queryParameters)
	}

	// This v1 relationship collection remains a legacy bare array.
	list := document.Components.Schemas["AcademicUnitMemberListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/AcademicUnitMemberResponse" {
		t.Fatalf("AcademicUnitMemberListResponse = %#v", list)
	}
}
