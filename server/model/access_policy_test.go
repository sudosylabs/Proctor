// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"reflect"
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

func TestAccessPolicyProviderIDsMatchDeploymentProviderGrammar(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"0-campus.oidc", "campus_oidc", "campus-oidc"} {
		settings := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100)).Settings()
		settings.ProviderAdmissions[providerID] = ProviderAdmissionLinkedOnly
		if err := settings.Validate(); err != nil {
			t.Fatalf("provider ID %q rejected: %v", providerID, err)
		}
	}
	for _, providerID := range []string{".campus", "_campus", "-campus", "Campus", "campus/provider"} {
		settings := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100)).Settings()
		settings.ProviderAdmissions[providerID] = ProviderAdmissionLinkedOnly
		if err := settings.Validate(); err == nil {
			t.Fatalf("provider ID %q accepted", providerID)
		}
	}
}

func TestAccessPolicyReplaceProducesSafeRevisionTransition(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(100)
	policy := NewInitialAccessPolicy(NewAccessPolicyID(), at)
	actorID := NewUserID()
	transition, err := policy.Replace(1, AccessPolicySettings{
		LocalLoginEnabled:                true,
		PublicRegistrationEnabled:        true,
		InvitationAdmissionEnabled:       true,
		InvitationLocalCredentialEnabled: true,
		DesktopAuthorizationEnabled:      false,
		ProviderAdmissions: map[string]ProviderAdmissionMode{
			"campus-oidc": ProviderAdmissionLinkedOnly,
		},
	}, actorID, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != 2 || transition.FromRevision != 1 || transition.ToRevision != 2 ||
		transition.ActorID != actorID || transition.Outcome != AccessPolicyTransitionApplied {
		t.Fatalf("replacement = policy %#v transition %#v", policy, transition)
	}
	want := []string{"desktop_authorization_enabled", "provider_admissions", "public_registration_enabled"}
	if !reflect.DeepEqual(transition.ChangedFields, want) {
		t.Fatalf("changed fields = %#v, want %#v", transition.ChangedFields, want)
	}
	if err := transition.Validate(); err != nil {
		t.Fatalf("transition validation: %v", err)
	}
}

func TestAccessPolicyReplaceRejectsStaleAndIncoherentSettings(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	settings := policy.Settings()
	if _, err := policy.Replace(2, settings, NewUserID(), time.UnixMilli(200)); err == nil {
		t.Fatal("Replace accepted a stale expected revision")
	}
	settings.LocalLoginEnabled = false
	settings.PublicRegistrationEnabled = true
	if _, err := policy.Replace(1, settings, NewUserID(), time.UnixMilli(200)); err == nil {
		t.Fatal("Replace accepted registration without local login")
	}
}
