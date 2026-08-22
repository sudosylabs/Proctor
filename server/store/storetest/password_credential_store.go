// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
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
	t.Run("RemovalRevokesOnlyPasswordSessions", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		requireNoError(t, err)
		_, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$remove-me"})
		requireNoError(t, err)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "password-removal-subject-" + model.NewId(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		requireNoError(t, err)

		passwordSession, passwordCredentials, _ := newSession(user.ID.String())
		passwordSession, _, err = ss.Session().Save(ctx, passwordSession, passwordCredentials, 10)
		requireNoError(t, err)
		providerSession, providerCredentials, _ := newSession(user.ID.String())
		providerSession.AuthenticationMethod = "oidc"
		providerSession.AuthenticationProviderID = "campus-cas"
		providerSession.ExternalIdentityID = identity.ID
		providerSession, _, err = ss.Session().Save(ctx, providerSession, providerCredentials, 10)
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
	t.Run("SaveGetAndUpdate", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		input := &model.PasswordCredential{
			UserID:       user.ID,
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$first$hash",
		}
		saved, err := ss.PasswordCredential().Save(ctx, input)
		requireNoError(t, err)
		if !saved.ID.IsValid() || !input.ID.IsZero() {
			t.Fatalf("Save() saved=%#v input=%#v", saved, input)
		}
		got, err := ss.PasswordCredential().GetByUser(ctx, user.ID.String())
		requireNoError(t, err)
		if *got != *saved {
			t.Fatalf("GetByUser() = %#v, want %#v", got, saved)
		}
		saved.PasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$second$hash"
		saved.PasswordChangedAt = model.TimeFromMillis(model.GetMillis() + 100)
		updated, err := ss.PasswordCredential().Update(ctx, saved)
		requireNoError(t, err)
		if updated.PasswordHash != saved.PasswordHash {
			t.Fatalf("Update() = %#v", updated)
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
