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

func TestRoleOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.InitRoles(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{role_id:"+canonicalIDRoutePattern()+"}", "{role_id}")
		if !strings.HasPrefix(path, model.APIURLSuffix+"/roles") {
			continue
		}
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/roles": {
			successStatus: "200", successRef: "#/components/responses/RoleListOK",
			successSchema: "RoleListResponse",
			errorCodes:    principalContractCodes("administration.unavailable"),
		},
		"POST /api/v1/roles": {
			requestBodyRef: "#/components/requestBodies/CreateRole", requestSchema: "CreateRoleRequest",
			successStatus: "201", successRef: "#/components/responses/RoleCreated", successSchema: "RoleResponse",
			errorCodes: principalMutationContractCodes("request.invalid", "role.invalid", "role.conflict", "role.permission.unknown", "administration.unavailable"),
		},
		"GET /api/v1/roles/{role_id}": {
			successStatus: "200", successRef: "#/components/responses/RoleOK", successSchema: "RoleResponse",
			errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
		},
		"PATCH /api/v1/roles/{role_id}": {
			requestBodyRef: "#/components/requestBodies/UpdateRole", requestSchema: "UpdateRoleRequest",
			successStatus: "200", successRef: "#/components/responses/RoleOK", successSchema: "RoleResponse",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "role.invalid", "role.conflict", "role.built_in.protected", "role.permission.unknown", "administration.unavailable"),
		},
		"DELETE /api/v1/roles/{role_id}": {
			successStatus: "204", successRef: "#/components/responses/RoleDeleted",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "role.built_in.protected", "role.conflict", "administration.unavailable"),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/roles" && !strings.HasPrefix(path, model.APIURLSuffix+"/roles/") {
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
}
