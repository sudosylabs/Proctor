// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/store/storetest/user_store.go.

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPasswordCredentialStore(t *testing.T, ss store.Store) {
	t.Run("RemovalFencesPasswordProofAndRevokesOnlyPasswordSessions", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		requireNoError(t, err)
		credential, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$remove-me"})
		requireNoError(t, err)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "password-removal-subject-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		requireNoError(t, err)

		passwordSession, passwordCredentials, _ := newSession(user.ID.String())
		passwordSession, _, err = ss.Session().Save(ctx, testSessionCreation(t, ctx, ss, passwordSession, passwordCredentials, 10))
		requireNoError(t, err)
		providerSession, providerCredentials, _ := newSession(user.ID.String())
		providerSession.AuthenticationMethod = "oidc"
		providerSession.AuthenticationProviderID = "campus-cas"
		providerSession.ExternalIdentityID = identity.ID
		providerSession, _, err = ss.Session().Save(ctx, testSessionCreation(t, ctx, ss, providerSession, providerCredentials, 10))
		requireNoError(t, err)

		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "remove_password")
		result, err := ss.PasswordCredential().RemoveWithAudit(ctx, &store.PasswordCredentialRemoval{
			UserID: user.ID, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
			ChangedAt: model.GetMillis(), RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: attempt.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		if len(result.RevokedSessions) != 1 || result.RevokedSessions[0].ID != passwordSession.ID || len(result.RevokedTokenHashes) != 2 {
			t.Fatalf("RemoveWithAudit() revocations = %#v", result)
		}
		revoked, err := ss.Session().Get(ctx, passwordSession.ID.String())
		requireNoError(t, err)
		if !revoked.RevokedAt.Valid {
			t.Fatalf("password Session was not revoked = %#v", revoked)
		}
		retained, err := ss.Session().Get(ctx, providerSession.ID.String())
		requireNoError(t, err)
		if retained.RevokedAt.Valid {
			t.Fatalf("provider Session was revoked = %#v", retained)
		}
		oldRehash := &store.PasswordCredentialRehash{ID: credential.ID, UserID: user.ID,
			ExpectedHash: credential.PasswordHash, ExpectedRevision: credential.Revision, PasswordHash: "must-not-resurrect"}
		if err = ss.PasswordCredential().Rehash(ctx, oldRehash); !errors.Is(err, store.ErrPasswordCredentialChanged) {
			t.Fatalf("Rehash(removed credential) = %v", err)
		}
		replacement, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID: user.ID, PasswordHash: "encoded-reenrolled-password",
		})
		requireNoError(t, err)
		if replacement.ID == credential.ID || replacement.Revision != credential.Revision {
			t.Fatal("re-enrollment did not create a distinct credential with initial revision")
		}
		if err = ss.PasswordCredential().Rehash(ctx, oldRehash); !errors.Is(err, store.ErrPasswordCredentialChanged) {
			t.Fatalf("Rehash(replaced credential) = %v", err)
		}
		staleSession, staleCredentials, _ := newSession(user.ID.String())
		_, _, err = ss.Session().Save(ctx, &store.SessionCreation{
			Session: staleSession, Credentials: staleCredentials, MaximumActive: 10,
			PasswordProof: store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision},
		})
		if !errors.Is(err, store.ErrPasswordCredentialChanged) {
			t.Fatalf("Save(replaced password proof) = %v", err)
		}
	})

	t.Run("AuditedEnrollmentRequiresVerifiedMailboxAndProtectsLastMethod", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		requireNoError(t, err)
		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "enroll_password")
		result, err := ss.PasswordCredential().EnrollWithAudit(ctx, &store.PasswordCredentialEnrollment{
			Credential:   &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$enrolled"},
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: attempt.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		if result.PasswordCredential == nil || result.PasswordCredential.UserID != user.ID {
			t.Fatalf("EnrollWithAudit() = %#v", result)
		}
		terminal, err := ss.Audit().Get(ctx, attempt.ID.String())
		requireNoError(t, err)
		if terminal.Status != model.AuditStatusSuccess {
			t.Fatalf("audit status = %s", terminal.Status)
		}
		removeAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "remove_password")
		_, err = ss.PasswordCredential().RemoveWithAudit(ctx, &store.PasswordCredentialRemoval{UserID: user.ID,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			ChangedAt:    model.GetMillis(), RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: removeAttempt.ID.String(), AuditAt: model.GetMillis()})
		if !errors.Is(err, store.ErrLastUsableAuthenticationMethod) {
			t.Fatalf("RemoveWithAudit(last) error = %v", err)
		}
		unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID.String())
		requireNoError(t, err)
		if unchanged.ID != result.PasswordCredential.ID {
			t.Fatalf("credential changed = %#v", unchanged)
		}
	})
	t.Run("RehashPreservesPasswordRevisionAndLifecycle", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		input := &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"}
		saved, err := ss.PasswordCredential().Save(ctx, input)
		requireNoError(t, err)
		if !saved.ID.IsValid() || !input.ID.IsZero() || saved.Revision != 1 {
			t.Fatalf("Save() identity/revision = %s/%d", saved.ID, saved.Revision)
		}
		request := &store.PasswordCredentialRehash{ID: saved.ID, UserID: user.ID,
			ExpectedHash: saved.PasswordHash, ExpectedRevision: saved.Revision, PasswordHash: "encoded-rehashed-password"}
		requireNoError(t, ss.PasswordCredential().Rehash(ctx, request))
		got, err := ss.PasswordCredential().GetByUser(ctx, user.ID.String())
		requireNoError(t, err)
		if got.PasswordHash != request.PasswordHash || got.ID != saved.ID || got.UserID != saved.UserID ||
			got.Revision != saved.Revision || !got.CreatedAt.Equal(saved.CreatedAt) ||
			!got.PasswordChangedAt.Equal(saved.PasswordChangedAt) || got.UpdatedAt.Before(saved.UpdatedAt) {
			t.Fatal("rehash did not preserve password identity, revision, and lifecycle")
		}
		if err = ss.PasswordCredential().Rehash(ctx, request); !errors.Is(err, store.ErrPasswordCredentialChanged) {
			t.Fatalf("Rehash(stale hash) = %v", err)
		}
		cases := []struct {
			name   string
			mutate func(*store.PasswordCredentialRehash)
		}{
			{name: "revision", mutate: func(input *store.PasswordCredentialRehash) { input.ExpectedRevision++ }},
			{name: "credential", mutate: func(input *store.PasswordCredentialRehash) { input.ID = model.NewPasswordCredentialID() }},
			{name: "user", mutate: func(input *store.PasswordCredentialRehash) { input.UserID = model.NewUserID() }},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				candidate := *request
				candidate.ExpectedHash = got.PasswordHash
				candidate.PasswordHash = "must-not-be-written"
				test.mutate(&candidate)
				if err := ss.PasswordCredential().Rehash(ctx, &candidate); !errors.Is(err, store.ErrPasswordCredentialChanged) {
					t.Fatalf("Rehash(stale %s) = %v", test.name, err)
				}
				unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID.String())
				requireNoError(t, err)
				if *unchanged != *got {
					t.Fatal("failed rehash changed persisted credential")
				}
			})
		}
	})

	t.Run("ReferencesAndUniqueness", func(t *testing.T) {
		ctx := context.Background()
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID:       model.UserID(model.NewId()),
			PasswordHash: "$argon2id$missing",
		})
		var reference *store.ErrReference
		if !errors.As(err, &reference) ||
			reference.Constraint != "password_credentials_user_id_fkey" {
			t.Fatalf("unknown user error = %v", err)
		}
		user := saveUser(t, ctx, ss)
		first := &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$first"}
		_, err = ss.PasswordCredential().Save(ctx, first)
		requireNoError(t, err)
		_, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID:       user.ID,
			PasswordHash: "$argon2id$second",
		})
		var conflict *store.ErrConflict
		if !errors.As(err, &conflict) ||
			conflict.Constraint != "password_credentials_user_id_key" {
			t.Fatalf("duplicate user credential error = %v", err)
		}
	})
}

func saveAuthenticationMethodAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExternalIdentityManage),
		Resource: model.Resource{Type: model.ResourceUser, ID: userID}, ScopeType: model.RoleScopeInstitution,
		ScopeID: model.NewId(), Status: model.AuditStatusAttempt, NodeID: "authentication-method-storetest",
		Parameters: []byte(`{"operation":"` + operation + `"}`)})
	requireNoError(t, err)
	return event
}
