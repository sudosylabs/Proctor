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

func TestAffiliationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerAffiliationRoutes(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{user_id:"+canonicalIDRoutePattern()+"}", "{user_id}")
		path = strings.ReplaceAll(path, "{affiliation_id:"+canonicalIDRoutePattern()+"}", "{affiliation_id}")
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/users/{user_id}/affiliations":     {successStatus: "200", successRef: "#/components/responses/AffiliationListOK", successSchema: "AffiliationListResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"POST /api/v1/users/{user_id}/affiliations":    {requestBodyRef: "#/components/requestBodies/CreateAffiliation", requestSchema: "CreateAffiliationRequest", successStatus: "201", successRef: "#/components/responses/AffiliationCreated", successSchema: "AffiliationResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "affiliation.invalid", "affiliation.conflict", "administration.unavailable")},
		"DELETE /api/v1/affiliations/{affiliation_id}": {successStatus: "200", successRef: "#/components/responses/AffiliationEnded", successSchema: "AffiliationResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "affiliation.student_has_active_enrollment", "affiliation.conflict", "administration.unavailable")},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if !strings.HasSuffix(path, "/affiliations") && !strings.HasPrefix(path, model.APIURLSuffix+"/affiliations/") {
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
	required := []string{"id", "create_at", "update_at", "delete_at", "user_id", "kind", "start_at"}
	assertOpenAPISchemaMatchesDTO(t, document, "AffiliationResponse", reflect.TypeOf(affiliationResponse{}), required)
	assertOpenAPISchemaMatchesDTO(t, document, "CreateAffiliationRequest", reflect.TypeOf(createAffiliationRequest{}), []string{"kind"})
	list := document.Components.Schemas["AffiliationListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/AffiliationResponse" {
		t.Fatalf("AffiliationListResponse = %#v", list)
	}
}
