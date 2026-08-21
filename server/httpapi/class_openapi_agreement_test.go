// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestClassOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	logger, _ := newTestLogger(t)
	runtimeAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: model.Principal{}},
		classResource(&classHTTPApplication{}),
	)
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-units/{academic_unit_id}/classes", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassListOK", SuccessSchema: "ClassListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "GET /api/v1/programme-levels/{programme_level_id}/classes", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassListOK", SuccessSchema: "ClassListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/programme-levels/{programme_level_id}/classes", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateClass", RequestSchema: "CreateClassRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/ClassCreated", SuccessSchema: "ClassResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/ClassOK", SuccessSchema: "ClassResponse", PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateClass", RequestSchema: "UpdateClassRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/ClassOK", SuccessSchema: "ClassResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/classes/{class_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/ClassArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "ClassResponse", DTO: reflect.TypeOf(classResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "programme_level_id", "academic_period_id", "name", "display_name", "description"}},
			{Name: "CreateClassRequest", DTO: reflect.TypeOf(createClassRequest{}), Required: []string{"academic_period_id", "name", "display_name"}},
			{Name: "UpdateClassRequest", DTO: reflect.TypeOf(updateClassRequest{})},
		},
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	var searchOperation openAPIOperation
	path := model.APIURLSuffix + "/academic-units/{academic_unit_id}/classes"
	if err := json.Unmarshal(document.Paths[path]["get"], &searchOperation); err != nil {
		t.Fatal(err)
	}
	if got := academicStructureQueryParameterNames(searchOperation.Parameters); !reflect.DeepEqual(got, []string{"limit", "q"}) {
		t.Fatalf("Class search query parameters = %v, want [limit q]", got)
	}
	list := document.Components.Schemas["ClassListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ClassResponse" {
		t.Fatalf("ClassListResponse = %#v", list)
	}
	if archived := document.Components.Responses["ClassArchived"]; archived.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("ClassArchived does not require no-store: %#v", archived)
	}
}
