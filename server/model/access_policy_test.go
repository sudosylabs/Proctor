// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"testing"
	"time"
)

func TestInitialAccessPolicyIsConservativeAndValid(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 1 || !policy.LocalLoginEnabled || !policy.InvitationAdmissionEnabled ||
		!policy.InvitationLocalCredentialEnabled || !policy.DesktopAuthorizationEnabled ||
		policy.PublicRegistrationEnabled || len(policy.ProviderAdmissions) != 0 {
		t.Fatalf("initial policy = %#v", policy)
	}
}

func TestAccessPolicyRejectsProviderAdmissionUnknownToTheClosedVocabulary(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	policy.ProviderAdmissions = map[string]ProviderAdmissionMode{"oidc-main": "automatic"}
	if err := policy.Validate(); err == nil {
		t.Fatal("Validate() accepted an unknown provider admission mode")
	}
}
