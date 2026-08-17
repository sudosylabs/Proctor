// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"reflect"
	"testing"
	"time"
)

func TestPersonalAccessTokenPrincipalValidation(t *testing.T) {
	principal := Principal{
		UserID: UserID(modelTestID()), CredentialID: PrincipalCredentialID(modelTestID()),
		CredentialType:       CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token",
		ClientType:           SessionClientCLI,
		CredentialScopes:     []string{string(ActionClassView)},
		AcademicUnitID:       AcademicUnitID(modelTestID()),
	}
	if principal.Validate() != nil {
		t.Fatalf("valid PAT principal rejected: %#v", principal)
	}
	principal.SessionID = SessionID(modelTestID())
	if principal.Validate() == nil {
		t.Fatal("PAT principal with a session ID was accepted")
	}
	principal.SessionID = ""
	principal.CredentialScopes = []string{"unknown.action"}
	if principal.Validate() == nil {
		t.Fatal("PAT principal with an unknown action was accepted")
	}
	principal.CredentialScopes = []string{string(ActionExamSittingParticipate)}
	if principal.Validate() == nil {
		t.Fatal("PAT principal with a relationship-only action was accepted")
	}
	principal.CredentialScopes = []string{string(ActionRoleBindingManage)}
	if principal.Validate() == nil {
		t.Fatal("PAT principal with an interactive-only action was accepted")
	}
}

func modelTestID() string {
	return NewId()
}

func TestSessionPrincipalUsesNativeAssuranceTimes(t *testing.T) {
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	principal := Principal{
		UserID:                 NewUserID(),
		SessionID:              NewSessionID(),
		CredentialID:           PrincipalCredentialID(NewId()),
		CredentialType:         CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: AuthenticationMultiFactor,
		ClientType:             SessionClientCLI,
		AuthenticatedAt:        now.Add(-time.Hour),
		MFACompletedAt:         OptionalTimeFrom(now.Add(-time.Minute)),
	}
	if principal.Validate() != nil {
		t.Fatalf("valid session principal rejected: %#v", principal)
	}
	if got := principal.LastAuthenticationAt(); !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("LastAuthenticationAt() = %s", got)
	}
	if !principal.IsRecentlyAuthenticated(now, 15*time.Minute) {
		t.Fatal("recent MFA completion was not accepted")
	}
	if principal.IsRecentlyAuthenticated(now, 30*time.Second) {
		t.Fatal("stale MFA completion was accepted")
	}
}

func TestPrincipalContractsDoNotDeclareWireTags(t *testing.T) {
	for _, contract := range []reflect.Type{
		reflect.TypeOf(Principal{}),
		reflect.TypeOf(AuthenticationTokens{}),
	} {
		for i := 0; i < contract.NumField(); i++ {
			field := contract.Field(i)
			if tag := field.Tag.Get("json"); tag != "" {
				t.Fatalf("%s.%s declares JSON tag %q", contract.Name(), field.Name, tag)
			}
		}
	}
}
