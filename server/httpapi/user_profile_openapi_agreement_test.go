// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserProfileOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	selectedPath := func(path string) bool {
		switch path {
		case model.APIURLSuffix + "/users",
			model.APIURLSuffix + "/users/me",
			model.APIURLSuffix + "/users/{user_id}",
			model.APIURLSuffix + "/users/{user_id}/email",
			model.APIURLSuffix + "/users/{user_id}/email/verify",
			model.APIURLSuffix + "/users/{user_id}/profile-picture",
			model.APIURLSuffix + "/users/{user_id}/disable",
			model.APIURLSuffix + "/users/{user_id}/enable":
			return true
		default:
			return false
		}
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/users", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileListOK", SuccessSchema: "UserProfileListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "user.invalid", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/users/me", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalContractCodes("resource.not_found", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/users/{user_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "administration.unavailable"),
			},
			{
				Key: "PATCH /api/v1/users/{user_id}", Auth: AuthPrincipalRequired,
				RequestBodyRef: "#/components/requestBodies/UpdateUserProfile", RequestSchema: "UpdateUserProfileRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "administration.unavailable"),
			},
			{
				Key: "PUT /api/v1/users/{user_id}/email", Auth: AuthStrongRecentSessionRequired,
				RequestBodyRef: "#/components/requestBodies/ChangeUserEmail", RequestSchema: "ChangeUserEmailRequest",
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserEmailStateOK", SuccessSchema: "UserEmailStateResponse",
				PublicErrorCodes: principalMutationContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "user.invalid", "user.conflict", "authentication.account_recovery.unavailable", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/users/{user_id}/email/verify", Auth: AuthStrongRecentSessionRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserEmailStateOK", SuccessSchema: "UserEmailStateResponse",
				PublicErrorCodes: principalMutationContractCodes("authentication.strong_required", "authentication.reauthentication_required", "request.invalid", "resource.not_found", "user.conflict", "authentication.account_recovery.unavailable", "administration.unavailable"),
			},
			{
				Key: "GET /api/v1/users/{user_id}/profile-picture", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/ProfilePictureOK", ExceptionalSuccess: true,
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "profile_picture.unavailable"),
			},
			{
				Key: "PUT /api/v1/users/{user_id}/profile-picture", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "profile_picture.invalid", "profile_picture.unavailable", "user.conflict"),
			},
			{
				Key: "DELETE /api/v1/users/{user_id}/profile-picture", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "profile_picture.unavailable", "user.conflict"),
			},
			{
				Key: "POST /api/v1/users/{user_id}/disable", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "user.last_system_admin", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/users/{user_id}/enable", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/UserProfileOK", SuccessSchema: "UserProfileResponse",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "user.invalid", "user.conflict", "user.last_system_admin", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "UserProfileResponse", DTO: reflect.TypeOf(userProfileResponse{}),
				Required: []string{"id", "create_at", "update_at", "delete_at", "username", "display_name", "first_name", "last_name", "profile_picture_url"},
			},
			{Name: "UserEmailStateResponse", DTO: reflect.TypeOf(userEmailStateResponse{}), Required: []string{"id", "email_verified"}},
			{Name: "UpdateUserProfileRequest", DTO: reflect.TypeOf(updateUserProfileRequest{})},
			{Name: "ChangeUserEmailRequest", DTO: reflect.TypeOf(changeUserEmailRequest{}), Required: []string{"email"}},
		},
		OperationSelector: func(_ string, path string) bool { return selectedPath(path) },
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(
		model.APIURLSuffix,
		userProfileResource(nil),
		userAdministrationResource(nil, nil),
	); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	// Binary download/upload and conditional request behavior are reviewed
	// protocol exceptions, not ordinary JSON schema agreement.
	document := readOpenAPIDocument(t)
	picturePath := model.APIURLSuffix + "/users/{user_id}/profile-picture"
	download := decodeOpenAPIOperationForProfile(t, document, picturePath, "get")
	response := document.Components.Responses["ProfilePictureOK"]
	shape := response.Content["image/webp"].Schema
	_, hasETag := response.Headers["ETag"]
	_, hasCacheControl := response.Headers["Cache-Control"]
	if shape.Type != "string" || shape.Format != "binary" || !hasETag || !hasCacheControl {
		t.Errorf("GET %s binary response = %#v", picturePath, response)
	}
	if !hasOpenAPIHeaderParameter(download, "If-None-Match") {
		t.Errorf("GET %s does not document If-None-Match", picturePath)
	}

	upload := decodeOpenAPIOperationForProfile(t, document, picturePath, "put")
	for _, mediaType := range []string{"image/png", "image/jpeg", "image/webp"} {
		shape := upload.RequestBody.Content[mediaType].Schema
		if shape.Type != "string" || shape.Format != "binary" {
			t.Errorf("PUT %s request %s = %#v", picturePath, mediaType, shape)
		}
	}
	assertOpenAPIIfMatchForProfile(t, "PUT "+picturePath, upload, false)

	remove := decodeOpenAPIOperationForProfile(t, document, picturePath, "delete")
	assertOpenAPIIfMatchForProfile(t, "DELETE "+picturePath, remove, true)

	list := document.Components.Schemas["UserProfileListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/UserProfileResponse" {
		t.Fatalf("UserProfileListResponse = %#v", list)
	}
}

func decodeOpenAPIOperationForProfile(
	t *testing.T,
	document openAPIDocument,
	path string,
	method string,
) openAPIOperation {
	t.Helper()
	var operation openAPIOperation
	if err := json.Unmarshal(document.Paths[path][method], &operation); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return operation
}

func hasOpenAPIHeaderParameter(operation openAPIOperation, name string) bool {
	for _, parameter := range operation.Parameters {
		if parameter.Name == name && parameter.In == "header" {
			return true
		}
	}
	return false
}

func assertOpenAPIIfMatchForProfile(
	t *testing.T,
	key string,
	operation openAPIOperation,
	wantRequired bool,
) {
	t.Helper()
	for _, parameter := range operation.Parameters {
		if parameter.Name == "If-Match" && parameter.In == "header" {
			if parameter.Required != wantRequired {
				t.Errorf("%s If-Match required = %v, want %v", key, parameter.Required, wantRequired)
			}
			return
		}
	}
	t.Errorf("%s does not document If-Match", key)
}
