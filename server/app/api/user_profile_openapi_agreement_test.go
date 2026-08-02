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

func TestUserProfileOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerUserProfileRoutes(); err != nil {
		t.Fatal(err)
	}
	if err := runtimeAPI.Register(runtimeAPI.BaseRoutes.CurrentUser, "", http.MethodGet, runtimeAPI.APIPrincipalRequired(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{user_id:"+canonicalIDRoutePattern()+"}", "{user_id}")
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/users":             {successStatus: "200", successRef: "#/components/responses/UserProfileListOK", successSchema: "UserProfileListResponse", errorCodes: principalContractCodes("request.invalid", "user.invalid", "administration.unavailable")},
		"GET /api/v1/users/me":          {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalContractCodes("resource.not_found", "administration.unavailable")},
		"GET /api/v1/users/{user_id}":   {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"PATCH /api/v1/users/{user_id}": {requestBodyRef: "#/components/requestBodies/UpdateUserProfile", requestSchema: "UpdateUserProfileRequest", successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "administration.unavailable")},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/users" && path != "/api/v1/users/me" && path != "/api/v1/users/{user_id}" {
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
	required := []string{"id", "create_at", "update_at", "delete_at", "username", "email", "email_verified", "display_name", "first_name", "last_name", "locale", "timezone"}
	assertOpenAPISchemaMatchesDTO(t, document, "UserProfileResponse", reflect.TypeOf(userProfileResponse{}), required)
	assertOpenAPISchemaMatchesDTO(t, document, "UpdateUserProfileRequest", reflect.TypeOf(updateUserProfileRequest{}), nil)
	list := document.Components.Schemas["UserProfileListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/UserProfileResponse" {
		t.Fatalf("UserProfileListResponse = %#v", list)
	}
}
