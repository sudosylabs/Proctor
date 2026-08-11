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

func TestAcademicUnitMemberOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/academic-units/{academic_unit_id}/members":          {successStatus: "200", successRef: "#/components/responses/AcademicUnitMemberListOK", successSchema: "AcademicUnitMemberListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"POST /api/v1/academic-units/{academic_unit_id}/members":         {requestBodyRef: "#/components/requestBodies/CreateAcademicUnitMember", requestSchema: "CreateAcademicUnitMemberRequest", successStatus: "201", successRef: "#/components/responses/AcademicUnitMemberCreated", successSchema: "AcademicUnitMemberResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit_member.invalid", "academic_unit_member.conflict", "administration.unavailable")},
		"DELETE /api/v1/academic-unit-members/{academic_unit_member_id}": {successStatus: "200", successRef: "#/components/responses/AcademicUnitMemberEnded", successSchema: "AcademicUnitMemberResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit_member.conflict", "administration.unavailable")},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, academicUnitMemberResource(&academicUnitMemberHTTPApplication{})); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{academic_unit_id:"+canonicalIDRoutePattern()+"}", "{academic_unit_id}")
		path = strings.ReplaceAll(path, "{academic_unit_member_id:"+canonicalIDRoutePattern()+"}", "{academic_unit_member_id}")
		key := route.Method + " " + path
		runtimeOperations[key] = route.Auth
		contract, exists := expected[key]
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
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/academic-units/{academic_unit_id}/members" && !strings.HasPrefix(path, model.APIURLSuffix+"/academic-unit-members/") {
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
			contract, ok := expected[key]
			if !ok {
				t.Errorf("unexpected documented Academic Unit Member operation %s", key)
				continue
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
				status := statuses[code]
				assertOpenAPIProblemResponse(t, document, key, status, operation.Responses[strconv.Itoa(status)])
			}
		}
	}
	var listOperation struct {
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(document.Paths["/api/v1/academic-units/{academic_unit_id}/members"]["get"], &listOperation); err != nil {
		t.Fatal(err)
	}
	queryParameters := map[string]bool{}
	for _, parameter := range listOperation.Parameters {
		if parameter.In == "query" {
			queryParameters[parameter.Name] = true
		}
	}
	if !queryParameters["active_at"] || !queryParameters["history"] {
		t.Fatalf("list query parameters = %#v, want active_at and history", queryParameters)
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime = %#v", documented, runtimeOperations)
	}
	required := []string{"id", "create_at", "update_at", "delete_at", "academic_unit_id", "user_id", "start_at"}
	assertOpenAPISchemaMatchesDTO(t, document, "AcademicUnitMemberResponse", reflect.TypeOf(academicUnitMemberResponse{}), required)
	assertOpenAPISchemaMatchesDTO(t, document, "CreateAcademicUnitMemberRequest", reflect.TypeOf(createAcademicUnitMemberRequest{}), []string{"user_id"})
	list := document.Components.Schemas["AcademicUnitMemberListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/AcademicUnitMemberResponse" {
		t.Fatalf("AcademicUnitMemberListResponse = %#v", list)
	}
}
