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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type authenticationMethodPasswordStoreFake struct {
	store.PasswordCredentialStore
	enroll *store.PasswordCredentialEnrollment
	remove *store.PasswordCredentialRemoval
}

func (s *authenticationMethodPasswordStoreFake) EnrollWithAudit(_ context.Context, input *store.PasswordCredentialEnrollment) (*store.AuthenticationMethodMutationResult, error) {
	s.enroll = input
	return &store.AuthenticationMethodMutationResult{}, nil
}
func (s *authenticationMethodPasswordStoreFake) RemoveWithAudit(_ context.Context, input *store.PasswordCredentialRemoval) (*store.AuthenticationMethodMutationResult, error) {
	s.remove = input
	return &store.AuthenticationMethodMutationResult{}, nil
}

type authenticationMethodIdentityStoreFake struct {
	store.ExternalIdentityStore
	unlink *store.ExternalIdentityUnlink
}

func (s *authenticationMethodIdentityStoreFake) UnlinkWithAudit(_ context.Context, input *store.ExternalIdentityUnlink) (*store.AuthenticationMethodMutationResult, error) {
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
	hasher, err := newPasswordHasher(testPasswordPolicy())
	if err != nil {
		t.Fatal(err)
	}
	service, err := newAuthenticationMethodService(passwords,
		&authenticationMethodIdentityStoreFake{}, externalProviderSourceSet{}, &accessPolicyCapabilitiesFake{},
		hasher, audit, &authenticationMethodEffectsFake{}, 15*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	if err = service.enrollPassword(context.Background(), NewInvocation(principal, model.RequestMetadata{}), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if passwords.enroll == nil || passwords.enroll.Credential.UserID != principal.UserID || passwords.enroll.Credential.PasswordHash == "" || passwords.enroll.Credential.PasswordHash == "correct horse battery staple" || passwords.enroll.AuditEventID != audit.beginID {
		t.Fatalf("enrollment = %#v", passwords.enroll)
	}
	weak := principal
	weak.AuthenticationStrength = model.AuthenticationSingleFactor
	weak.MFACompletedAt = model.OptionalTime{}
	passwords.enroll = nil
	if err = service.enrollPassword(context.Background(), NewInvocation(weak, model.RequestMetadata{}), "correct horse battery staple"); !Is(err, "authentication.strong_required") {
		t.Fatalf("weak enrollment error = %v", err)
	}
	if passwords.enroll != nil {
		t.Fatal("weak enrollment reached Store")
	}
}

func TestAuthenticationMethodRemovalPassesCapabilitiesAndExactIdentity(t *testing.T) {
	now := time.UnixMilli(20_000)
	identities := &authenticationMethodIdentityStoreFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewAuditEventID().String()}
	hasher, _ := newPasswordHasher(testPasswordPolicy())
	capabilities := &accessPolicyCapabilitiesFake{snapshot: AccessPolicyCapabilitySnapshot{Providers: []AccessPolicyProviderCapability{{Descriptor: model.ExternalAuthenticationProvider{Id: "campus"}}}}}
	service, err := newAuthenticationMethodService(&authenticationMethodPasswordStoreFake{}, identities,
		externalProviderSourceSet{}, capabilities, hasher, audit, &authenticationMethodEffectsFake{}, 15*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	principal := userSettingsSessionPrincipal(now)
	principal.AuthenticationStrength = model.AuthenticationMultiFactor
	principal.MFACompletedAt = model.OptionalTimeFrom(now)
	id := model.NewExternalIdentityID()
	if err = service.unlink(context.Background(), NewInvocation(principal, model.RequestMetadata{}), id); err != nil {
		t.Fatal(err)
	}
	if identities.unlink == nil || identities.unlink.ID != id || identities.unlink.UserID != principal.UserID {
		t.Fatalf("unlink = %#v", identities.unlink)
	}
	if _, ok := identities.unlink.Capabilities.Providers["campus"]; !ok {
		t.Fatalf("capabilities = %#v", identities.unlink.Capabilities)
	}
}
