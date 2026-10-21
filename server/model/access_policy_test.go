// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInitialAccessPolicyIsConservativeAndValid(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	require.NoError(t, policy.Validate())
	require.Equal(t, int64(1), policy.Revision)
	require.True(t, policy.LocalLoginEnabled)
	require.True(t, policy.InvitationAdmissionEnabled)
	require.True(t, policy.InvitationLocalCredentialEnabled)
	require.True(t, policy.DesktopAuthorizationEnabled)
	require.False(t, policy.PublicRegistrationEnabled)
	require.Empty(t, policy.ProviderAdmissions)
}

func TestAccessPolicyRejectsProviderAdmissionUnknownToTheClosedVocabulary(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	policy.ProviderAdmissions = map[string]ProviderAdmissionMode{"oidc-main": "automatic"}
	require.Error(t, policy.Validate(), "Validate() accepted an unknown provider admission mode")
}

func TestAccessPolicyProviderIDsMatchDeploymentProviderGrammar(t *testing.T) {
	t.Parallel()

	for _, providerID := range []string{"0-campus.oidc", "campus_oidc", "campus-oidc"} {
		settings := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100)).Settings()
		settings.ProviderAdmissions[providerID] = ProviderAdmissionLinkedOnly
		require.NoError(t, settings.Validate(), "provider ID %q rejected", providerID)
	}
	for _, providerID := range []string{".campus", "_campus", "-campus", "Campus", "campus/provider"} {
		settings := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100)).Settings()
		settings.ProviderAdmissions[providerID] = ProviderAdmissionLinkedOnly
		require.Error(t, settings.Validate(), "provider ID %q accepted", providerID)
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
	require.NoError(t, err)
	require.Equal(t, int64(2), policy.Revision, "replacement = policy %#v transition %#v", policy, transition)
	require.Equal(t, int64(1), transition.FromRevision, "replacement = policy %#v transition %#v", policy, transition)
	require.Equal(t, int64(2), transition.ToRevision, "replacement = policy %#v transition %#v", policy, transition)
	require.Equal(t, actorID, transition.ActorID, "replacement = policy %#v transition %#v", policy, transition)
	require.Equal(t, AccessPolicyTransitionApplied, transition.Outcome, "replacement = policy %#v transition %#v", policy, transition)
	want := []string{"desktop_authorization_enabled", "provider_admissions", "public_registration_enabled"}
	require.Equal(t, want, transition.ChangedFields, "changed fields")
	require.NoError(t, transition.Validate())
}

func TestAccessPolicyReplaceRejectsStaleAndIncoherentSettings(t *testing.T) {
	t.Parallel()

	policy := NewInitialAccessPolicy(NewAccessPolicyID(), time.UnixMilli(100))
	settings := policy.Settings()
	_, err := policy.Replace(2, settings, NewUserID(), time.UnixMilli(200))
	require.Error(t, err, "Replace accepted a stale expected revision")
	settings.LocalLoginEnabled = false
	settings.PublicRegistrationEnabled = true
	_, err = policy.Replace(1, settings, NewUserID(), time.UnixMilli(200))
	require.Error(t, err, "Replace accepted registration without local login")
}
