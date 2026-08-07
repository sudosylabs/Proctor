// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRoleBindingOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.InitRoleBindings(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{role_binding_id:"+canonicalIDRoutePattern()+"}", "{role_binding_id}")
		if !strings.Contains(path, "/role-bindings") {
			continue
		}
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/role-bindings": {
			successStatus: "200", successRef: "#/components/responses/RoleBindingListOK",
			successSchema: "RoleBindingListResponse",
			errorCodes:    principalContractCodes("request.invalid", "administration.unavailable"),
		},
		"POST /api/v1/role-bindings": {
			requestBodyRef: "#/components/requestBodies/CreateRoleBinding", requestSchema: "CreateRoleBindingRequest",
			successStatus: "201", successRef: "#/components/responses/RoleBindingCreated", successSchema: "RoleBindingResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "role_binding.invalid", "role_binding.conflict",
				"role_binding.system_admin_requires_institution_scope", "administration.unavailable",
			),
		},
		"DELETE /api/v1/role-bindings/{role_binding_id}": {
			successStatus: "200", successRef: "#/components/responses/RoleBindingEnded", successSchema: "RoleBindingResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "role_binding.conflict",
				"role_binding.last_system_admin", "administration.unavailable",
			),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/role-bindings" && !strings.HasPrefix(path, model.APIURLSuffix+"/role-bindings/") {
			continue
		}
		for method, raw := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			key := upper + " " + path
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatal(err)
			}
			documented[key] = operation.Auth
			contract, exists := expected[key]
			if !exists {
				t.Fatalf("unexpected operation %s", key)
			}
			assertPrincipalSecurity(t, key, upper, operation.Security)
			if operation.RequestBody.Ref != contract.requestBodyRef || operation.Responses[contract.successStatus].Ref != contract.successRef {
				t.Errorf("%s request/success refs do not agree", key)
			}
			assertOpenAPIRequestBody(t, document, key, contract)
			if contract.successSchema != "" {
				assertOpenAPISuccessResponse(t, document, key, contract)
			}
			got, want := append([]string(nil), operation.ErrorCodes...), append([]string(nil), contract.errorCodes...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s error codes = %v, want %v", key, got, want)
			}
			for _, code := range operation.ErrorCodes {
				status, exists := statuses[code]
				if !exists {
					t.Errorf("%s unmapped code %q", key, code)
					continue
				}
				assertOpenAPIProblemResponse(t, document, key, status, operation.Responses[strconv.Itoa(status)])
			}
		}
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("documented=%v runtime=%v", documented, runtimeOperations)
	}
	assertOpenAPISchemaMatchesDTO(
		t, document, "RoleBindingResponse", reflect.TypeOf(roleBindingResponse{}),
		[]string{"id", "create_at", "update_at", "delete_at", "user_id", "role_id", "scope_type", "scope_id", "start_at"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "CreateRoleBindingRequest", reflect.TypeOf(createRoleBindingRequest{}),
		[]string{"user_id", "role_id", "scope_type", "scope_id"},
	)
	list := document.Components.Schemas["RoleBindingListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/RoleBindingResponse" {
		t.Fatalf("RoleBindingListResponse = %#v", list)
	}
}
