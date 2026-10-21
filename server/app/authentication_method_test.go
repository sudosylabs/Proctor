// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type authenticationMethodPasswordStoreFake struct {
	store.PasswordCredentialStore
	enroll *store.PasswordCredentialEnrollment
	remove *store.PasswordCredentialRemoval
}

func (s *authenticationMethodPasswordStoreFake) Enroll(_ context.Context, input *store.PasswordCredentialEnrollment) (*store.AuthenticationMethodMutationResult, error) {
	s.enroll = input
	return &store.AuthenticationMethodMutationResult{}, nil
}
func (s *authenticationMethodPasswordStoreFake) Remove(_ context.Context, input *store.PasswordCredentialRemoval) (*store.AuthenticationMethodMutationResult, error) {
	s.remove = input
	return &store.AuthenticationMethodMutationResult{}, nil
}

type authenticationMethodIdentityStoreFake struct {
	store.ExternalIdentityStore
	unlink *store.ExternalIdentityUnlink
}

func (s *authenticationMethodIdentityStoreFake) Unlink(_ context.Context, input *store.ExternalIdentityUnlink) (*store.AuthenticationMethodMutationResult, error) {
	s.unlink = input
	return &store.AuthenticationMethodMutationResult{}, nil
}

type authenticationMethodUserStoreFake struct{ store.UserStore }

type authenticationMethodEffectsFake struct {
	userID             string
	sessionIDs, hashes []string
}

func (e *authenticationMethodEffectsFake) AuthenticationCacheInvalidated(context.Context, string, []string) {
}
func (e *authenticationMethodEffectsFake) SessionsRevoked(_ context.Context, userID string, sessionIDs, hashes []string) {
	e.userID, e.sessionIDs, e.hashes = userID, sessionIDs, hashes
}

func TestAuthenticationMethodEnrollmentRequiresStrongRecentAndPreparesHashOnly(t *testing.T) {
	now := time.UnixMilli(10_000)
	passwords := &authenticationMethodPasswordStoreFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewAuditEventID().String()}
	hasher, err := newPasswordHasher(testPasswordPolicy(), nil)
	require.NoError(t, err)
	service, err := newAuthenticationMethodService(passwords,
		&authenticationMethodIdentityStoreFake{}, externalProviderSourceSet{}, &accessPolicyCapabilitiesFake{},
		hasher, audit, &authenticationMethodEffectsFake{}, 15*time.Minute, func() time.Time { return now })
	require.NoError(t, err)
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	err = service.EnrollPassword(context.Background(), NewInvocation(principal, model.RequestMetadata{}), "correct horse battery staple")
	require.NoError(t, err)
	require.NotNil(t, passwords.enroll)
	require.Equal(t, principal.UserID, passwords.enroll.Credential.UserID)
	require.NotEmpty(t, passwords.enroll.Credential.PasswordHash)
	require.NotEqual(t, "correct horse battery staple", passwords.enroll.Credential.PasswordHash)
	require.Equal(t, model.AuditEventID(audit.beginID), passwords.enroll.AuditEventID)
	weak := principal
	weak.AuthenticationStrength = model.AuthenticationSingleFactor
	weak.MFACompletedAt = model.OptionalTime{}
	passwords.enroll = nil
	err = service.EnrollPassword(context.Background(), NewInvocation(weak, model.RequestMetadata{}), "correct horse battery staple")
	require.True(t, apperror.Is(err, "authentication.strong_required"), "weak enrollment error = %v", err)
	require.Nil(t, passwords.enroll, "weak enrollment reached Store")
}

func TestPasswordEnrollmentRejectsWorkBeforeAuditAndPersistence(t *testing.T) {
	now := time.UnixMilli(10_000)
	passwords := &authenticationMethodPasswordStoreFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewAuditEventID().String()}
	hasher, err := newPasswordHasher(testPasswordPolicy(), nil)
	require.NoError(t, err)
	service, err := newAuthenticationMethodService(passwords,
		&authenticationMethodIdentityStoreFake{}, externalProviderSourceSet{}, &accessPolicyCapabilitiesFake{},
		hasher, audit, &authenticationMethodEffectsFake{}, 15*time.Minute, func() time.Time { return now })
	require.NoError(t, err)
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	invocation := NewInvocation(principal, model.RequestMetadata{})
	saturatePasswordWork(t, hasher)
	err = service.EnrollPassword(context.Background(), invocation, "correct horse battery staple")
	require.True(t, apperror.Is(err, "service.busy"), "saturated enrollment = %v", err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = service.EnrollPassword(ctx, invocation, "correct horse battery staple")
	require.True(t, apperror.Is(err, "authentication.internal"))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, passwords.enroll, "rejected password work reached audit or credential mutation")
	require.Empty(t, audit.attempt.Operation, "rejected password work reached audit or credential mutation")
}

func TestAuthenticationMethodRemovalPassesCapabilitiesAndExactIdentity(t *testing.T) {
	now := time.UnixMilli(20_000)
	identities := &authenticationMethodIdentityStoreFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewAuditEventID().String()}
	hasher, _ := newPasswordHasher(testPasswordPolicy(), nil)
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{ID: "campus"}}}}}
	service, err := newAuthenticationMethodService(&authenticationMethodPasswordStoreFake{}, identities,
		externalProviderSourceSet{}, capabilities, hasher, audit, &authenticationMethodEffectsFake{}, 15*time.Minute, func() time.Time { return now })
	require.NoError(t, err)
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	id := model.NewExternalIdentityID()
	err = service.UnlinkExternalIdentity(context.Background(), NewInvocation(principal, model.RequestMetadata{}), id)
	require.NoError(t, err)
	require.NotNil(t, identities.unlink)
	require.Equal(t, id, identities.unlink.ID)
	require.Equal(t, principal.UserID, identities.unlink.UserID)
	_, ok := identities.unlink.Capabilities.Providers["campus"]
	require.True(t, ok, "capabilities = %#v", identities.unlink.Capabilities)
}
