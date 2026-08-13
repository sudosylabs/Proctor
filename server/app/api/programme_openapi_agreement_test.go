// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, programmeResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-units/{academic_unit_id}/programmes", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeListOK", SuccessSchema: "ProgrammeListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/academic-units/{academic_unit_id}/programmes", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateProgramme", RequestSchema: "CreateProgrammeRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ProgrammeCreated", SuccessSchema: "ProgrammeResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/programmes/{programme_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeOK", SuccessSchema: "ProgrammeResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/programmes/{programme_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateProgramme", RequestSchema: "UpdateProgrammeRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeOK", SuccessSchema: "ProgrammeResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/programmes/{programme_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/ProgrammeArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ProgrammeResponse", DTO: reflect.TypeOf(programmeResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "academic_unit_id", "name", "display_name", "description"}},
			{Name: "CreateProgrammeRequest", DTO: reflect.TypeOf(createProgrammeRequest{}), Required: []string{"name", "display_name"}},
			{Name: "UpdateProgrammeRequest", DTO: reflect.TypeOf(updateProgrammeRequest{})},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var listOperation openAPIOperation
	path := model.APIURLSuffix + "/academic-units/{academic_unit_id}/programmes"
	if err := json.Unmarshal(document.Paths[path]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	if got := academicStructureQueryParameterNames(listOperation.Parameters); !reflect.DeepEqual(got, []string{"limit", "q"}) {
		t.Fatalf("Programme list query parameters = %v, want [limit q]", got)
	}
	list := document.Components.Schemas["ProgrammeListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ProgrammeResponse" {
		t.Fatalf("ProgrammeListResponse = %#v", list)
	}
	if archived := document.Components.Responses["ProgrammeArchived"]; archived.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("ProgrammeArchived does not require no-store: %#v", archived)
	}
}
