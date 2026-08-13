// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/roles", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/RoleListOK", SuccessSchema: "RoleListResponse",
				PublicErrorCodes: principalContractCodes("administration.unavailable"),
			},
			{
				Key: "POST /api/v1/roles", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/CreateRole", RequestSchema: "CreateRoleRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/RoleCreated", SuccessSchema: "RoleResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "role.invalid", "role.conflict", "role.permission.unknown", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/roles/{role_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/RoleOK", SuccessSchema: "RoleResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
			},
			{
				Key: "PATCH /api/v1/roles/{role_id}", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/UpdateRole", RequestSchema: "UpdateRoleRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/RoleOK", SuccessSchema: "RoleResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "role.invalid", "role.conflict", "role.built_in.protected", "role.permission.unknown", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/roles/{role_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "204", SuccessRef: "#/components/responses/RoleDeleted",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "role.built_in.protected", "role.conflict", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "RoleResponse", DTO: reflect.TypeOf(roleResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "name", "display_name", "description", "permissions", "built_in"},
			},
			{
				Name: "CreateRoleRequest", DTO: reflect.TypeOf(createRoleRequest{}),
				Required: []string{"name", "display_name", "permissions"},
			},
			{Name: "UpdateRoleRequest", DTO: reflect.TypeOf(updateRoleRequest{})},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, roleResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// Permissions are the scoped-authorization vocabulary carried by Role DTOs;
	// the ordinary schema agreements above keep those fields explicit. The v1
	// list contract remains a legacy bare array.
	document := readOpenAPIDocument(t)
	list := document.Components.Schemas["RoleListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/RoleResponse" {
		t.Fatalf("RoleListResponse = %#v", list)
	}
}
