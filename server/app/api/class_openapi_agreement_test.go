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

func TestClassOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	logger, _ := newTestLogger(t)
	runtimeAPI := newFocusedResourceAPI(
		t,
		logger,
		classRouteAuthenticator{principal: model.Principal{}},
		classResource(&classHTTPApplication{}),
	)
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{academic_unit_id:"+canonicalIDRoutePattern()+"}", "{academic_unit_id}")
		path = strings.ReplaceAll(path, "{programme_level_id:"+canonicalIDRoutePattern()+"}", "{programme_level_id}")
		path = strings.ReplaceAll(path, "{class_id:"+canonicalIDRoutePattern()+"}", "{class_id}")
		key := route.Method + " " + path
		runtimeOperations[key] = route.Auth
		contract, exists := expectedClassOperationContracts()[key]
		if !exists {
			t.Fatalf("unexpected runtime operation %s", key)
		}
		got, want := append([]string(nil), route.ErrorCodes...), append([]string(nil), contract.errorCodes...)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s runtime error codes = %v, want %v", key, got, want)
		}
	}
	expected := expectedClassOperationContracts()

	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if !strings.HasSuffix(path, "/classes") && !strings.HasPrefix(path, model.APIURLSuffix+"/classes/") {
			continue
		}
		if strings.HasSuffix(path, "/members") {
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
	fields := []string{"id", "create_at", "update_at", "delete_at", "programme_level_id", "academic_period_id", "name", "display_name", "description"}
	assertOpenAPISchemaMatchesDTO(t, document, "ClassResponse", reflect.TypeOf(classResponse{}), fields)
	assertOpenAPISchemaMatchesDTO(t, document, "CreateClassRequest", reflect.TypeOf(createClassRequest{}), []string{"academic_period_id", "name", "display_name"})
	assertOpenAPISchemaMatchesDTO(t, document, "UpdateClassRequest", reflect.TypeOf(updateClassRequest{}), nil)
	list := document.Components.Schemas["ClassListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ClassResponse" {
		t.Fatalf("ClassListResponse = %#v", list)
	}
}

func expectedClassOperationContracts() map[string]openAPIOperationContract {
	return map[string]openAPIOperationContract{
		"GET /api/v1/academic-units/{academic_unit_id}/classes":      {successStatus: "200", successRef: "#/components/responses/ClassListOK", successSchema: "ClassListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"GET /api/v1/programme-levels/{programme_level_id}/classes":  {successStatus: "200", successRef: "#/components/responses/ClassListOK", successSchema: "ClassListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"POST /api/v1/programme-levels/{programme_level_id}/classes": {requestBodyRef: "#/components/requestBodies/CreateClass", requestSchema: "CreateClassRequest", successStatus: "201", successRef: "#/components/responses/ClassCreated", successSchema: "ClassResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
		"GET /api/v1/classes/{class_id}":                             {successStatus: "200", successRef: "#/components/responses/ClassOK", successSchema: "ClassResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"PATCH /api/v1/classes/{class_id}":                           {requestBodyRef: "#/components/requestBodies/UpdateClass", requestSchema: "UpdateClassRequest", successStatus: "200", successRef: "#/components/responses/ClassOK", successSchema: "ClassResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.invalid", "class.conflict", "administration.unavailable")},
		"DELETE /api/v1/classes/{class_id}":                          {successStatus: "204", successRef: "#/components/responses/ClassArchived", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.conflict", "administration.unavailable")},
	}
}
