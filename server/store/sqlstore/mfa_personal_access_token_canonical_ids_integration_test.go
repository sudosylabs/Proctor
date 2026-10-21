//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestMFAPersonalAccessTokenCanonicalIDConstraints(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "credential-constraints", DisplayName: "Credential Constraints"})
	require.NoError(t, err)

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "credential-constraints", Email: "credential-constraints@example.edu", DisplayName: "Credential Constraints",
	})
	credential, err := persistence.MFA().SeedPending(ctx, &model.MFACredential{
		UserID: user.ID, State: model.MFAStatePending, EncryptedSecret: "ciphertext",
		EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFromMillis(model.GetMillis() + 60_000),
	})
	require.NoError(t, err)
	recoveryID := model.NewMFARecoveryCodeID()
	_, err = persistence.GetMaster().Exec(ctx, `
		INSERT INTO mfa_recovery_codes (id, created_at, updated_at, user_id, code_hash)
		VALUES (?, NOW(), NOW(), ?, ?)`, recoveryID.String(), user.ID.String(), model.HashToken(model.NewCredentialToken()))
	require.NoError(t, err)
	token := &model.PersonalAccessToken{
		UserID: user.ID, Description: "constraint token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, ExpiresAt: model.TimeFromMillis(model.GetMillis() + 60_000),
	}
	token.PrepareCreate(model.NewPersonalAccessTokenID(), model.NowUTC())
	require.NoError(t, insertPersonalAccessToken(ctx, persistence.GetMaster(), token))
	patSession := savePersonalAccessTokenMutationSession(t, ctx, persistence, user.ID)
	preparation, err := persistence.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: user.ID, TokenID: token.ID, Kind: store.PersonalAccessTokenMutationRevoke, Lifetime: time.Minute,
		AuditEvent: &model.AuditEvent{ActorID: user.ID, SessionID: patSession.ID, Action: "personal_access_token.revoke",
			Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
			ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "constraint-test"},
	})
	require.NoError(t, err)

	tests := []struct {
		name       string
		query      string
		id         string
		constraint string
	}{
		{name: "MFA credential id", query: "UPDATE mfa_credentials SET id = 'bad' WHERE id = ?", id: credential.ID.String(), constraint: "mfa_credentials_id_canonical_check"},
		{name: "MFA credential user id", query: "UPDATE mfa_credentials SET user_id = 'bad' WHERE id = ?", id: credential.ID.String(), constraint: "mfa_credentials_user_id_canonical_check"},
		{name: "recovery code id", query: "UPDATE mfa_recovery_codes SET id = 'bad' WHERE id = ?", id: recoveryID.String(), constraint: "mfa_recovery_codes_id_canonical_check"},
		{name: "recovery code user id", query: "UPDATE mfa_recovery_codes SET user_id = 'bad' WHERE id = ?", id: recoveryID.String(), constraint: "mfa_recovery_codes_user_id_canonical_check"},
		{name: "personal access token id", query: "UPDATE personal_access_tokens SET id = 'bad' WHERE id = ?", id: token.ID.String(), constraint: "personal_access_tokens_id_canonical_check"},
		{name: "personal access token user id", query: "UPDATE personal_access_tokens SET user_id = 'bad' WHERE id = ?", id: token.ID.String(), constraint: "personal_access_tokens_user_id_canonical_check"},
		{name: "personal access token academic unit id", query: "UPDATE personal_access_tokens SET academic_unit_id = 'bad' WHERE id = ?", id: token.ID.String(), constraint: "personal_access_tokens_academic_unit_id_canonical_check"},
		{name: "personal access token preparation id", query: "UPDATE personal_access_token_mutation_preparations SET id = 'bad' WHERE id = ?", id: preparation.ID.String(), constraint: "personal_access_token_mutation_preparations_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query, test.id)
			var postgresErr *pq.Error
			require.ErrorAs(t, err, &postgresErr, "constraint error")
			require.Equal(t, "23514", string(postgresErr.Code), "constraint error")
			require.Equal(t, test.constraint, postgresErr.Constraint, "constraint error")
		})
	}
}

func TestMFAActivationRejectsCorruptionBeforeCommit(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "mfa-corruption", Email: "mfa-corruption@example.edu", DisplayName: "MFA Corruption",
	})
	now := model.NowUTC()
	session, sessionCredentials, err := storetest.SaveSession(ctx, persistence, sessionCreationForSQLTest(t, ctx, persistence, &model.Session{
		UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken("mfa-corruption-access"), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken("mfa-corruption-refresh"), ExpiresAt: now.Add(2 * time.Hour)},
	}, 10))
	require.NoError(t, err)
	pending, err := persistence.MFA().SeedPending(ctx, &model.MFACredential{
		UserID: user.ID, State: model.MFAStatePending, EncryptedSecret: "ciphertext",
		EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFrom(now.Add(time.Hour)),
	})
	require.NoError(t, err)
	_, err = persistence.GetMaster().Exec(ctx,
		"UPDATE mfa_credentials SET encrypted_secret = '' WHERE id = ?", pending.ID.String(),
	)
	require.NoError(t, err)
	activationAt := model.MillisFromTime(pending.CreatedAt) + 1
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, persistence, user, model.MailTemplateIdentityMFAEnabled, activationAt)
	_, err = persistence.MFA().Activate(ctx, &store.MFAActivationMutation{
		Principal: mfaSQLPrincipal(session, sessionCredentials[0]), RecentAuthenticationTTL: time.Hour,
		CredentialID: pending.ID, UserID: user.ID, TimeStep: time.Now().Unix() / 30,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken("mfa-corruption-recovery")}},
		SessionID:     session.ID, At: model.TimeFromMillis(activationAt), AuditEventID: audit.ID, AuditAt: activationAt, Notice: notice,
	})
	var persisted *persistedStateError
	require.ErrorAs(t, err, &persisted, "Activate() error")
	require.Equal(t, "mfa_credential", persisted.Entity, "Activate() error")
	require.Equal(t, "encrypted_secret", persisted.Field, "Activate() error")

	var state string
	require.NoError(t, persistence.GetMaster().Get(ctx, &state, "SELECT state FROM mfa_credentials WHERE id = ?", pending.ID.String()))
	require.Equal(t, string(model.MFAStatePending), state, "credential state")
	count, countErr := persistence.MFA().CountRecoveryCodes(ctx, user.ID)
	require.NoError(t, countErr, "recovery codes after rollback")
	require.Equal(t, 0, count, "recovery codes after rollback")
	gotSession, err := persistence.Session().Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, model.AuthenticationSingleFactor, gotSession.AuthenticationStrength)
	require.False(t, gotSession.MFACompletedAt.Valid)
}

func mfaSQLSecurityNoticeFixture(t *testing.T, ctx context.Context, persistence *SQLStore, user *model.User, key model.MailTemplateKey, at int64) (*model.AuditEvent, store.MFASecurityNotice) {
	t.Helper()
	scopeID := model.NewID()
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: "mfa.security_transition",
		Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "mfa-integration"})
	require.NoError(t, err)
	when := model.TimeFromMillis(at)
	deliveryID, occurrenceID, jobID := model.NewMailDeliveryID(), model.NewMailOccurrenceID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	require.NoError(t, err)
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), when, when, model.MailMaximumAttempts)
	require.NoError(t, err)
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSecurityNotice, TemplateKey: key, ActorUserID: user.ID, CreatedAt: when}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: key,
		TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "m***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: when, UpdatedAt: when, MessageDate: when, Deadline: when.Add(24 * time.Hour),
		MessageID: "<mail." + deliveryID.String() + "@example.edu>", EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1}
	return audit, store.MFASecurityNotice{Occurrence: occurrence, Delivery: delivery, Job: job}
}

func mfaSQLPrincipal(session *model.Session, credential *model.SessionCredential) model.Principal {
	return model.Principal{UserID: session.UserID, SessionID: session.ID, CredentialID: model.PrincipalCredentialID(credential.ID), CredentialType: model.CredentialSessionAccess, AuthenticationGeneration: session.AuthenticationGeneration, MFARecoveryRequired: session.MFARecoveryRequired, ClientType: session.ClientType, AuthenticationMethod: session.AuthenticationMethod, AuthenticationProviderID: session.AuthenticationProviderID, ExternalIdentityID: session.ExternalIdentityID, AuthenticationStrength: session.AuthenticationStrength, AuthenticatedAt: session.AuthenticatedAt, ReauthenticatedAt: session.ReauthenticatedAt, MFACompletedAt: session.MFACompletedAt}
}
