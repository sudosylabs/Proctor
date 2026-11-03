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
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPasswordCredentialStore(t *testing.T, ss store.Store) {
	t.Run("RemovalFencesPasswordProofAndRevokesOnlyPasswordSessions", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		require.NoError(t, err)
		// #nosec G101 -- Nonfunctional password hash used only to exercise Store removal.
		credential, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$remove-me"})
		require.NoError(t, err)
		identity, err := ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "password-removal-subject-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		require.NoError(t, err)

		passwordSession, passwordCredentials, _ := newSession(user.ID.String())
		passwordSession, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, passwordSession, passwordCredentials, 10))
		require.NoError(t, err)
		providerSession, providerCredentials, _ := newSession(user.ID.String())
		providerSession.AuthenticationMethod = "oidc"
		providerSession.AuthenticationProviderID = "campus-cas"
		providerSession.ExternalIdentityID = identity.ID
		providerSession, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, providerSession, providerCredentials, 10))
		require.NoError(t, err)

		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "remove_password")
		result, err := ss.PasswordCredential().Remove(ctx, &store.PasswordCredentialRemoval{
			UserID: user.ID, Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}},
			ChangedAt: model.NowUTC(), RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: attempt.ID, AuditAt: model.NowUTC(),
		})
		require.NoError(t, err)
		require.Len(t, result.RevokedSessions, 1)
		require.Equal(t, passwordSession.ID, result.RevokedSessions[0].ID)
		require.Len(t, result.RevokedTokenHashes, 2)
		revoked, err := ss.Session().Get(ctx, passwordSession.ID)
		require.NoError(t, err)
		require.True(t, revoked.RevokedAt.Valid)
		retained, err := ss.Session().Get(ctx, providerSession.ID)
		require.NoError(t, err)
		require.False(t, retained.RevokedAt.Valid)
		// #nosec G101 -- Synthetic replacement must be rejected before any hash is stored.
		oldRehash := &store.PasswordCredentialRehash{ID: credential.ID, UserID: user.ID,
			ExpectedHash: credential.PasswordHash, ExpectedRevision: credential.Revision, PasswordHash: "must-not-resurrect"}
		err = ss.PasswordCredential().Rehash(ctx, oldRehash)
		require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
		replacement, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID: user.ID, PasswordHash: "encoded-reenrolled-password",
		})
		require.NoError(t, err)
		require.NotEqual(t, credential.ID, replacement.ID, "re-enrollment did not create a distinct credential with initial revision")
		require.Equal(t, credential.Revision, replacement.Revision, "re-enrollment did not create a distinct credential with initial revision")
		err = ss.PasswordCredential().Rehash(ctx, oldRehash)
		require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
		staleSession, staleCredentials, _ := newSession(user.ID.String())
		_, _, err = SaveSession(ctx, ss, &store.SessionCreation{
			Session: staleSession, Credentials: staleCredentials, MaximumActive: 10,
			PasswordProof: store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision},
		})
		require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
	})

	t.Run("AuditedEnrollmentRequiresVerifiedMailboxAndProtectsLastMethod", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		require.NoError(t, err)
		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "enroll_password")
		result, err := ss.PasswordCredential().Enroll(ctx, &store.PasswordCredentialEnrollment{
			Credential:   &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$enrolled"},
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			AuditEventID: attempt.ID, AuditAt: model.NowUTC(),
		})
		require.NoError(t, err)
		require.NotNil(t, result.PasswordCredential)
		require.Equal(t, user.ID, result.PasswordCredential.UserID)
		terminal, err := ss.Audit().Get(ctx, attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.AuditStatusSuccess, terminal.Status)
		removeAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "remove_password")
		_, err = ss.PasswordCredential().Remove(ctx, &store.PasswordCredentialRemoval{UserID: user.ID,
			Capabilities: store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{}},
			ChangedAt:    model.NowUTC(), RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: removeAttempt.ID, AuditAt: model.NowUTC()})
		require.ErrorIs(t, err, store.ErrLastUsableAuthenticationMethod)
		unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, result.PasswordCredential.ID, unchanged.ID)
	})
	t.Run("ChangeKeepsTheCallingSessionAndRevokesTheOthers", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		// #nosec G101 -- Nonfunctional password hashes used only to exercise the Store change.
		credential, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$before-change"})
		require.NoError(t, err)
		kept, keptCredentials, _ := newSession(user.ID.String())
		kept, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, kept, keptCredentials, 10))
		require.NoError(t, err)
		other, otherCredentials, _ := newSession(user.ID.String())
		other, _, err = SaveSession(ctx, ss, testSessionCreation(t, ctx, ss, other, otherCredentials, 10))
		require.NoError(t, err)
		resetToken := newUserToken(user, model.UserTokenPasswordReset)
		_, err = issueUserToken(t, ss, ctx, resetToken, userTokenAudit(string(model.ActionAuthenticationPasswordResetRequest), user.ID.String(), model.NewID()))
		require.NoError(t, err)

		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "change_password")
		at := model.NowUTC()
		occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityPasswordChanged, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
		result, err := ss.PasswordCredential().Change(ctx, &store.PasswordCredentialChange{UserID: user.ID, KeepSessionID: kept.ID,
			PasswordHash: "$argon2id$after-change", RevocationReason: model.SessionRevocationPasswordChanged,
			Occurrence: occurrence, Delivery: delivery, Job: job, AuditEventID: attempt.ID, AuditAt: at})
		require.NoError(t, err)
		require.Equal(t, credential.ID, result.PasswordCredential.ID)
		require.Equal(t, credential.Revision+1, result.PasswordCredential.Revision)
		require.Len(t, result.RevokedSessions, 1)
		require.Equal(t, other.ID, result.RevokedSessions[0].ID)
		require.Len(t, result.RevokedTokenHashes, 2)

		stored, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, "$argon2id$after-change", stored.PasswordHash)
		require.Equal(t, credential.Revision+1, stored.Revision)
		require.False(t, stored.PasswordChangedAt.Before(credential.PasswordChangedAt))
		retained, err := ss.Session().Get(ctx, kept.ID)
		require.NoError(t, err)
		require.False(t, retained.RevokedAt.Valid)
		revoked, err := ss.Session().Get(ctx, other.ID)
		require.NoError(t, err)
		require.True(t, revoked.RevokedAt.Valid)
		require.Equal(t, model.SessionRevocationPasswordChanged, revoked.RevocationReason)
		reset, err := ss.UserToken().Get(ctx, resetToken.ID)
		require.NoError(t, err)
		require.True(t, reset.ArchivedAt.Valid, "an outstanding password-reset token survived the change")
		terminal, err := ss.Audit().Get(ctx, attempt.ID)
		require.NoError(t, err)
		require.Equal(t, model.AuditStatusSuccess, terminal.Status)
		notices, err := ss.Mail().ListDeliveries(ctx, store.MailDeliveryListOptions{TemplateKeys: []model.MailTemplateKey{model.MailTemplateIdentityPasswordChanged}, Limit: 200})
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(notices, func(notice *model.MailDelivery) bool { return notice.ID == delivery.ID }), "the password-changed notice was not queued")

		staleSession, staleCredentials, _ := newSession(user.ID.String())
		_, _, err = SaveSession(ctx, ss, &store.SessionCreation{Session: staleSession, Credentials: staleCredentials, MaximumActive: 10,
			PasswordProof: store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision}})
		require.ErrorIs(t, err, store.ErrPasswordCredentialChanged, "the replaced password still proves a new Session")
	})

	t.Run("ChangeRefusesRecentPasswordsAndBoundsHistory", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$history-0"})
		require.NoError(t, err)
		kept := model.NewSessionID()
		change := func(hash string, limit int) error {
			attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "change_password")
			at := model.NowUTC()
			occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityPasswordChanged, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
			_, err := ss.PasswordCredential().Change(ctx, &store.PasswordCredentialChange{UserID: user.ID, KeepSessionID: kept,
				PasswordHash: hash, Reuse: exactPasswordReuse(hash, limit), RevocationReason: model.SessionRevocationPasswordChanged,
				Occurrence: occurrence, Delivery: delivery, Job: job, AuditEventID: attempt.ID, AuditAt: at})
			return err
		}
		for _, hash := range []string{"$argon2id$history-1", "$argon2id$history-2", "$argon2id$history-3"} {
			require.NoError(t, change(hash, 3), "change to %s", hash)
		}
		require.ErrorIs(t, change("$argon2id$history-3", 3), store.ErrPasswordReused, "the current password")
		require.ErrorIs(t, change("$argon2id$history-1", 3), store.ErrPasswordReused, "a retained password")
		require.NoError(t, change("$argon2id$history-0", 3), "a password older than the history limit")
		current, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, "$argon2id$history-0", current.PasswordHash)
		require.NoError(t, change("$argon2id$history-3", 0), "a disabled history accepts any password")
		require.NoError(t, change("$argon2id$history-2", 1), "history was discarded when disabled")
	})

	t.Run("EnrollmentRefusesAPasswordRemovedRecently", func(t *testing.T) {
		ctx := context.Background()
		candidate := newUser()
		candidate.EmailVerified = true
		user, err := createUser(t, ctx, ss, candidate)
		require.NoError(t, err)
		_, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$removed"})
		require.NoError(t, err)
		_, err = ss.ExternalIdentity().Save(ctx, &model.ExternalIdentity{UserID: user.ID, Provider: "campus-cas",
			Subject: "history-subject-" + model.NewID(), LastSeenAt: model.OptionalTimeFromMillis(model.GetMillis())})
		require.NoError(t, err)
		capabilities := store.AccessDeploymentCapabilities{Providers: map[string]store.AccessProviderCapability{"campus-cas": {}}}
		removeAttempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "remove_password")
		_, err = ss.PasswordCredential().Remove(ctx, &store.PasswordCredentialRemoval{UserID: user.ID, Capabilities: capabilities,
			ChangedAt: model.NowUTC(), RevocationReason: model.SessionRevocationPasswordRemoved, AuditEventID: removeAttempt.ID, AuditAt: model.NowUTC()})
		require.NoError(t, err)
		enroll := func(hash string) error {
			attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "enroll_password")
			_, err := ss.PasswordCredential().Enroll(ctx, &store.PasswordCredentialEnrollment{
				Credential: &model.PasswordCredential{UserID: user.ID, PasswordHash: hash}, Reuse: exactPasswordReuse(hash, 5),
				Capabilities: capabilities, AuditEventID: attempt.ID, AuditAt: model.NowUTC()})
			return err
		}
		require.ErrorIs(t, enroll("$argon2id$removed"), store.ErrPasswordReused)
		_, err = ss.PasswordCredential().GetByUser(ctx, user.ID)
		require.True(t, store.IsNotFound(err), "a refused enrollment created a credential: %v", err)
		require.NoError(t, enroll("$argon2id$fresh"))
	})

	t.Run("ChangeWithoutActiveCredentialIsNotFound", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		attempt := saveAuthenticationMethodAuditAttempt(t, ctx, ss, user.ID.String(), "change_password")
		at := model.NowUTC()
		occurrence, delivery, job := userTokenMailFixture(t, user.ID, model.NewMailOccurrenceID(), model.MailOccurrenceSecurityNotice, model.MailTemplateIdentityPasswordChanged, model.JobTypeMailDeliver, at, at.Add(24*time.Hour))
		_, err := ss.PasswordCredential().Change(ctx, &store.PasswordCredentialChange{UserID: user.ID, KeepSessionID: model.NewSessionID(),
			PasswordHash: "$argon2id$after-change", RevocationReason: model.SessionRevocationPasswordChanged,
			Occurrence: occurrence, Delivery: delivery, Job: job, AuditEventID: attempt.ID, AuditAt: at})
		require.True(t, store.IsNotFound(err), "Change() without a credential = %v", err)
		_, err = ss.Mail().GetDelivery(ctx, delivery.ID)
		require.True(t, store.IsNotFound(err), "a refused change queued its notice: %v", err)
	})

	t.Run("RehashPreservesPasswordRevisionAndLifecycle", func(t *testing.T) {
		ctx := context.Background()
		user := saveUser(t, ctx, ss)
		input := &model.PasswordCredential{UserID: user.ID, PasswordHash: "encoded-original-password"}
		saved, err := ss.PasswordCredential().Save(ctx, input)
		require.NoError(t, err)
		require.True(t, saved.ID.IsValid(), "Save() identity/revision = %s", saved.ID)
		require.True(t, input.ID.IsZero(), "Save() identity/revision = %s", saved.ID)
		require.Equal(t, int64(1), saved.Revision, "Save() identity/revision = %s", saved.ID)
		request := &store.PasswordCredentialRehash{ID: saved.ID, UserID: user.ID,
			ExpectedHash: saved.PasswordHash, ExpectedRevision: saved.Revision, PasswordHash: "encoded-rehashed-password"}
		require.NoError(t, ss.PasswordCredential().Rehash(ctx, request))
		got, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, request.PasswordHash, got.PasswordHash, "rehash did not preserve password identity, revision, and lifecycle")
		require.Equal(t, saved.ID, got.ID, "rehash did not preserve password identity, revision, and lifecycle")
		require.Equal(t, saved.UserID, got.UserID, "rehash did not preserve password identity, revision, and lifecycle")
		require.Equal(t, saved.Revision, got.Revision, "rehash did not preserve password identity, revision, and lifecycle")
		require.True(t, got.CreatedAt.Equal(saved.CreatedAt), "rehash did not preserve password identity, revision, and lifecycle")
		require.True(t, got.PasswordChangedAt.Equal(saved.PasswordChangedAt), "rehash did not preserve password identity, revision, and lifecycle")
		require.False(t, got.UpdatedAt.Before(saved.UpdatedAt), "rehash did not preserve password identity, revision, and lifecycle")
		err = ss.PasswordCredential().Rehash(ctx, request)
		require.ErrorIs(t, err, store.ErrPasswordCredentialChanged)
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
				require.ErrorIs(t, ss.PasswordCredential().Rehash(ctx, &candidate), store.ErrPasswordCredentialChanged, "Rehash(stale %s)", test.name)
				unchanged, err := ss.PasswordCredential().GetByUser(ctx, user.ID)
				require.NoError(t, err)
				require.Equal(t, *got, *unchanged, "failed rehash changed persisted credential")
			})
		}
	})

	t.Run("ReferencesAndUniqueness", func(t *testing.T) {
		ctx := context.Background()
		_, err := ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID:       model.UserID(model.NewID()),
			PasswordHash: "$argon2id$missing",
		})
		var reference *store.ErrReference
		require.ErrorAs(t, err, &reference)
		require.Equal(t, "password_credentials_user_id_fkey", reference.Constraint)
		user := saveUser(t, ctx, ss)
		first := &model.PasswordCredential{UserID: user.ID, PasswordHash: "$argon2id$first"}
		_, err = ss.PasswordCredential().Save(ctx, first)
		require.NoError(t, err)
		_, err = ss.PasswordCredential().Save(ctx, &model.PasswordCredential{
			UserID:       user.ID,
			PasswordHash: "$argon2id$second",
		})
		var conflict *store.ErrConflict
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "password_credentials_user_id_key", conflict.Constraint)
	})
}

func saveAuthenticationMethodAuditAttempt(t *testing.T, ctx context.Context, ss store.Store, userID, operation string) *model.AuditEvent {
	t.Helper()
	event, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(model.ActionExternalIdentityManage),
		Resource: model.Resource{Type: model.ResourceUser, ID: userID}, ScopeType: model.RoleScopeInstitution,
		ScopeID: model.NewID(), Status: model.AuditStatusAttempt, NodeID: "authentication-method-storetest",
		Parameters: []byte(`{"operation":"` + operation + `"}`)})
	require.NoError(t, err)
	return event
}

// exactPasswordMatcher stands in for the Argon2 verifier: the fixture
// "hashes" are opaque strings, so a new password matches only its own text.
type exactPasswordMatcher struct{ hash string }

func (m exactPasswordMatcher) Matches(_ context.Context, encoded string) (bool, error) {
	return encoded == m.hash, nil
}

func exactPasswordReuse(hash string, limit int) store.PasswordReuseCheck {
	if limit == 0 {
		return store.PasswordReuseCheck{}
	}
	return store.PasswordReuseCheck{Limit: limit, Matcher: exactPasswordMatcher{hash: hash}}
}
