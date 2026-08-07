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

func TestSessionAdministrationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.initUserAdministration(); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{user_id:"+canonicalIDRoutePattern()+"}", "{user_id}")
		path = strings.ReplaceAll(path, "{session_id:"+canonicalIDRoutePattern()+"}", "{session_id}")
		if !strings.Contains(path, "/sessions") {
			continue
		}
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/users/{user_id}/sessions": {
			successStatus: "200", successRef: "#/components/responses/SessionAdministrationListOK",
			successSchema: "SessionAdministrationListResponse",
			errorCodes:    principalContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
		},
		"POST /api/v1/users/{user_id}/sessions/revoke-all": {
			successStatus: "204", successRef: "#/components/responses/SessionAdministrationRevoked",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
		},
		"DELETE /api/v1/users/{user_id}/sessions/{session_id}": {
			successStatus: "204", successRef: "#/components/responses/SessionAdministrationRevoked",
			errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
		},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if !strings.Contains(path, "/sessions") || !strings.HasPrefix(path, model.APIURLSuffix+"/users/") {
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
	list := document.Components.Schemas["SessionAdministrationListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/SessionAdministrationResponse" {
		t.Fatalf("SessionAdministrationListResponse = %#v", list)
	}
	sessionSchema := document.Components.Schemas["SessionAdministrationResponse"]
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "credential", "token_hash"} {
		if _, exists := sessionSchema.Properties[forbidden]; exists {
			t.Fatalf("session schema exposes %q", forbidden)
		}
	}
}
