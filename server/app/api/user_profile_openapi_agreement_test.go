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
	if err := runtimeAPI.initUserAdministration(); err != nil {
		t.Fatal(err)
	}
	if err := runtimeAPI.Register(runtimeAPI.BaseRoutes.CurrentUser, "", http.MethodGet, runtimeAPI.APIPrincipalRequired(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))); err != nil {
		t.Fatal(err)
	}
	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		path := strings.ReplaceAll(route.Path, "{user_id:"+canonicalIDRoutePattern()+"}", "{user_id}")
		if path != "/api/v1/users" && path != "/api/v1/users/me" && path != "/api/v1/users/{user_id}" && path != "/api/v1/users/{user_id}/profile-picture" && path != "/api/v1/users/{user_id}/disable" && path != "/api/v1/users/{user_id}/enable" {
			continue
		}
		runtimeOperations[route.Method+" "+path] = route.Auth
	}
	expected := map[string]openAPIOperationContract{
		"GET /api/v1/users":                           {successStatus: "200", successRef: "#/components/responses/UserProfileListOK", successSchema: "UserProfileListResponse", errorCodes: principalContractCodes("request.invalid", "user.invalid", "administration.unavailable")},
		"GET /api/v1/users/me":                        {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalContractCodes("resource.not_found", "administration.unavailable")},
		"GET /api/v1/users/{user_id}":                 {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable")},
		"PATCH /api/v1/users/{user_id}":               {requestBodyRef: "#/components/requestBodies/UpdateUserProfile", requestSchema: "UpdateUserProfileRequest", successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "administration.unavailable")},
		"GET /api/v1/users/{user_id}/profile-picture": {successStatus: "200", successRef: "#/components/responses/ProfilePictureOK", successSchema: "binary", errorCodes: principalContractCodes("request.invalid", "resource.not_found", "profile_picture.unavailable")},
		"PUT /api/v1/users/{user_id}/profile-picture": {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalContractCodes("authentication.csrf.invalid", "request.invalid", "resource.not_found", "profile_picture.invalid", "profile_picture.unavailable", "user.conflict")},
		"POST /api/v1/users/{user_id}/disable":        {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "user.last_system_admin", "administration.unavailable")},
		"POST /api/v1/users/{user_id}/enable":         {successStatus: "200", successRef: "#/components/responses/UserProfileOK", successSchema: "UserProfileResponse", errorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "user.last_system_admin", "administration.unavailable")},
	}
	statuses := ApplicationErrorStatuses()
	statuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	statuses["authentication.csrf.invalid"] = http.StatusForbidden
	documented := make(map[string]AuthRequirement)
	for path, item := range document.Paths {
		if path != "/api/v1/users" && path != "/api/v1/users/me" && path != "/api/v1/users/{user_id}" && path != "/api/v1/users/{user_id}/profile-picture" && path != "/api/v1/users/{user_id}/disable" && path != "/api/v1/users/{user_id}/enable" {
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
			if contract.successSchema == "binary" {
				response := document.Components.Responses["ProfilePictureOK"]
				shape := response.Content["image/webp"].Schema
				_, hasETag := response.Headers["ETag"]
				_, hasCacheControl := response.Headers["Cache-Control"]
				if shape.Type != "string" || shape.Format != "binary" || !hasETag || !hasCacheControl {
					t.Errorf("%s binary response = %#v", key, response)
				}
				hasConditionalHeader := false
				for _, parameter := range operation.Parameters {
					hasConditionalHeader = hasConditionalHeader || (parameter.Name == "If-None-Match" && parameter.In == "header")
				}
				if !hasConditionalHeader {
					t.Errorf("%s does not document If-None-Match", key)
				}
			} else {
				assertOpenAPISuccessResponse(t, document, key, contract)
			}
			if key == "PUT /api/v1/users/{user_id}/profile-picture" {
				for _, mediaType := range []string{"image/png", "image/jpeg", "image/webp"} {
					shape := operation.RequestBody.Content[mediaType].Schema
					if shape.Type != "string" || shape.Format != "binary" {
						t.Errorf("%s request %s = %#v", key, mediaType, shape)
					}
				}
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
		t.Fatalf("OpenAPI operations = %#v, runtime = %#v", documented, runtimeOperations)
	}
	required := []string{"id", "create_at", "update_at", "delete_at", "username", "email", "email_verified", "display_name", "first_name", "last_name", "locale", "timezone", "profile_picture_url"}
	assertOpenAPISchemaMatchesDTO(t, document, "UserProfileResponse", reflect.TypeOf(userProfileResponse{}), required)
	assertOpenAPISchemaMatchesDTO(t, document, "UpdateUserProfileRequest", reflect.TypeOf(updateUserProfileRequest{}), nil)
	list := document.Components.Schemas["UserProfileListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/UserProfileResponse" {
		t.Fatalf("UserProfileListResponse = %#v", list)
	}
}
