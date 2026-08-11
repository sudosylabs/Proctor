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
	Parameters  []openAPIParameter          `json:"parameters"`
	RequestBody openAPIRequestBody          `json:"requestBody"`
	Responses   map[string]openAPIReference `json:"responses"`
}

type openAPIParameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
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

type openAPISchemaShape struct {
	Ref                  string                     `json:"$ref"`
	Type                 any                        `json:"type"`
	Format               string                     `json:"format"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Items                *openAPISchemaShape        `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

type openAPIResponse struct {
	Headers map[string]openAPIReference `json:"headers"`
	Content map[string]struct {
		Schema openAPISchemaShape `json:"schema"`
	} `json:"content"`
}

type openAPIRequestBody struct {
	Ref     string `json:"$ref"`
	Content map[string]struct {
		Schema openAPISchemaShape `json:"schema"`
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

func TestOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.registerRoutes(); err != nil {
		t.Fatal(err)
	}

	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		key := route.Method + " " + normalizeRuntimeRoutePath(route.Path)
		runtimeOperations[key] = route.Auth
	}
	documentedOperations := make(map[string]AuthRequirement)
	operationIDs := make(map[string]string)
	errorStatuses := ApplicationErrorStatuses()
	errorStatuses["authentication.credential_ambiguous"] = http.StatusBadRequest
	errorStatuses["authentication.csrf.invalid"] = http.StatusForbidden
	errorStatuses["not_live"] = http.StatusServiceUnavailable
	errorStatuses["not_ready"] = http.StatusServiceUnavailable
	for path, pathItem := range document.Paths {
		for method, raw := range pathItem {
			upperMethod := strings.ToUpper(method)
			if !isHTTPMethod(upperMethod) {
				continue
			}
			key := upperMethod + " " + path
			var operation openAPIOperation
			if err := json.Unmarshal(raw, &operation); err != nil {
				t.Fatalf("decode %s: %v", key, err)
			}
			documentedOperations[key] = operation.Auth
			if operation.OperationID == "" {
				t.Errorf("%s has no operationId", key)
			} else if prior, exists := operationIDs[operation.OperationID]; exists {
				t.Errorf("operationId %q is shared by %s and %s", operation.OperationID, prior, key)
			} else {
				operationIDs[operation.OperationID] = key
			}
			if operation.Security == nil {
				t.Errorf("%s has no explicit security requirement", key)
			} else {
				assertSecurityMatchesAuth(t, key, upperMethod, operation.Auth, operation.Security)
			}
			if len(operation.Responses) == 0 {
				t.Errorf("%s has no responses", key)
			}
			for _, code := range operation.ErrorCodes {
				status, exists := errorStatuses[code]
				if !exists {
					t.Errorf("%s documents unmapped error code %q", key, code)
					continue
				}
				if operation.Responses[strconv.Itoa(status)].Ref == "" {
					t.Errorf("%s code %q has no %d response", key, code, status)
				}
			}
		}
	}
	if !reflect.DeepEqual(documentedOperations, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime operations = %#v", documentedOperations, runtimeOperations)
	}
}

func assertSecurityMatchesAuth(
	t *testing.T,
	operation string,
	method string,
	auth AuthRequirement,
	security []map[string][]string,
) {
	t.Helper()
	var want []map[string][]string
	switch auth {
	case AuthPublic:
		want = []map[string][]string{}
	case AuthRefreshCredentialRequired:
		want = []map[string][]string{{"refreshBearerAuth": {}}, {"refreshCookie": {}, "csrfToken": {}}}
	case AuthPrincipalRequired, AuthSessionRequired, AuthStrongSessionRequired,
		AuthRecentSessionRequired, AuthStrongRecentSessionRequired:
		want = []map[string][]string{{"bearerAuth": {}}}
		if method == http.MethodGet {
			want = append(want, map[string][]string{"sessionCookie": {}})
		} else {
			want = append(want, map[string][]string{"sessionCookie": {}, "csrfToken": {}})
		}
	default:
		t.Errorf("%s has unknown auth requirement %q", operation, auth)
		return
	}
	if !reflect.DeepEqual(security, want) {
		t.Errorf("%s security = %#v, want %#v", operation, security, want)
	}
}

func TestIdentityAndSystemOpenAPISchemasAgreeWithDTOs(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	tests := []struct {
		name     string
		dto      reflect.Type
		required []string
	}{
		{"HealthResponse", reflect.TypeOf(healthResponse{}), []string{"status"}},
		{"BuildInfoResponse", reflect.TypeOf(BuildInfo{}), []string{"version", "commit", "build_time", "go_version"}},
		{"LoginRequest", reflect.TypeOf(loginRequest{}), []string{"login_id", "password", "client_type"}},
		{"PasswordResetRequest", reflect.TypeOf(passwordResetRequest{}), []string{"email"}},
		{"PasswordResetCompletionRequest", reflect.TypeOf(passwordResetCompletion{}), []string{"token", "password"}},
		{"EmailVerificationCompletionRequest", reflect.TypeOf(emailVerificationCompletion{}), []string{"token"}},
		{"AuthenticationResponse", reflect.TypeOf(authenticationResponse{}), []string{"session"}},
		{"SessionResponse", reflect.TypeOf(sessionResponse{}), []string{"id", "create_at", "update_at", "delete_at", "user_id", "client_type", "authentication_method", "authentication_strength", "authenticated_at", "last_activity_at", "idle_expires_at", "expires_at"}},
		{"AuthenticationTokensResponse", reflect.TypeOf(authenticationTokensResponse{}), []string{"access_token", "refresh_token", "access_expires_at", "refresh_expires_at"}},
		{"RevokeSessionRequest", reflect.TypeOf(revokeSessionRequest{}), []string{"session_id"}},
		{"MFACodeRequest", reflect.TypeOf(mfaCodeRequest{}), []string{"code"}},
		{"MFARecoveryCodesResponse", reflect.TypeOf(mfaRecoveryCodesResponse{}), []string{"recovery_codes"}},
		{"MFASetupResponse", reflect.TypeOf(mfaSetupResponse{}), []string{"secret", "provisioning_uri", "expires_at"}},
		{"MFAActivationResponse", reflect.TypeOf(mfaActivationResponse{}), []string{"recovery_codes"}},
		{"MFAStatusResponse", reflect.TypeOf(mfaStatusResponse{}), []string{"enabled", "pending", "recovery_codes_remaining"}},
		{"CreatePersonalAccessTokenRequest", reflect.TypeOf(createPersonalAccessTokenRequest{}), []string{"description", "scopes", "expires_at"}},
		{"PersonalAccessTokenResponse", reflect.TypeOf(personalAccessTokenResponse{}), []string{"id", "create_at", "update_at", "delete_at", "user_id", "description", "scopes", "expires_at"}},
		{"PersonalAccessTokenCreationResponse", reflect.TypeOf(personalAccessTokenCreationResponse{}), []string{"token", "credential"}},
		{"ExternalAuthenticationProviderResponse", reflect.TypeOf(externalAuthenticationProviderResponse{}), []string{"id", "display_name", "type"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantRequired := append([]string(nil), test.required...)
			sort.Strings(wantRequired)
			if derived := requiredJSONFieldNames(test.dto); !reflect.DeepEqual(derived, wantRequired) {
				t.Fatalf("test required fields = %v, DTO JSON tags require %v", test.required, derived)
			}
			assertOpenAPISchemaMatchesDTO(t, document, test.name, test.dto, test.required)
		})
	}
}

func TestIdentityAndSystemOpenAPIOperationDTOsAgreeWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	contracts := map[string]openAPIOperationContract{
		"GET /health/live":                                                {successStatus: "200", successRef: "#/components/responses/HealthOK", successSchema: "HealthResponse"},
		"GET /health/ready":                                               {successStatus: "200", successRef: "#/components/responses/HealthOK", successSchema: "HealthResponse"},
		"GET /api/v1/system/version":                                      {successStatus: "200", successRef: "#/components/responses/BuildInfoOK", successSchema: "BuildInfoResponse"},
		"POST /api/v1/auth/login":                                         {requestBodyRef: "#/components/requestBodies/Login", requestSchema: "LoginRequest", successStatus: "200", successRef: "#/components/responses/AuthenticationOK", successSchema: "AuthenticationResponse"},
		"POST /api/v1/auth/refresh":                                       {successStatus: "200", successRef: "#/components/responses/AuthenticationOK", successSchema: "AuthenticationResponse"},
		"POST /api/v1/auth/logout":                                        {successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"POST /api/v1/auth/email-verification/request":                    {successStatus: "202", successRef: "#/components/responses/SensitiveAccepted"},
		"POST /api/v1/auth/email-verification/complete":                   {requestBodyRef: "#/components/requestBodies/CompleteEmailVerification", requestSchema: "EmailVerificationCompletionRequest", successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"POST /api/v1/auth/password-reset/request":                        {requestBodyRef: "#/components/requestBodies/RequestPasswordReset", requestSchema: "PasswordResetRequest", successStatus: "202", successRef: "#/components/responses/SensitiveAccepted"},
		"POST /api/v1/auth/password-reset/complete":                       {requestBodyRef: "#/components/requestBodies/CompletePasswordReset", requestSchema: "PasswordResetCompletionRequest", successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"GET /api/v1/auth/providers":                                      {successStatus: "200", successRef: "#/components/responses/ExternalAuthenticationProviderListOK", successSchema: "ExternalAuthenticationProviderListResponse"},
		"GET /api/v1/auth/providers/{provider_id}/login":                  {successStatus: "303", successRef: "#/components/responses/ExternalAuthenticationRedirect"},
		"GET /api/v1/auth/providers/{provider_id}/callback":               {successStatus: "303", successRef: "#/components/responses/ExternalAuthenticationRedirect"},
		"GET /api/v1/users/me/sessions":                                   {successStatus: "200", successRef: "#/components/responses/SessionListOK", successSchema: "SessionListResponse"},
		"POST /api/v1/users/me/sessions/revoke":                           {requestBodyRef: "#/components/requestBodies/RevokeSession", requestSchema: "RevokeSessionRequest", successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"POST /api/v1/users/me/sessions/revoke-all":                       {successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"GET /api/v1/users/me/mfa":                                        {successStatus: "200", successRef: "#/components/responses/MFAStatusOK", successSchema: "MFAStatusResponse"},
		"POST /api/v1/users/me/mfa/setup":                                 {successStatus: "201", successRef: "#/components/responses/MFASetupCreated", successSchema: "MFASetupResponse"},
		"POST /api/v1/users/me/mfa/activate":                              {requestBodyRef: "#/components/requestBodies/MFACode", requestSchema: "MFACodeRequest", successStatus: "200", successRef: "#/components/responses/MFAActivationOK", successSchema: "MFAActivationResponse"},
		"POST /api/v1/users/me/mfa/challenge":                             {requestBodyRef: "#/components/requestBodies/MFACode", requestSchema: "MFACodeRequest", successStatus: "200", successRef: "#/components/responses/SessionOK", successSchema: "SessionResponse"},
		"POST /api/v1/users/me/mfa/recovery-codes/regenerate":             {successStatus: "200", successRef: "#/components/responses/MFARecoveryCodesOK", successSchema: "MFARecoveryCodesResponse"},
		"POST /api/v1/users/me/mfa/disable":                               {successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
		"GET /api/v1/users/me/tokens":                                     {successStatus: "200", successRef: "#/components/responses/PersonalAccessTokenListOK", successSchema: "PersonalAccessTokenListResponse"},
		"POST /api/v1/users/me/tokens":                                    {requestBodyRef: "#/components/requestBodies/CreatePersonalAccessToken", requestSchema: "CreatePersonalAccessTokenRequest", successStatus: "201", successRef: "#/components/responses/PersonalAccessTokenCreated", successSchema: "PersonalAccessTokenCreationResponse"},
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/disable": {successStatus: "200", successRef: "#/components/responses/PersonalAccessTokenOK", successSchema: "PersonalAccessTokenResponse"},
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/enable":  {successStatus: "200", successRef: "#/components/responses/PersonalAccessTokenOK", successSchema: "PersonalAccessTokenResponse"},
		"DELETE /api/v1/users/me/tokens/{personal_access_token_id}":       {successStatus: "204", successRef: "#/components/responses/SensitiveNoContent"},
	}
	errorContracts := identityAndSystemErrorContracts()
	for key, contract := range contracts {
		method, path, _ := strings.Cut(key, " ")
		raw := document.Paths[path][strings.ToLower(method)]
		if raw == nil {
			t.Errorf("%s is missing", key)
			continue
		}
		var operation openAPIOperation
		if err := json.Unmarshal(raw, &operation); err != nil {
			t.Fatal(err)
		}
		if operation.RequestBody.Ref != contract.requestBodyRef {
			t.Errorf("%s request body = %q, want %q", key, operation.RequestBody.Ref, contract.requestBodyRef)
		}
		if operation.Responses[contract.successStatus].Ref != contract.successRef {
			t.Errorf("%s success response = %q, want %q", key, operation.Responses[contract.successStatus].Ref, contract.successRef)
		}
		gotErrors := append([]string(nil), operation.ErrorCodes...)
		wantErrors := append([]string(nil), errorContracts[key]...)
		sort.Strings(gotErrors)
		sort.Strings(wantErrors)
		if !reflect.DeepEqual(gotErrors, wantErrors) {
			t.Errorf("%s error codes = %v, want %v", key, gotErrors, wantErrors)
		}
		assertOpenAPIRequestBody(t, document, key, contract)
		assertOpenAPISuccessResponse(t, document, key, contract)
	}
	websocket := document.Paths["/api/v1/websocket"]["get"]
	var operation openAPIOperation
	if err := json.Unmarshal(websocket, &operation); err != nil {
		t.Fatal(err)
	}
	if _, exists := operation.Responses["101"]; !exists {
		t.Error("GET /api/v1/websocket has no 101 response")
	}
	gotErrors := append([]string(nil), operation.ErrorCodes...)
	wantErrors := append([]string(nil), errorContracts["GET /api/v1/websocket"]...)
	sort.Strings(gotErrors)
	sort.Strings(wantErrors)
	if !reflect.DeepEqual(gotErrors, wantErrors) {
		t.Errorf("GET /api/v1/websocket error codes = %v, want %v", gotErrors, wantErrors)
	}

	for name, item := range map[string]string{
		"SessionListResponse":                        "#/components/schemas/SessionResponse",
		"PersonalAccessTokenListResponse":            "#/components/schemas/PersonalAccessTokenResponse",
		"ExternalAuthenticationProviderListResponse": "#/components/schemas/ExternalAuthenticationProviderResponse",
	} {
		schema := document.Components.Schemas[name]
		if schema.Type != "array" || schema.Items.Ref != item {
			t.Errorf("%s = %#v, want array of %s", name, schema, item)
		}
	}
}

func identityAndSystemErrorContracts() map[string][]string {
	return map[string][]string{
		"GET /health/live":           {"not_live"},
		"GET /health/ready":          {"not_ready"},
		"GET /api/v1/system/version": {},
		"POST /api/v1/auth/login": {
			"request.invalid", "authentication.client_type.invalid", "authentication.password.invalid",
			"authentication.invalid_credentials", "authentication.mfa.required", "authentication.mfa.unavailable",
			"authentication.sessions.maximum_reached", "authentication.rate_limited",
			"authentication.rate_limit_unavailable", "authentication.internal",
		},
		"POST /api/v1/auth/refresh": {
			"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
			"authentication.csrf.invalid", "authentication.session.invalid", "authentication.internal",
		},
		"POST /api/v1/auth/logout": sessionMutationErrorCodes("authentication.internal"),
		"POST /api/v1/auth/email-verification/request": sessionMutationErrorCodes(
			"authentication.rate_limited", "authentication.rate_limit_unavailable",
			"authentication.account_recovery.unavailable",
		),
		"POST /api/v1/auth/email-verification/complete": {
			"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
			"authentication.account_token.invalid", "authentication.account_recovery.unavailable",
		},
		"POST /api/v1/auth/password-reset/request": {
			"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
			"authentication.account_recovery.unavailable",
		},
		"POST /api/v1/auth/password-reset/complete": {
			"request.invalid", "authentication.password.invalid", "authentication.rate_limited",
			"authentication.rate_limit_unavailable", "authentication.account_token.invalid",
			"authentication.account_recovery.unavailable",
		},
		"GET /api/v1/auth/providers": {},
		"GET /api/v1/auth/providers/{provider_id}/login": {
			"request.invalid", "authentication.external.request.invalid",
			"authentication.external.provider_not_found", "authentication.rate_limited",
			"authentication.rate_limit_unavailable", "authentication.external.unavailable",
			"authentication.external.rejected", "authentication.internal",
		},
		"GET /api/v1/auth/providers/{provider_id}/callback": {
			"request.invalid", "authentication.external.invalid", "authentication.external.provider_not_found",
			"authentication.external.rejected", "authentication.external.unavailable",
			"authentication.external.account_conflict", "authentication.external.account_not_linked",
			"authentication.sessions.maximum_reached", "authentication.internal", "audit.unavailable",
		},
		"GET /api/v1/users/me/sessions": sessionErrorCodes("authentication.internal"),
		"POST /api/v1/users/me/sessions/revoke": sessionMutationErrorCodes(
			"request.invalid", "session.id.invalid", "session.not_found", "authentication.internal",
		),
		"POST /api/v1/users/me/sessions/revoke-all": sessionMutationErrorCodes("authentication.internal"),
		"GET /api/v1/users/me/mfa": sessionErrorCodes(
			"authentication.mfa.disabled", "authentication.mfa.unavailable",
		),
		"POST /api/v1/users/me/mfa/setup": recentSessionMutationErrorCodes(
			"authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict",
			"authentication.mfa.unavailable", "authentication.internal", "audit.unavailable",
		),
		"POST /api/v1/users/me/mfa/activate": recentSessionMutationErrorCodes(
			"request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled",
			"authentication.mfa.not_found", "authentication.mfa.conflict",
			"authentication.mfa.unavailable", "authentication.internal", "audit.unavailable",
		),
		"POST /api/v1/users/me/mfa/challenge": sessionMutationErrorCodes(
			"request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled",
			"authentication.mfa.not_found", "authentication.mfa.conflict",
			"authentication.mfa.unavailable", "authentication.internal", "audit.unavailable",
		),
		"POST /api/v1/users/me/mfa/recovery-codes/regenerate": strongRecentSessionMutationErrorCodes(
			"authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict",
			"authentication.mfa.unavailable", "authentication.internal", "audit.unavailable",
		),
		"POST /api/v1/users/me/mfa/disable": strongRecentSessionMutationErrorCodes(
			"authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict",
			"authentication.mfa.unavailable", "audit.unavailable",
		),
		"GET /api/v1/users/me/tokens": sessionErrorCodes("personal_access_token.unavailable"),
		"POST /api/v1/users/me/tokens": recentSessionMutationErrorCodes(
			"request.invalid", "resource.not_found", "personal_access_token.invalid",
			"personal_access_token.maximum_reached", "personal_access_token.unavailable", "audit.unavailable",
		),
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/disable": sessionMutationErrorCodes(
			"request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable",
		),
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/enable": recentSessionMutationErrorCodes(
			"request.invalid", "resource.not_found", "personal_access_token.maximum_reached",
			"personal_access_token.unavailable", "audit.unavailable",
		),
		"DELETE /api/v1/users/me/tokens/{personal_access_token_id}": sessionMutationErrorCodes(
			"request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable",
		),
		"GET /api/v1/websocket": sessionErrorCodes("request.invalid"),
	}
}

func sessionErrorCodes(extra ...string) []string {
	return append([]string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
	}, extra...)
}

func sessionMutationErrorCodes(extra ...string) []string {
	return sessionErrorCodes(append([]string{"authentication.csrf.invalid"}, extra...)...)
}

func recentSessionMutationErrorCodes(extra ...string) []string {
	return sessionMutationErrorCodes(append([]string{"authentication.reauthentication_required"}, extra...)...)
}

func strongRecentSessionMutationErrorCodes(extra ...string) []string {
	return recentSessionMutationErrorCodes(append([]string{"authentication.strong_required"}, extra...)...)
}

func normalizeRuntimeRoutePath(path string) string {
	var normalized strings.Builder
	for index := 0; index < len(path); {
		if path[index] != '{' {
			normalized.WriteByte(path[index])
			index++
			continue
		}
		start := index
		colon := strings.IndexByte(path[start:], ':')
		if colon < 0 {
			normalized.WriteString(path[start:])
			break
		}
		colon += start
		depth := 1
		end := colon + 1
		for ; end < len(path) && depth > 0; end++ {
			switch path[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if depth != 0 {
			normalized.WriteString(path[start:])
			break
		}
		normalized.WriteString(path[start:colon])
		normalized.WriteByte('}')
		index = end
	}
	return normalized.String()
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
		if strings.HasSuffix(path, "/classes") {
			continue
		}
		if strings.HasSuffix(path, "/members") {
			continue
		}
		if strings.Contains(path, "/programmes") {
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
	for index := 0; index < dto.NumField(); index++ {
		field := dto.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		var property openAPISchemaShape
		if err := json.Unmarshal(schema.Properties[name], &property); err != nil {
			t.Fatalf("decode OpenAPI schema %s.%s: %v", schemaName, name, err)
		}
		assertOpenAPIShapeMatchesGoType(
			t, document, schemaName+"."+name, property, field.Type,
			strings.HasSuffix(schemaName, "Request"), !stringSliceContains(required, name),
		)
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

func requiredJSONFieldNames(dto reflect.Type) []string {
	fields := make([]string, 0, dto.NumField())
	for index := 0; index < dto.NumField(); index++ {
		tag := dto.Field(index).Tag.Get("json")
		parts := strings.Split(tag, ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		optional := false
		for _, option := range parts[1:] {
			optional = optional || option == "omitempty"
		}
		if !optional {
			fields = append(fields, parts[0])
		}
	}
	sort.Strings(fields)
	return fields
}

func assertOpenAPIShapeMatchesGoType(
	t *testing.T,
	document openAPIDocument,
	location string,
	shape openAPISchemaShape,
	goType reflect.Type,
	requestSchema bool,
	fieldOptional bool,
) {
	t.Helper()
	nullable := false
	for goType.Kind() == reflect.Pointer {
		// Optional request pointers accept explicit JSON null. Required request
		// pointers and present response pointers are non-null by contract.
		nullable = nullable || requestSchema && fieldOptional
		goType = goType.Elem()
	}
	if goType.PkgPath() == reflect.TypeOf(Optional[string]{}).PkgPath() &&
		strings.HasPrefix(goType.Name(), "Optional[") {
		// Optional[T] is the transport's explicit absent/null/value PATCH type.
		nullable = true
		goType = goType.Field(0).Type
	}
	if shape.Ref != "" {
		const prefix = "#/components/schemas/"
		name := strings.TrimPrefix(shape.Ref, prefix)
		component, exists := document.Components.Schemas[name]
		if name == shape.Ref || !exists {
			t.Errorf("%s has unresolved schema reference %q", location, shape.Ref)
			return
		}
		encoded, err := json.Marshal(component)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &shape); err != nil {
			t.Fatal(err)
		}
	}
	wantType := map[reflect.Kind]string{
		reflect.Bool: "boolean", reflect.Int: "integer", reflect.Int8: "integer",
		reflect.Int16: "integer", reflect.Int32: "integer", reflect.Int64: "integer",
		reflect.Uint: "integer", reflect.Uint8: "integer", reflect.Uint16: "integer",
		reflect.Uint32: "integer", reflect.Uint64: "integer", reflect.String: "string",
		reflect.Slice: "array", reflect.Array: "array", reflect.Map: "object",
		reflect.Struct: "object",
	}[goType.Kind()]
	if wantType == "" {
		t.Errorf("%s uses unsupported Go type %s", location, goType)
		return
	}
	wantTypes := []string{wantType}
	if nullable {
		wantTypes = append(wantTypes, "null")
	}
	if !openAPITypesEqual(shape.Type, wantTypes) {
		t.Errorf("%s OpenAPI type = %#v, Go JSON types = %v", location, shape.Type, wantTypes)
		return
	}
	if goType.Kind() == reflect.Int64 || goType.Kind() == reflect.Uint64 {
		if shape.Format != "int64" {
			t.Errorf("%s OpenAPI format = %q, want int64", location, shape.Format)
		}
	}
	if goType.Kind() == reflect.Slice || goType.Kind() == reflect.Array {
		if shape.Items == nil {
			t.Errorf("%s has no array item schema", location)
			return
		}
		assertOpenAPIShapeMatchesGoType(
			t, document, location+"[]", *shape.Items, goType.Elem(), requestSchema, false,
		)
		return
	}
	if goType.Kind() == reflect.Map {
		var additional openAPISchemaShape
		if goType.Key().Kind() != reflect.String ||
			len(shape.AdditionalProperties) == 0 ||
			json.Unmarshal(shape.AdditionalProperties, &additional) != nil {
			t.Errorf("%s map schema does not declare string-keyed additional properties", location)
			return
		}
		assertOpenAPIShapeMatchesGoType(
			t, document, location+"{}", additional, goType.Elem(), requestSchema, false,
		)
		return
	}
	if goType.Kind() != reflect.Struct {
		return
	}
	wantProperties := jsonFieldNames(goType)
	gotProperties := make([]string, 0, len(shape.Properties))
	for property := range shape.Properties {
		gotProperties = append(gotProperties, property)
	}
	sort.Strings(gotProperties)
	if !reflect.DeepEqual(gotProperties, wantProperties) {
		t.Errorf("%s OpenAPI fields = %v, Go fields = %v", location, gotProperties, wantProperties)
	}
	gotRequired := append([]string(nil), shape.Required...)
	wantRequired := requiredJSONFieldNames(goType)
	sort.Strings(gotRequired)
	if !reflect.DeepEqual(gotRequired, wantRequired) {
		t.Errorf("%s OpenAPI required = %v, Go JSON tags require %v", location, gotRequired, wantRequired)
	}
	for index := 0; index < goType.NumField(); index++ {
		field := goType.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		var property openAPISchemaShape
		if err := json.Unmarshal(shape.Properties[name], &property); err != nil {
			t.Errorf("decode %s.%s: %v", location, name, err)
			continue
		}
		assertOpenAPIShapeMatchesGoType(
			t, document, location+"."+name, property, field.Type, requestSchema,
			!stringSliceContains(shape.Required, name),
		)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func openAPITypesEqual(value any, want []string) bool {
	var got []string
	switch types := value.(type) {
	case string:
		got = []string{types}
	case []any:
		for _, candidate := range types {
			value, ok := candidate.(string)
			if !ok {
				return false
			}
			got = append(got, value)
		}
	default:
		return false
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	return reflect.DeepEqual(got, want)
}
