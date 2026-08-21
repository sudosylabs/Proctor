// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionAdministrationOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{
				Key: "GET /api/v1/users/{user_id}/sessions", Auth: AuthPrincipalRequired,
				SuccessStatus: "200", SuccessRef: "#/components/responses/SessionAdministrationListOK", SuccessSchema: "SessionAdministrationListResponse",
				PublicErrorCodes: principalContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
			},
			{
				Key: "POST /api/v1/users/{user_id}/sessions/revoke-all", Auth: AuthPrincipalRequired,
				SuccessStatus: "204", SuccessRef: "#/components/responses/SessionAdministrationRevoked",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
			},
			{
				Key: "DELETE /api/v1/users/{user_id}/sessions/{session_id}", Auth: AuthPrincipalRequired,
				SuccessStatus: "204", SuccessRef: "#/components/responses/SessionAdministrationRevoked",
				PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "session.not_found", "administration.unavailable"),
			},
		},
		Schemas: []openAPIAgreementSchema{
			{
				Name: "SessionAdministrationResponse", DTO: reflect.TypeOf(sessionResponse{}),
				Required: []string{
					"id", "create_at", "update_at", "delete_at", "user_id", "client_type",
					"authentication_method", "authentication_strength", "authenticated_at",
					"last_activity_at", "idle_expires_at", "expires_at",
				},
			},
		},
		OperationSelector: func(_ string, path string) bool {
			return strings.HasPrefix(path, model.APIURLSuffix+"/users/") &&
				!strings.HasPrefix(path, model.APIURLSuffix+"/users/me/") &&
				strings.Contains(path, "/sessions")
		},
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, userAdministrationResource(nil, nil)); err != nil {
		t.Fatal(err)
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	list := document.Components.Schemas["SessionAdministrationListResponse"]
	if list.Type != "array" || list.Items.Ref != "#/components/schemas/SessionAdministrationResponse" {
		t.Fatalf("SessionAdministrationListResponse = %#v", list)
	}

	// Administrative session projections must never expose usable credentials
	// or their hashes, even when the underlying Session model gains fields.
	sessionSchema := document.Components.Schemas["SessionAdministrationResponse"]
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "credential", "token_hash"} {
		if _, exists := sessionSchema.Properties[forbidden]; exists {
			t.Fatalf("session schema exposes %q", forbidden)
		}
	}
}
