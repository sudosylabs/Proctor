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

func TestClassMemberOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerClassMemberRoutes(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{class_id:"+canonicalIDRoutePattern()+"}", "{class_id}")
		path = strings.ReplaceAll(path, "{class_member_id:"+canonicalIDRoutePattern()+"}", "{class_member_id}")
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/classes/{class_id}/members":         {successStatus: "200", successRef: "#/components/responses/ClassMemberListOK", successSchema: "ClassMemberListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"POST /api/v1/classes/{class_id}/members":        {requestBodyRef: "#/components/requestBodies/EnrollClassMember", requestSchema: "EnrollClassMemberRequest", successStatus: "201", successRef: "#/components/responses/ClassMemberEnrolled", successSchema: "ClassEnrollmentResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class_member.invalid", "class_member.student_affiliation_required", "class.enrollment_conflict", "administration.unavailable")},
		"DELETE /api/v1/class-members/{class_member_id}": {successStatus: "200", successRef: "#/components/responses/ClassMemberEnded", successSchema: "ClassMemberResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "class.enrollment_conflict", "administration.unavailable")},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/classes/{class_id}/members" && !strings.HasPrefix(path, model.APIURLSuffix+"/class-members/") {
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
				assertOpenAPIProblemResponse(t, document, key, status, operation.Responses[strconv.Itoa(status)])
			}
		}
	}
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime = %#v", documented, runtimeOperations)
	}
	required := []string{"id", "create_at", "update_at", "delete_at", "class_id", "academic_period_id", "user_id", "start_at"}
	assertOpenAPISchemaMatchesDTO(t, document, "ClassMemberResponse", reflect.TypeOf(classMemberResponse{}), required)
	assertOpenAPISchemaMatchesDTO(t, document, "EnrollClassMemberRequest", reflect.TypeOf(enrollClassMemberRequest{}), []string{"user_id"})
	assertOpenAPISchemaMatchesDTO(t, document, "ClassEnrollmentResponse", reflect.TypeOf(classEnrollmentResponse{}), []string{"membership"})
	list := document.Components.Schemas["ClassMemberListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/ClassMemberResponse" {
		t.Fatalf("ClassMemberListResponse = %#v", list)
	}
	var listOperation struct {
		Parameters []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(document.Paths["/api/v1/classes/{class_id}/members"]["get"], &listOperation); err != nil {
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
}
