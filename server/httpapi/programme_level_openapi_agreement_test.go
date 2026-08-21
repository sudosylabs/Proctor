// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestProgrammeLevelOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, programmeLevelResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/programmes/{programme_id}/levels", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeLevelListOK", SuccessSchema: "ProgrammeLevelListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/programmes/{programme_id}/levels", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateProgrammeLevel", RequestSchema: "CreateProgrammeLevelRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ProgrammeLevelCreated", SuccessSchema: "ProgrammeLevelResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.invalid", "programme_level.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/programme-levels/{programme_level_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeLevelOK", SuccessSchema: "ProgrammeLevelResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/programme-levels/{programme_level_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateProgrammeLevel", RequestSchema: "UpdateProgrammeLevelRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ProgrammeLevelOK", SuccessSchema: "ProgrammeLevelResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.invalid", "programme_level.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/programme-levels/{programme_level_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/ProgrammeLevelArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ProgrammeLevelResponse", DTO: reflect.TypeOf(programmeLevelResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "programme_id", "name", "display_name", "description"}},
			{Name: "CreateProgrammeLevelRequest", DTO: reflect.TypeOf(createProgrammeLevelRequest{}), Required: []string{"name", "display_name"}},
			{Name: "UpdateProgrammeLevelRequest", DTO: reflect.TypeOf(updateProgrammeLevelRequest{})},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var listOperation openAPIOperation
	path := model.APIURLSuffix + "/programmes/{programme_id}/levels"
	if err := json.Unmarshal(document.Paths[path]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	if got := academicStructureQueryParameterNames(listOperation.Parameters); !reflect.DeepEqual(got, []string{"limit", "q"}) {
		t.Fatalf("Programme Level list query parameters = %v, want [limit q]", got)
	}
	list := document.Components.Schemas["ProgrammeLevelListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ProgrammeLevelResponse" {
		t.Fatalf("ProgrammeLevelListResponse = %#v", list)
	}
	if archived := document.Components.Responses["ProgrammeLevelArchived"]; archived.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("ProgrammeLevelArchived does not require no-store: %#v", archived)
	}
}
