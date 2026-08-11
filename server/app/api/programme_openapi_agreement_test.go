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

func TestProgrammeOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, programmeResource(nil)); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{academic_unit_id:"+canonicalIDRoutePattern()+"}", "{academic_unit_id}")
		path = strings.ReplaceAll(path, "{programme_id:"+canonicalIDRoutePattern()+"}", "{programme_id}")
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/academic-units/{academic_unit_id}/programmes": {
			successStatus: "200", successRef: "#/components/responses/ProgrammeListOK", successSchema: "ProgrammeListResponse",
			errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
		},
		"POST /api/v1/academic-units/{academic_unit_id}/programmes": {
			requestBodyRef: "#/components/requestBodies/CreateProgramme", requestSchema: "CreateProgrammeRequest",
			successStatus: "201", successRef: "#/components/responses/ProgrammeCreated", successSchema: "ProgrammeResponse",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict", "administration.unavailable"),
		},
		"GET /api/v1/programmes/{programme_id}": {
			successStatus: "200", successRef: "#/components/responses/ProgrammeOK", successSchema: "ProgrammeResponse",
			errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
		},
		"PATCH /api/v1/programmes/{programme_id}": {
			requestBodyRef: "#/components/requestBodies/UpdateProgramme", requestSchema: "UpdateProgrammeRequest",
			successStatus: "200", successRef: "#/components/responses/ProgrammeOK", successSchema: "ProgrammeResponse",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict", "administration.unavailable"),
		},
		"DELETE /api/v1/programmes/{programme_id}": {
			successStatus: "204", successRef: "#/components/responses/ProgrammeArchived",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme.conflict", "administration.unavailable"),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if !strings.Contains(path, "/programmes") {
			continue
		}
		if strings.HasSuffix(path, "/levels") {
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
			assertOpenAPISuccessResponse(t, document, key, contract)
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
				response := operation.Responses[strconv.Itoa(status)]
				if response.Ref == "" {
					t.Errorf("%s code %q has no %d response", key, code, status)
					continue
				}
				assertOpenAPIProblemResponse(t, document, key, status, response)
			}
		}
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime = %#v", documented, runtimeOperations)
	}
	assertOpenAPISchemaMatchesDTO(t, document, "ProgrammeResponse", reflect.TypeOf(programmeResponse{}), []string{"id", "create_at", "update_at", "delete_at", "academic_unit_id", "name", "display_name", "description"})
	assertOpenAPISchemaMatchesDTO(t, document, "CreateProgrammeRequest", reflect.TypeOf(createProgrammeRequest{}), []string{"name", "display_name"})
	assertOpenAPISchemaMatchesDTO(t, document, "UpdateProgrammeRequest", reflect.TypeOf(updateProgrammeRequest{}), nil)
	list := document.Components.Schemas["ProgrammeListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ProgrammeResponse" {
		t.Fatalf("ProgrammeListResponse = %#v", list)
	}
}
