// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "testing"

func TestPersonalAccessTokenPrincipalValidation(t *testing.T) {
	principal := Principal{
		UserId: modelTestID(), CredentialId: modelTestID(),
		CredentialType:       CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token",
		ClientType:           SessionClientCLI,
		CredentialScopes:     []string{string(ActionClassView)},
		AcademicUnitId:       modelTestID(),
	}
	if !principal.IsValid() {
		t.Fatalf("valid PAT principal rejected: %#v", principal)
	}
	principal.SessionId = modelTestID()
	if principal.IsValid() {
		t.Fatal("PAT principal with a session ID was accepted")
	}
	principal.SessionId = ""
	principal.CredentialScopes = []string{"unknown.action"}
	if principal.IsValid() {
		t.Fatal("PAT principal with an unknown action was accepted")
	}
}

func modelTestID() string {
	return NewId()
}
