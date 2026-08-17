// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleBindingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/role-bindings", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/RoleBindingListOK", SuccessSchema: "RoleBindingListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/role-bindings", Auth: AuthStrongRecentSessionRequired,
				RequestBodyRef: "#/components/requestBodies/CreateRoleBinding", RequestSchema: "CreateRoleBindingRequest",
				SuccessStatus: "201", SuccessRef: "#/components/responses/RoleBindingCreated", SuccessSchema: "RoleBindingResponse",
				PublicErrorCodes: strongRecentSessionMutationErrorCodes(
					"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable",
					"request.invalid", "resource.not_found", "role_binding.invalid", "role_binding.conflict",
					"role_binding.system_admin_requires_institution_scope", "administration.unavailable",
				),
			},
			{
				Key: "DELETE /api/v1/role-bindings/{role_binding_id}", Auth: AuthStrongRecentSessionRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/RoleBindingEnded", SuccessSchema: "RoleBindingResponse",
				PublicErrorCodes: strongRecentSessionMutationErrorCodes(
					"authorization.denied", "authorization.request.invalid", "authorization.unavailable", "audit.unavailable",
					"request.invalid", "resource.not_found", "role_binding.conflict",
					"role_binding.last_system_admin", "administration.unavailable",
				),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "RoleBindingResponse", DTO: reflect.TypeOf(roleBindingResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "user_id", "role_id", "scope_type", "scope_id", "start_at"},
			},
			{
				Name: "CreateRoleBindingRequest", DTO: reflect.TypeOf(createRoleBindingRequest{}),
				Required: []string{"user_id", "role_id", "scope_type", "scope_id"},
			},
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, roleBindingResource(nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// The ordinary DTO agreements keep scope_type and scope_id visible as the
	// authorization scope carried by each binding. The v1 collection remains a
	// legacy bare array rather than a new collection-envelope precedent.
	document := readOpenAPIDocument(t)
	list := document.Components.Schemas["RoleBindingListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/RoleBindingResponse" {
		t.Fatalf("RoleBindingListResponse = %#v", list)
	}
}
