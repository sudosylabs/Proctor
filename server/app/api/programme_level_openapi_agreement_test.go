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

func TestProgrammeLevelOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerProgrammeLevelRoutes(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{programme_id:"+canonicalIDRoutePattern()+"}", "{programme_id}")
		path = strings.ReplaceAll(path, "{programme_level_id:"+canonicalIDRoutePattern()+"}", "{programme_level_id}")
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/programmes/{programme_id}/levels":         {successStatus: "200", successRef: "#/components/responses/ProgrammeLevelListOK", successSchema: "ProgrammeLevelListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"POST /api/v1/programmes/{programme_id}/levels":        {requestBodyRef: "#/components/requestBodies/CreateProgrammeLevel", requestSchema: "CreateProgrammeLevelRequest", successStatus: "201", successRef: "#/components/responses/ProgrammeLevelCreated", successSchema: "ProgrammeLevelResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.invalid", "programme_level.conflict", "administration.unavailable")},
		"GET /api/v1/programme-levels/{programme_level_id}":    {successStatus: "200", successRef: "#/components/responses/ProgrammeLevelOK", successSchema: "ProgrammeLevelResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"PATCH /api/v1/programme-levels/{programme_level_id}":  {requestBodyRef: "#/components/requestBodies/UpdateProgrammeLevel", requestSchema: "UpdateProgrammeLevelRequest", successStatus: "200", successRef: "#/components/responses/ProgrammeLevelOK", successSchema: "ProgrammeLevelResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.invalid", "programme_level.conflict", "administration.unavailable")},
		"DELETE /api/v1/programme-levels/{programme_level_id}": {successStatus: "204", successRef: "#/components/responses/ProgrammeLevelArchived", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "programme_level.conflict", "administration.unavailable")},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if !strings.HasPrefix(path, model.APIURLSuffix+"/programme-levels") && !strings.HasSuffix(path, "/levels") {
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
	assertOpenAPISchemaMatchesDTO(t, document, "ProgrammeLevelResponse", reflect.TypeOf(programmeLevelResponse{}), []string{"id", "create_at", "update_at", "delete_at", "programme_id", "name", "display_name", "description"})
	assertOpenAPISchemaMatchesDTO(t, document, "CreateProgrammeLevelRequest", reflect.TypeOf(createProgrammeLevelRequest{}), []string{"name", "display_name"})
	assertOpenAPISchemaMatchesDTO(t, document, "UpdateProgrammeLevelRequest", reflect.TypeOf(updateProgrammeLevelRequest{}), nil)
	list := document.Components.Schemas["ProgrammeLevelListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ProgrammeLevelResponse" {
		t.Fatalf("ProgrammeLevelListResponse = %#v", list)
	}
}
