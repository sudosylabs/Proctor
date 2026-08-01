// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

type openAPIDocument struct {
	OpenAPI    string                                `json:"openapi"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas       map[string]openAPISchema      `json:"schemas"`
		Responses     map[string]openAPIResponse    `json:"responses"`
		RequestBodies map[string]openAPIRequestBody `json:"requestBodies"`
	} `json:"components"`
}

type openAPIOperation struct {
	OperationID string                      `json:"operationId"`
	Auth        AuthRequirement             `json:"x-proctor-auth"`
	ErrorCodes  []string                    `json:"x-proctor-error-codes"`
	Security    []map[string][]string       `json:"security"`
	RequestBody openAPIReference            `json:"requestBody"`
	Responses   map[string]openAPIReference `json:"responses"`
}

type openAPIReference struct {
	Ref string `json:"$ref"`
}

type openAPISchema struct {
	Type                 any                        `json:"type"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties any                        `json:"additionalProperties"`
	Items                openAPIReference           `json:"items"`
}

type openAPIResponse struct {
	Headers map[string]openAPIReference `json:"headers"`
	Content map[string]struct {
		Schema openAPIReference `json:"schema"`
	} `json:"content"`
}

type openAPIRequestBody struct {
	Content map[string]struct {
		Schema openAPIReference `json:"schema"`
	} `json:"content"`
}

type openAPIOperationContract struct {
	requestBodyRef string
	requestSchema  string
	successStatus  string
	successRef     string
	successSchema  string
	errorCodes     []string
}

func TestAcademicUnitOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.OpenAPI)
	}

	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerAcademicUnitRoutes(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(
			route.Path,
			"{academic_unit_id:"+canonicalIDRoutePattern()+"}",
			"{academic_unit_id}",
		)
		runtimeOperations[route.Method+" "+path] = route.Auth
	}

	documentedOperations := make(map[string]AuthRequirement)
	operationIDs := make(map[string]string)
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	expectedContracts := map[string]openAPIOperationContract{
		"GET /api/v1/academic-units": {
			successStatus: "200", successRef: "#/components/responses/AcademicUnitListOK",
			successSchema: "AcademicUnitListResponse",
			errorCodes: principalContractCodes(
				"request.invalid", "administration.unavailable",
			),
		},
		"POST /api/v1/academic-units": {
			requestBodyRef: "#/components/requestBodies/CreateAcademicUnit",
			requestSchema:  "CreateAcademicUnitRequest",
			successStatus:  "201", successRef: "#/components/responses/AcademicUnitCreated",
			successSchema: "AcademicUnitResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "administration.unavailable",
				"academic_unit.invalid", "academic_unit.conflict",
			),
		},
		"GET /api/v1/academic-units/{academic_unit_id}": {
			successStatus: "200", successRef: "#/components/responses/AcademicUnitOK",
			successSchema: "AcademicUnitResponse",
			errorCodes: principalContractCodes(
				"resource.not_found", "administration.unavailable",
			),
		},
		"PATCH /api/v1/academic-units/{academic_unit_id}": {
			requestBodyRef: "#/components/requestBodies/UpdateAcademicUnit",
			requestSchema:  "UpdateAcademicUnitRequest",
			successStatus:  "200", successRef: "#/components/responses/AcademicUnitOK",
			successSchema: "AcademicUnitResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "academic_unit.invalid",
				"academic_unit.conflict", "administration.unavailable",
			),
		},
		"DELETE /api/v1/academic-units/{academic_unit_id}": {
			successStatus: "204", successRef: "#/components/responses/AcademicUnitArchived",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "academic_unit.conflict",
				"administration.unavailable",
			),
		},
		"GET /api/v1/academic-units/{academic_unit_id}/children": {
			successStatus: "200", successRef: "#/components/responses/AcademicUnitListOK",
			successSchema: "AcademicUnitListResponse",
			errorCodes: principalContractCodes(
				"resource.not_found", "administration.unavailable",
			),
		},
		"POST /api/v1/academic-units/{academic_unit_id}/children": {
			requestBodyRef: "#/components/requestBodies/CreateAcademicUnit",
			requestSchema:  "CreateAcademicUnitRequest",
			successStatus:  "201", successRef: "#/components/responses/AcademicUnitCreated",
			successSchema: "AcademicUnitResponse",
			errorCodes: principalMutationContractCodes(
				"request.invalid", "resource.not_found", "academic_unit.invalid",
				"academic_unit.conflict", "administration.unavailable",
			),
		},
	}
	for path, pathItem := range document.Paths {
		if !strings.HasPrefix(path, model.APIURLSuffix+"/academic-units") {
			continue
		}
		for method, raw := range pathItem {
			upperMethod := strings.ToUpper(method)
			if !isHTTPMethod(upperMethod) {
				continue
			}
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("decode %s %s: %v", upperMethod, path, err)
			}
			key := upperMethod + " " + path
			documentedOperations[key] = operation.Auth
			if operation.OperationID == "" {
				t.Errorf("%s has no operationId", key)
			} else if prior, exists := operationIDs[operation.OperationID]; exists {
				t.Errorf("operationId %q is shared by %s and %s", operation.OperationID, prior, key)
			} else {
				operationIDs[operation.OperationID] = key
			}
			if operation.Auth != AuthPrincipalRequired {
				t.Errorf("%s auth = %q, want %q", key, operation.Auth, AuthPrincipalRequired)
			}
			assertPrincipalSecurity(t, key, upperMethod, operation.Security)
			contract, exists := expectedContracts[key]
			if !exists {
				t.Errorf("%s is not an expected Academic Unit operation", key)
			} else {
				if operation.RequestBody.Ref != contract.requestBodyRef {
					t.Errorf("%s request body = %q, want %q", key, operation.RequestBody.Ref, contract.requestBodyRef)
				}
				if response := operation.Responses[contract.successStatus]; response.Ref != contract.successRef {
					t.Errorf("%s success response = %#v, want ref %q", key, response, contract.successRef)
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
			}
			for _, code := range operation.ErrorCodes {
				status, exists := statuses[code]
				if !exists {
					t.Errorf("%s documents unmapped error code %q", key, code)
					continue
				}
				response, exists := operation.Responses[strconv.Itoa(status)]
				if !exists || response.Ref == "" {
					t.Errorf("%s code %q has no %d Problem response", key, code, status)
					continue
				}
				assertOpenAPIProblemResponse(t, document, key, status, response)
			}
		}
	}
	if !reflect.DeepEqual(documentedOperations, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime operations = %#v", documentedOperations, runtimeOperations)
	}

	assertOpenAPISchemaMatchesDTO(
		t, document, "AcademicUnitResponse", reflect.TypeOf(academicUnitResponse{}),
		[]string{"id", "create_at", "update_at", "delete_at", "institution_id", "name", "display_name", "description"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "CreateAcademicUnitRequest", reflect.TypeOf(createAcademicUnitRequest{}),
		[]string{"name", "display_name"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "UpdateAcademicUnitRequest", reflect.TypeOf(updateAcademicUnitRequest{}), nil,
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "ProblemDetails", reflect.TypeOf(Problem{}),
		[]string{"type", "title", "status", "code"},
	)
	listSchema := document.Components.Schemas["AcademicUnitListResponse"]
	if listSchema.Type != "array" ||
		listSchema.Items.Ref != "#/components/schemas/AcademicUnitResponse" {
		t.Fatalf("AcademicUnitListResponse = %#v", listSchema)
	}
	archivedResponse := document.Components.Responses["AcademicUnitArchived"]
	if archivedResponse.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("AcademicUnitArchived does not require no-store: %#v", archivedResponse)
	}
}

func principalContractCodes(extra ...string) []string {
	return append([]string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
	}, extra...)
}

func principalMutationContractCodes(extra ...string) []string {
	return principalContractCodes(append([]string{
		"authentication.csrf.invalid",
		"audit.unavailable",
	}, extra...)...)
}

func assertOpenAPIRequestBody(
	t *testing.T,
	document openAPIDocument,
	operation string,
	contract openAPIOperationContract,
) {
	t.Helper()
	if contract.requestBodyRef == "" {
		return
	}
	const prefix = "#/components/requestBodies/"
	name := strings.TrimPrefix(contract.requestBodyRef, prefix)
	if name == contract.requestBodyRef {
		t.Errorf("%s request body ref = %q", operation, contract.requestBodyRef)
		return
	}
	requestBody, exists := document.Components.RequestBodies[name]
	if !exists {
		t.Errorf("%s request body component %q is missing", operation, name)
		return
	}
	wantSchema := "#/components/schemas/" + contract.requestSchema
	if requestBody.Content["application/json"].Schema.Ref != wantSchema {
		t.Errorf("%s request schema = %#v, want %q", operation, requestBody, wantSchema)
	}
}

func assertOpenAPISuccessResponse(
	t *testing.T,
	document openAPIDocument,
	operation string,
	contract openAPIOperationContract,
) {
	t.Helper()
	const prefix = "#/components/responses/"
	name := strings.TrimPrefix(contract.successRef, prefix)
	if name == contract.successRef {
		t.Errorf("%s success response ref = %q", operation, contract.successRef)
		return
	}
	response, exists := document.Components.Responses[name]
	if !exists {
		t.Errorf("%s success response component %q is missing", operation, name)
		return
	}
	if contract.successSchema == "" {
		if len(response.Content) != 0 {
			t.Errorf("%s no-content response has content: %#v", operation, response.Content)
		}
		return
	}
	wantSchema := "#/components/schemas/" + contract.successSchema
	if response.Content["application/json"].Schema.Ref != wantSchema {
		t.Errorf("%s success schema = %#v, want %q", operation, response, wantSchema)
	}
}

func assertPrincipalSecurity(
	t *testing.T,
	operation string,
	method string,
	security []map[string][]string,
) {
	t.Helper()
	want := []map[string][]string{{"bearerAuth": {}}}
	if method == http.MethodGet {
		want = append(want, map[string][]string{"sessionCookie": {}})
	} else {
		want = append(want, map[string][]string{"sessionCookie": {}, "csrfToken": {}})
	}
	if !reflect.DeepEqual(security, want) {
		t.Errorf("%s security = %#v, want %#v", operation, security, want)
	}
}

func assertOpenAPIProblemResponse(
	t *testing.T,
	document openAPIDocument,
	operation string,
	status int,
	reference openAPIReference,
) {
	t.Helper()
	const responsePrefix = "#/components/responses/"
	if !strings.HasPrefix(reference.Ref, responsePrefix) {
		t.Errorf("%s %d response = %q, want component response", operation, status, reference.Ref)
		return
	}
	responseName := strings.TrimPrefix(reference.Ref, responsePrefix)
	response, exists := document.Components.Responses[responseName]
	if !exists {
		t.Errorf("%s %d response component %q is missing", operation, status, responseName)
		return
	}
	content, exists := response.Content["application/problem+json"]
	if !exists || content.Schema.Ref != "#/components/schemas/ProblemDetails" {
		t.Errorf("%s %d response does not use ProblemDetails: %#v", operation, status, response)
	}
	if response.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Errorf("%s %d response does not require no-store: %#v", operation, status, response)
	}
}

func readOpenAPIDocument(t *testing.T) openAPIDocument {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate agreement test")
	}
	documentPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "openapi.json")
	encoded, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatalf("read checked-in OpenAPI document: %v", err)
	}
	var document openAPIDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode checked-in OpenAPI document: %v", err)
	}
	return document
}

func assertOpenAPISchemaMatchesDTO(
	t *testing.T,
	document openAPIDocument,
	schemaName string,
	dto reflect.Type,
	required []string,
) {
	t.Helper()
	schema, exists := document.Components.Schemas[schemaName]
	if !exists {
		t.Fatalf("OpenAPI schema %q is missing", schemaName)
	}
	if schema.Type != "object" || schema.AdditionalProperties != false {
		t.Fatalf("OpenAPI schema %q is not a closed object: %#v", schemaName, schema)
	}
	wantProperties := jsonFieldNames(dto)
	gotProperties := make([]string, 0, len(schema.Properties))
	for property := range schema.Properties {
		gotProperties = append(gotProperties, property)
	}
	sort.Strings(gotProperties)
	if !reflect.DeepEqual(gotProperties, wantProperties) {
		t.Fatalf("OpenAPI schema %q fields = %v, DTO fields = %v", schemaName, gotProperties, wantProperties)
	}
	sort.Strings(required)
	gotRequired := append([]string(nil), schema.Required...)
	sort.Strings(gotRequired)
	if !reflect.DeepEqual(gotRequired, required) {
		t.Fatalf("OpenAPI schema %q required = %v, want %v", schemaName, gotRequired, required)
	}
}

func jsonFieldNames(dto reflect.Type) []string {
	fields := make([]string, 0, dto.NumField())
	for index := 0; index < dto.NumField(); index++ {
		name := strings.Split(dto.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func isHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
