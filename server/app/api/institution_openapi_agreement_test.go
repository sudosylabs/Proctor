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

func TestInstitutionOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, institutionResource(nil)); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		runtimeOperations[route.Method+" "+route.Path] = route.Auth
	}

	expected := map[string]openAPIOperationContract{
		"GET /api/v1/institution": {
			successStatus: "200", successRef: "#/components/responses/InstitutionOK",
			successSchema: "InstitutionResponse",
			errorCodes: principalContractCodes(
				"resource.not_found", "administration.unavailable",
			),
		},
		"PATCH /api/v1/institution": {
			requestBodyRef: "#/components/requestBodies/UpdateInstitution",
			requestSchema:  "UpdateInstitutionRequest",
			successStatus:  "200", successRef: "#/components/responses/InstitutionOK",
			successSchema: "InstitutionResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "institution.invalid",
				"institution.conflict", "administration.unavailable",
			),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	pathItem := document.Paths[model.APIURLSuffix+"/institution"]
	for method, raw := range pathItem {
		upperMethod := strings.ToUpper(method)
		if !isHTTPMethod(upperMethod) {
			continue
		}
		key := upperMethod + " " + model.APIURLSuffix + "/institution"
		var operation openAPIOperation
		if err := json.Unmarshal(raw, &operation); err != nil {
			t.Fatal(err)
		}
		documented[key] = operation.Auth
		contract, exists := expected[key]
		if !exists {
			t.Fatalf("unexpected operation %s", key)
		}
		assertPrincipalSecurity(t, key, upperMethod, operation.Security)
		if operation.RequestBody.Ref != contract.requestBodyRef ||
			operation.Responses[contract.successStatus].Ref != contract.successRef {
			t.Errorf("%s request/success refs do not agree", key)
		}
		assertOpenAPIRequestBody(t, document, key, contract)
		assertOpenAPISuccessResponse(t, document, key, contract)
		gotCodes := append([]string(nil), operation.ErrorCodes...)
		wantCodes := append([]string(nil), contract.errorCodes...)
		sort.Strings(gotCodes)
		sort.Strings(wantCodes)
		if !reflect.DeepEqual(gotCodes, wantCodes) {
			t.Errorf("%s error codes = %v, want %v", key, gotCodes, wantCodes)
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
	if !reflect.DeepEqual(documented, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime = %#v", documented, runtimeOperations)
	}
	assertOpenAPISchemaMatchesDTO(
		t, document, "InstitutionResponse", reflect.TypeOf(institutionResponse{}),
		[]string{"id", "create_at", "update_at", "delete_at", "name", "display_name", "description"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "UpdateInstitutionRequest", reflect.TypeOf(updateInstitutionRequest{}), nil,
	)
	patchSchema := document.Components.Schemas["UpdateInstitutionRequest"]
	for _, propertyName := range []string{"name", "display_name", "description"} {
		var property struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(patchSchema.Properties[propertyName], &property); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(property.Description, "Omitted or null leaves the value unchanged") ||
			!strings.Contains(property.Description, "empty string is present") {
			t.Errorf("%s semantics = %q", propertyName, property.Description)
		}
	}
}
