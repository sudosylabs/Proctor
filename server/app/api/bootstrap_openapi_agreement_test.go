// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestBootstrapOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.InitBootstrap(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		if route.Path != model.APIURLSuffix+"/bootstrap" {
			continue
		}
		runtimeOperations[route.Method+" "+route.Path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/bootstrap": {
			successStatus: "200", successRef: "#/components/responses/InstallationStatusOK",
			successSchema: "InstallationStatusResponse",
			errorCodes:    []string{"installation.unavailable"},
		},
		"POST /api/v1/bootstrap": {
			requestBodyRef: "#/components/requestBodies/BootstrapInstallation",
			requestSchema:  "BootstrapInstallationRequest",
			successStatus:  "201", successRef: "#/components/responses/InstallationBootstrapCreated",
			successSchema: "InstallationBootstrapResult",
			errorCodes: []string{
				"request.invalid", "installation.already_initialized", "installation.unavailable",
				"authentication.password.invalid", "authentication.rate_limited",
			},
		},
	}
	statuses := ApplicationErrorStatuses()
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/bootstrap" {
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
			if operation.Auth != AuthPublic {
				t.Errorf("%s auth = %q, want public", key, operation.Auth)
			}
			if len(operation.Security) != 0 {
				t.Errorf("%s security = %#v, want empty for public routes", key, operation.Security)
			}
			if operation.RequestBody.Ref != contract.requestBodyRef ||
				operation.Responses[contract.successStatus].Ref != contract.successRef {
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
		t, document, "InstallationStatusResponse", reflect.TypeOf(installationStatusResponse{}),
		[]string{"initialized"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "BootstrapInstallationRequest", reflect.TypeOf(bootstrapRequest{}),
		[]string{"institution", "administrator", "password"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "InstallationStateResponse", reflect.TypeOf(installationStateResponse{}),
		[]string{"initialized_at", "institution_id", "administrator_user_id"},
	)
	assertOpenAPISchemaMatchesDTO(
		t, document, "InstallationBootstrapResult", reflect.TypeOf(installationBootstrapResponse{}),
		nil,
	)
}
