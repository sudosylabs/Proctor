// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
					"id", "created_at", "updated_at", "user_id", "client_type",
					"authentication_method", "authentication_strength", "mfa_recovery_required", "authenticated_at",
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
	require.NoError(t, runtimeAPI.collectResources(model.APIURLSuffix, userAdministrationResource(nil, nil)))
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	document := readOpenAPIDocument(t)
	list := document.Components.Schemas["SessionAdministrationListResponse"]
	require.Equal(t, "array", list.Type)
	require.Equal(t, "#/components/schemas/SessionAdministrationResponse", list.Items.Ref)

	// Administrative session projections must never expose usable credentials
	// or their hashes, even when the underlying Session model gains fields.
	sessionSchema := document.Components.Schemas["SessionAdministrationResponse"]
	for _, forbidden := range []string{"token", "access_token", "refresh_token", "credential", "token_hash"} {
		_, exists := sessionSchema.Properties[forbidden]
		require.False(t, exists, "session schema exposes %q", forbidden)
	}
}
