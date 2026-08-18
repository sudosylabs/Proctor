// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

// TestOpenAPIAgreesWithRuntime owns catalog-wide invariants. Resource contract
// details are independently declared through the shared agreement evaluator in
// the focused suites below and in the resource agreement files.
func TestOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, productionResources(Options{}, browserCookies{}, nil)...); err != nil {
		t.Fatal(err)
	}

	runtimeOperations := make(map[string]AuthRequirement)
	for _, route := range runtimeAPI.Routes() {
		key := route.Method + " " + normalizeRuntimeRoutePath(route.Path)
		if _, exists := runtimeOperations[key]; exists {
			t.Errorf("runtime catalog repeats %s", key)
		}
		runtimeOperations[key] = route.Auth
	}
	documentedOperations := make(map[string]AuthRequirement)
	operationIDs := make(map[string]string)
	statuses := openAPIErrorStatuses()
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
			} else if want, err := securityForAuth(operation.Auth, upperMethod); err != nil {
				t.Errorf("%s: %v", key, err)
			} else if !reflect.DeepEqual(operation.Security, want) {
				t.Errorf("%s security = %#v, want %#v", key, operation.Security, want)
			}
			if len(operation.Responses) == 0 {
				t.Errorf("%s has no responses", key)
			}
			for _, code := range operation.ErrorCodes {
				status, exists := statuses[code]
				if !exists {
					t.Errorf("%s documents unmapped error code %q", key, code)
					continue
				}
				if operation.Responses[statusCode(status)].Ref == "" {
					t.Errorf("%s code %q has no %d response", key, code, status)
				}
			}
		}
	}
	if !reflect.DeepEqual(documentedOperations, runtimeOperations) {
		t.Fatalf("OpenAPI operations = %#v, runtime operations = %#v", documentedOperations, runtimeOperations)
	}
}

func TestIdentityAndSystemOpenAPISchemasAgreeWithDTOs(t *testing.T) {
	t.Parallel()

	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, productionResources(Options{}, browserCookies{}, nil)...); err != nil {
		t.Fatal(err)
	}
	suite := identityAndSystemOpenAPIAgreementSuite()
	suite.OperationSelector = identityAndSystemOperation
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())
}

func TestIdentityAndSystemOpenAPIOperationDTOsAgreeWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	var websocket openAPIOperation
	if err := json.Unmarshal(document.Paths["/api/v1/websocket"]["get"], &websocket); err != nil {
		t.Fatal(err)
	}
	if _, exists := websocket.Responses["101"]; !exists {
		t.Error("GET /api/v1/websocket has no 101 response")
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

func identityAndSystemOpenAPIAgreementSuite() openAPIAgreementSuite {
	errors := identityAndSystemErrorContracts()
	operation := func(key string, auth AuthRequirement, requestRef, requestSchema, status, responseRef, responseSchema string) openAPIAgreementOperation {
		return openAPIAgreementOperation{Key: key, Auth: auth, RequestBodyRef: requestRef, RequestSchema: requestSchema, SuccessStatus: status, SuccessRef: responseRef, SuccessSchema: responseSchema, PublicErrorCodes: errors[key]}
	}
	operations := []openAPIAgreementOperation{
		operation("GET /health/live", AuthPublic, "", "", "200", "#/components/responses/HealthOK", "HealthResponse"),
		operation("GET /health/ready", AuthPublic, "", "", "200", "#/components/responses/HealthOK", "HealthResponse"),
		operation("GET /api/v1/system/version", AuthPublic, "", "", "200", "#/components/responses/BuildInfoOK", "BuildInfoResponse"),
		operation("POST /api/v1/auth/desktop/authorizations", AuthPublic, "#/components/requestBodies/DesktopAuthorizationStart", "DesktopAuthorizationStartRequest", "201", "#/components/responses/DesktopAuthorizationStarted", "DesktopAuthorizationStartResponse"),
		operation("POST /api/v1/auth/desktop/authorizations/approve", AuthSessionRequired, "#/components/requestBodies/DesktopAuthorizationProof", "DesktopAuthorizationProofRequest", "200", "#/components/responses/DesktopAuthorizationApproved", "DesktopAuthorizationApprovalResponse"),
		operation("POST /api/v1/auth/desktop/authorizations/cancel", AuthPublic, "#/components/requestBodies/DesktopAuthorizationProof", "DesktopAuthorizationProofRequest", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("POST /api/v1/auth/desktop/token", AuthPublic, "#/components/requestBodies/DesktopAuthorizationExchange", "DesktopAuthorizationExchangeRequest", "200", "#/components/responses/AuthenticationOK", "AuthenticationResponse"),
		operation("POST /api/v1/auth/login", AuthPublic, "#/components/requestBodies/Login", "LoginRequest", "200", "#/components/responses/AuthenticationOK", "AuthenticationResponse"),
		operation("POST /api/v1/auth/refresh", AuthRefreshCredentialRequired, "", "", "200", "#/components/responses/AuthenticationOK", "AuthenticationResponse"),
		operation("POST /api/v1/auth/logout", AuthSessionRequired, "", "", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("POST /api/v1/auth/email-verification/request", AuthSessionRequired, "", "", "202", "#/components/responses/SensitiveAccepted", ""),
		operation("POST /api/v1/auth/email-verification/complete", AuthPublic, "#/components/requestBodies/CompleteEmailVerification", "EmailVerificationCompletionRequest", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("POST /api/v1/auth/password-reset/request", AuthPublic, "#/components/requestBodies/RequestPasswordReset", "PasswordResetRequest", "202", "#/components/responses/SensitiveAccepted", ""),
		operation("POST /api/v1/auth/password-reset/complete", AuthPublic, "#/components/requestBodies/CompletePasswordReset", "PasswordResetCompletionRequest", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("GET /api/v1/auth/providers", AuthPublic, "", "", "200", "#/components/responses/ExternalAuthenticationProviderListOK", "ExternalAuthenticationProviderListResponse"),
		operation("GET /api/v1/auth/providers/{provider_id}/login", AuthPublic, "", "", "303", "#/components/responses/ExternalAuthenticationRedirect", ""),
		operation("POST /api/v1/auth/providers/{provider_id}/login", AuthPublic, "#/components/requestBodies/BeginInvitationExternalAuthentication", "ExternalAuthenticationStartRequest", "303", "#/components/responses/ExternalAuthenticationRedirect", ""),
		operation("GET /api/v1/auth/providers/{provider_id}/callback", AuthPublic, "", "", "303", "#/components/responses/ExternalAuthenticationRedirect", ""),
		operation("GET /api/v1/users/me/sessions", AuthSessionRequired, "", "", "200", "#/components/responses/SessionListOK", "SessionListResponse"),
		operation("POST /api/v1/users/me/sessions/revoke", AuthSessionRequired, "#/components/requestBodies/RevokeSession", "RevokeSessionRequest", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("POST /api/v1/users/me/sessions/revoke-all", AuthSessionRequired, "", "", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("GET /api/v1/users/me/mfa", AuthSessionRequired, "", "", "200", "#/components/responses/MFAStatusOK", "MFAStatusResponse"),
		operation("POST /api/v1/users/me/mfa/setup", AuthRecentSessionRequired, "", "", "201", "#/components/responses/MFASetupCreated", "MFASetupResponse"),
		operation("POST /api/v1/users/me/mfa/activate", AuthRecentSessionRequired, "#/components/requestBodies/MFACode", "MFACodeRequest", "200", "#/components/responses/MFAActivationOK", "MFAActivationResponse"),
		operation("POST /api/v1/users/me/mfa/challenge", AuthSessionRequired, "#/components/requestBodies/MFACode", "MFACodeRequest", "200", "#/components/responses/SessionOK", "SessionResponse"),
		operation("POST /api/v1/users/me/mfa/recovery-codes/regenerate", AuthStrongRecentSessionRequired, "", "", "200", "#/components/responses/MFARecoveryCodesOK", "MFARecoveryCodesResponse"),
		operation("POST /api/v1/users/me/mfa/disable", AuthStrongRecentSessionRequired, "", "", "204", "#/components/responses/SensitiveNoContent", ""),
		operation("GET /api/v1/users/me/tokens", AuthSessionRequired, "", "", "200", "#/components/responses/PersonalAccessTokenListOK", "PersonalAccessTokenListResponse"),
		operation("POST /api/v1/users/me/tokens", AuthRecentSessionRequired, "#/components/requestBodies/CreatePersonalAccessToken", "CreatePersonalAccessTokenRequest", "201", "#/components/responses/PersonalAccessTokenCreated", "PersonalAccessTokenCreationResponse"),
		operation("POST /api/v1/users/me/tokens/{personal_access_token_id}/disable", AuthSessionRequired, "", "", "200", "#/components/responses/PersonalAccessTokenOK", "PersonalAccessTokenResponse"),
		operation("POST /api/v1/users/me/tokens/{personal_access_token_id}/enable", AuthRecentSessionRequired, "", "", "200", "#/components/responses/PersonalAccessTokenOK", "PersonalAccessTokenResponse"),
		operation("DELETE /api/v1/users/me/tokens/{personal_access_token_id}", AuthSessionRequired, "", "", "204", "#/components/responses/SensitiveNoContent", ""),
		{Key: "GET /api/v1/websocket", Auth: AuthSessionRequired, SuccessStatus: "101", ExceptionalSuccess: true, PublicErrorCodes: errors["GET /api/v1/websocket"]},
	}
	return openAPIAgreementSuite{
		Operations: operations,
		Schemas: []openAPIAgreementSchema{
			{Name: "HealthResponse", DTO: reflect.TypeOf(healthResponse{}), Required: []string{"status"}},
			{Name: "BuildInfoResponse", DTO: reflect.TypeOf(BuildInfo{}), Required: []string{"version", "commit", "build_time", "go_version"}},
			{Name: "LoginRequest", DTO: reflect.TypeOf(loginRequest{}), Required: []string{"login_id", "password", "client_type"}},
			{Name: "DesktopAuthorizationStartRequest", DTO: reflect.TypeOf(desktopAuthorizationStartRequest{}), Required: []string{"callback_url", "state", "code_challenge", "authentication_method"}},
			{Name: "DesktopAuthorizationProofRequest", DTO: reflect.TypeOf(desktopAuthorizationProofRequest{}), Required: []string{"handle", "browser_proof", "state"}},
			{Name: "DesktopAuthorizationExchangeRequest", DTO: reflect.TypeOf(desktopAuthorizationExchangeRequest{}), Required: []string{"code", "state", "code_verifier"}},
			{Name: "DesktopAuthorizationStartResponse", DTO: reflect.TypeOf(desktopAuthorizationStartResponse{}), Required: []string{"authorization_url", "expires_at"}},
			{Name: "DesktopAuthorizationApprovalResponse", DTO: reflect.TypeOf(desktopAuthorizationApprovalResponse{}), Required: []string{"redirect_url", "expires_at"}},
			{Name: "PasswordResetRequest", DTO: reflect.TypeOf(passwordResetRequest{}), Required: []string{"email"}},
			{Name: "PasswordResetCompletionRequest", DTO: reflect.TypeOf(passwordResetCompletion{}), Required: []string{"token", "password"}},
			{Name: "EmailVerificationCompletionRequest", DTO: reflect.TypeOf(emailVerificationCompletion{}), Required: []string{"token"}},
			{Name: "AuthenticationResponse", DTO: reflect.TypeOf(authenticationResponse{}), Required: []string{"session"}},
			{Name: "SessionResponse", DTO: reflect.TypeOf(sessionResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "user_id", "client_type", "authentication_method", "authentication_strength", "authenticated_at", "last_activity_at", "idle_expires_at", "expires_at"}},
			{Name: "AuthenticationTokensResponse", DTO: reflect.TypeOf(authenticationTokensResponse{}), Required: []string{"access_token", "refresh_token", "access_expires_at", "refresh_expires_at"}},
			{Name: "RevokeSessionRequest", DTO: reflect.TypeOf(revokeSessionRequest{}), Required: []string{"session_id"}},
			{Name: "MFACodeRequest", DTO: reflect.TypeOf(mfaCodeRequest{}), Required: []string{"code"}},
			{Name: "MFARecoveryCodesResponse", DTO: reflect.TypeOf(mfaRecoveryCodesResponse{}), Required: []string{"recovery_codes"}},
			{Name: "MFASetupResponse", DTO: reflect.TypeOf(mfaSetupResponse{}), Required: []string{"secret", "provisioning_uri", "expires_at"}},
			{Name: "MFAActivationResponse", DTO: reflect.TypeOf(mfaActivationResponse{}), Required: []string{"recovery_codes"}},
			{Name: "MFAStatusResponse", DTO: reflect.TypeOf(mfaStatusResponse{}), Required: []string{"enabled", "pending", "recovery_codes_remaining"}},
			{Name: "CreatePersonalAccessTokenRequest", DTO: reflect.TypeOf(createPersonalAccessTokenRequest{}), Required: []string{"description", "scopes", "expires_at"}},
			{Name: "PersonalAccessTokenResponse", DTO: reflect.TypeOf(personalAccessTokenResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "user_id", "description", "scopes", "expires_at"}},
			{Name: "PersonalAccessTokenCreationResponse", DTO: reflect.TypeOf(personalAccessTokenCreationResponse{}), Required: []string{"token", "credential"}},
			{Name: "ExternalAuthenticationProviderResponse", DTO: reflect.TypeOf(externalAuthenticationProviderResponse{}), Required: []string{"id", "display_name", "type"}},
			{Name: "ExternalAuthenticationStartRequest", DTO: reflect.TypeOf(externalAuthenticationStartRequest{}), Required: []string{"invitation_claim"}},
		},
	}
}

func identityAndSystemOperation(_ string, path string) bool {
	return path == "/health/live" || path == "/health/ready" ||
		path == "/api/v1/system/version" || path == "/api/v1/websocket" ||
		strings.HasPrefix(path, "/api/v1/auth/") ||
		strings.HasPrefix(path, "/api/v1/users/me/sessions") ||
		strings.HasPrefix(path, "/api/v1/users/me/mfa") ||
		strings.HasPrefix(path, "/api/v1/users/me/tokens")
}

func identityAndSystemErrorContracts() map[string][]string {
	return map[string][]string{
		"GET /health/live": {"not_live"}, "GET /health/ready": {"not_ready"}, "GET /api/v1/system/version": {},
		"POST /api/v1/auth/login":                                         {"request.invalid", "authentication.client_type.invalid", "authentication.password.invalid", "authentication.invalid_credentials", "authentication.mfa.required", "authentication.mfa.invalid_code", "authentication.mfa.unavailable", "authentication.sessions.maximum_reached", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.internal"},
		"POST /api/v1/auth/desktop/authorizations":                        {"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.disabled", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable"},
		"POST /api/v1/auth/desktop/authorizations/approve":                sessionMutationErrorCodes("request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "audit.unavailable"),
		"POST /api/v1/auth/desktop/authorizations/cancel":                 {"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable"},
		"POST /api/v1/auth/desktop/token":                                 {"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.sessions.maximum_reached", "audit.unavailable"},
		"POST /api/v1/auth/refresh":                                       {"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous", "authentication.csrf.invalid", "authentication.session.invalid", "authentication.internal"},
		"POST /api/v1/auth/logout":                                        sessionMutationErrorCodes("authentication.internal"),
		"POST /api/v1/auth/email-verification/request":                    sessionMutationErrorCodes("authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.account_recovery.unavailable"),
		"POST /api/v1/auth/email-verification/complete":                   {"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.account_token.invalid", "authentication.account_recovery.unavailable"},
		"POST /api/v1/auth/password-reset/request":                        {"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.account_recovery.unavailable"},
		"POST /api/v1/auth/password-reset/complete":                       {"request.invalid", "authentication.password.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.account_token.invalid", "authentication.account_recovery.unavailable"},
		"GET /api/v1/auth/providers":                                      {"authentication.internal"},
		"GET /api/v1/auth/providers/{provider_id}/login":                  {"request.invalid", "authentication.external.request.invalid", "authentication.external.provider_not_found", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.external.unavailable", "authentication.external.rejected", "authentication.internal"},
		"POST /api/v1/auth/providers/{provider_id}/login":                 {"request.invalid", "authentication.external.request.invalid", "authentication.external.provider_not_found", "authentication.external.account_not_linked", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.external.unavailable", "authentication.external.rejected", "authentication.internal"},
		"GET /api/v1/auth/providers/{provider_id}/callback":               {"request.invalid", "authentication.external.invalid", "authentication.external.provider_not_found", "authentication.external.rejected", "authentication.external.unavailable", "authentication.external.account_conflict", "authentication.external.account_not_linked", "authentication.method.disabled", "authentication.method.last_usable", "authentication.method.not_found", "authentication.method.provider_conflict", "authentication.method.conflict", "authentication.method.unavailable", "authentication.sessions.maximum_reached", "authentication.internal", "audit.unavailable"},
		"GET /api/v1/users/me/sessions":                                   sessionErrorCodes("authentication.internal"),
		"POST /api/v1/users/me/sessions/revoke":                           sessionMutationErrorCodes("request.invalid", "session.id.invalid", "session.not_found", "authentication.internal"),
		"POST /api/v1/users/me/sessions/revoke-all":                       sessionMutationErrorCodes("authentication.internal"),
		"GET /api/v1/users/me/mfa":                                        sessionErrorCodes("authentication.mfa.disabled", "authentication.mfa.unavailable"),
		"POST /api/v1/users/me/mfa/setup":                                 recentSessionMutationErrorCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"),
		"POST /api/v1/users/me/mfa/activate":                              recentSessionMutationErrorCodes("request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"),
		"POST /api/v1/users/me/mfa/challenge":                             sessionMutationErrorCodes("request.invalid", "authentication.mfa.invalid_code", "authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"),
		"POST /api/v1/users/me/mfa/recovery-codes/regenerate":             strongRecentSessionMutationErrorCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "authentication.internal", "audit.unavailable"),
		"POST /api/v1/users/me/mfa/disable":                               strongRecentSessionMutationErrorCodes("authentication.mfa.disabled", "authentication.mfa.not_found", "authentication.mfa.conflict", "authentication.mfa.unavailable", "audit.unavailable"),
		"GET /api/v1/users/me/tokens":                                     sessionErrorCodes("personal_access_token.unavailable"),
		"POST /api/v1/users/me/tokens":                                    recentSessionMutationErrorCodes("request.invalid", "resource.not_found", "personal_access_token.invalid", "personal_access_token.maximum_reached", "personal_access_token.unavailable", "audit.unavailable"),
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/disable": sessionMutationErrorCodes("request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable"),
		"POST /api/v1/users/me/tokens/{personal_access_token_id}/enable":  recentSessionMutationErrorCodes("request.invalid", "resource.not_found", "personal_access_token.maximum_reached", "personal_access_token.unavailable", "audit.unavailable"),
		"DELETE /api/v1/users/me/tokens/{personal_access_token_id}":       sessionMutationErrorCodes("request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable"),
		"GET /api/v1/websocket":                                           sessionErrorCodes("request.invalid", "websocket.origin.invalid", "websocket.unavailable"),
	}
}

func sessionErrorCodes(extra ...string) []string {
	return append([]string{"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous"}, extra...)
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

func TestAcademicUnitOpenAPIAgreesWithRuntime(t *testing.T) {
	t.Parallel()

	document := readOpenAPIDocument(t)
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", document.OpenAPI)
	}
	runtimeAPI := newRoutingTestAPI(model.APIURLSuffix)
	if err := runtimeAPI.collectResources(model.APIURLSuffix, academicUnitResource(nil)); err != nil {
		t.Fatal(err)
	}
	suite := openAPIAgreementSuite{
		Operations: []openAPIAgreementOperation{
			{Key: "GET /api/v1/academic-units", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitListOK", SuccessSchema: "AcademicUnitListResponse", PublicErrorCodes: principalContractCodes("request.invalid", "administration.unavailable")},
			{Key: "POST /api/v1/academic-units", Auth: AuthPrincipalRequired, Idempotency: IdempotencyOptional, RequestBodyRef: "#/components/requestBodies/CreateAcademicUnit", RequestSchema: "CreateAcademicUnitRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/AcademicUnitCreated", SuccessSchema: "AcademicUnitResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "administration.unavailable", "academic_unit.invalid", "academic_unit.conflict", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress")},
			{Key: "GET /api/v1/academic-units/{academic_unit_id}", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitOK", SuccessSchema: "AcademicUnitResponse", PublicErrorCodes: principalContractCodes("resource.not_found", "administration.unavailable")},
			{Key: "PATCH /api/v1/academic-units/{academic_unit_id}", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/UpdateAcademicUnit", RequestSchema: "UpdateAcademicUnitRequest", SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitOK", SuccessSchema: "AcademicUnitResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict", "administration.unavailable")},
			{Key: "DELETE /api/v1/academic-units/{academic_unit_id}", Auth: AuthPrincipalRequired, SuccessStatus: "204", SuccessRef: "#/components/responses/AcademicUnitArchived", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit.conflict", "administration.unavailable")},
			{Key: "GET /api/v1/academic-units/{academic_unit_id}/children", Auth: AuthPrincipalRequired, SuccessStatus: "200", SuccessRef: "#/components/responses/AcademicUnitListOK", SuccessSchema: "AcademicUnitListResponse", PublicErrorCodes: principalContractCodes("resource.not_found", "administration.unavailable")},
			{Key: "POST /api/v1/academic-units/{academic_unit_id}/children", Auth: AuthPrincipalRequired, RequestBodyRef: "#/components/requestBodies/CreateAcademicUnit", RequestSchema: "CreateAcademicUnitRequest", SuccessStatus: "201", SuccessRef: "#/components/responses/AcademicUnitCreated", SuccessSchema: "AcademicUnitResponse", PublicErrorCodes: principalMutationContractCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict", "administration.unavailable")},
		},
		Schemas: []openAPIAgreementSchema{
			{Name: "AcademicUnitResponse", DTO: reflect.TypeOf(academicUnitResponse{}), Required: []string{"id", "create_at", "update_at", "delete_at", "institution_id", "name", "display_name", "description"}},
			{Name: "CreateAcademicUnitRequest", DTO: reflect.TypeOf(createAcademicUnitRequest{}), Required: []string{"name", "display_name"}},
			{Name: "UpdateAcademicUnitRequest", DTO: reflect.TypeOf(updateAcademicUnitRequest{})},
			{Name: "ProblemDetails", DTO: reflect.TypeOf(Problem{}), Required: []string{"type", "title", "status", "code"}},
		},
		OperationSelector: academicUnitOperation,
	}
	assertOpenAPIAgreement(t, suite, runtimeAPI.Routes())

	listSchema := document.Components.Schemas["AcademicUnitListResponse"]
	if listSchema.Type != "array" || listSchema.Items.Ref != "#/components/schemas/AcademicUnitResponse" {
		t.Fatalf("AcademicUnitListResponse = %#v", listSchema)
	}
	archivedResponse := document.Components.Responses["AcademicUnitArchived"]
	if archivedResponse.Headers["Cache-Control"].Ref != "#/components/headers/NoStore" {
		t.Fatalf("AcademicUnitArchived does not require no-store: %#v", archivedResponse)
	}
}

func academicUnitOperation(_ string, path string) bool {
	if !strings.HasPrefix(path, model.APIURLSuffix+"/academic-units") {
		return false
	}
	return !strings.HasSuffix(path, "/classes") && !strings.HasSuffix(path, "/members") &&
		!strings.Contains(path, "/programmes") && !strings.Contains(path, "/invitations")
}

func principalContractCodes(extra ...string) []string {
	return append([]string{"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous", "authorization.denied", "authorization.request.invalid", "authorization.unavailable"}, extra...)
}

func principalMutationContractCodes(extra ...string) []string {
	return principalContractCodes(append([]string{"authentication.csrf.invalid", "audit.unavailable"}, extra...)...)
}

func statusCode(status int) string {
	return strconv.Itoa(status)
}
