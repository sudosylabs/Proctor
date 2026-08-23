//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestMFAPersonalAccessTokenCanonicalIDConstraints(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()
	institution, err := persistence.Institution().Save(ctx, &model.Institution{Name: "credential-constraints", DisplayName: "Credential Constraints"})
	if err != nil {
		t.Fatal(err)
	}

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "credential-constraints", Email: "credential-constraints@example.edu", DisplayName: "Credential Constraints",
	})
	credential, err := persistence.MFA().SavePending(ctx, &model.MFACredential{
		UserID: user.ID, State: model.MFAStatePending, EncryptedSecret: "ciphertext",
		EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFromMillis(model.GetMillis() + 60_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveryID := model.NewMFARecoveryCodeID()
	if _, err := persistence.GetMaster().Exec(ctx, `
		INSERT INTO mfa_recovery_codes (id, created_at, updated_at, user_id, code_hash)
		VALUES (?, NOW(), NOW(), ?, ?)`, recoveryID.String(), user.ID.String(), model.HashToken(model.NewCredentialToken())); err != nil {
		t.Fatal(err)
	}
	token := &model.PersonalAccessToken{
		UserID: user.ID, Description: "constraint token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, ExpiresAt: model.TimeFromMillis(model.GetMillis() + 60_000),
	}
	token.PrepareCreate(model.NewPersonalAccessTokenID(), model.NowUTC())
	if err := insertPersonalAccessToken(ctx, persistence.GetMaster(), token); err != nil {
		t.Fatal(err)
	}
	patSession := savePersonalAccessTokenMutationSession(t, ctx, persistence, user.ID)
	preparation, err := persistence.PersonalAccessToken().PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: user.ID.String(), TokenID: token.ID.String(), Kind: store.PersonalAccessTokenMutationRevoke, Lifetime: time.Minute,
		Audit: &model.AuditEvent{ActorID: user.ID, SessionID: patSession.ID, Action: "personal_access_token.revoke",
			Resource: model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, ScopeType: model.RoleScopeInstitution,
			ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "constraint-test"},
	})
	if err != nil {
		t.Fatal(err)
	}

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
		{name: "personal access token preparation id", query: "UPDATE personal_access_token_mutation_preparations SET id = 'bad' WHERE id = ?", id: preparation.ID, constraint: "personal_access_token_mutation_preparations_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query, test.id)
			var postgresErr *pq.Error
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want check violation %s", err, test.constraint)
			}
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
	session, _, err := persistence.Session().Save(ctx, &model.Session{
		UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken("mfa-corruption-access"), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken("mfa-corruption-refresh"), ExpiresAt: now.Add(2 * time.Hour)},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := persistence.MFA().SavePending(ctx, &model.MFACredential{
		UserID: user.ID, State: model.MFAStatePending, EncryptedSecret: "ciphertext",
		EncryptionKeyID: "0123456789abcdef0123456789abcdef", PendingExpiresAt: model.OptionalTimeFrom(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx,
		"UPDATE mfa_credentials SET encrypted_secret = '' WHERE id = ?", pending.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	activationAt := model.MillisFromTime(pending.CreatedAt) + 1
	audit, notice := mfaSQLSecurityNoticeFixture(t, ctx, persistence, user, model.MailTemplateIdentityMFAEnabled, activationAt)
	_, err = persistence.MFA().Activate(ctx, &store.MFAActivationMutation{
		CredentialID: pending.ID.String(), UserID: user.ID.String(), TimeStep: 1,
		RecoveryCodes: []*model.MFARecoveryCode{{CodeHash: model.HashToken("mfa-corruption-recovery")}},
		SessionID:     session.ID.String(), At: activationAt, AuditEventID: audit.ID.String(), AuditAt: activationAt, Notice: notice,
	})
	var persisted *persistedStateError
	if !errors.As(err, &persisted) || persisted.Entity != "mfa_credential" || persisted.Field != "encrypted_secret" {
		t.Fatalf("Activate() error = %v, want mfa_credential.encrypted_secret persisted-state error", err)
	}

	var state string
	if err := persistence.GetMaster().Get(ctx, &state, "SELECT state FROM mfa_credentials WHERE id = ?", pending.ID.String()); err != nil {
		t.Fatal(err)
	}
	if state != string(model.MFAStatePending) {
		t.Fatalf("credential state = %q, want pending after rollback", state)
	}
	if count, err := persistence.MFA().CountRecoveryCodes(ctx, user.ID.String()); err != nil || count != 0 {
		t.Fatalf("recovery codes after rollback = %d, %v; want 0", count, err)
	}
	gotSession, err := persistence.Session().Get(ctx, session.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.AuthenticationStrength != model.AuthenticationSingleFactor || gotSession.MFACompletedAt.Valid {
		t.Fatalf("session assurance changed despite rollback: %#v", gotSession)
	}
}

func mfaSQLSecurityNoticeFixture(t *testing.T, ctx context.Context, persistence *SQLStore, user *model.User, key model.MailTemplateKey, at int64) (*model.AuditEvent, store.MFASecurityNotice) {
	t.Helper()
	scopeID := model.NewId()
	audit, err := persistence.Audit().Save(ctx, &model.AuditEvent{ActorID: user.ID, Action: "mfa.security_transition",
		Resource: model.Resource{Type: model.ResourceUser, ID: user.ID.String()}, ScopeType: model.RoleScopeInstitution,
		ScopeID: scopeID, Status: model.AuditStatusAttempt, NodeID: "mfa-integration"})
	if err != nil {
		t.Fatal(err)
	}
	when := model.TimeFromMillis(at)
	deliveryID, occurrenceID, jobID := model.NewMailDeliveryID(), model.NewMailOccurrenceID(), model.NewJobID()
	command, err := model.EncodeMailDeliveryCommand(model.MailDeliveryCommandV1{DeliveryID: deliveryID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := model.NewJob(jobID, model.JobTypeMailDeliver, 1, command, deliveryID.String(), when, when, model.MailMaximumAttempts)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceSecurityNotice, TemplateKey: key, ActorUserID: user.ID, CreatedAt: when}
	delivery := &model.MailDelivery{ID: deliveryID, OccurrenceID: occurrenceID, JobID: jobID, TargetUserID: user.ID, TemplateKey: key,
		TemplateDigest: strings.Repeat("a", 64), MaskedRecipient: "m***@example.edu", State: model.MailDeliveryQueued,
		CreatedAt: when, UpdatedAt: when, MessageDate: when, Deadline: when.Add(24 * time.Hour),
		MessageID: "<mail." + deliveryID.String() + "@example.edu>", EncryptedPayload: json.RawMessage(`{"key_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), Revision: 1}
	return audit, store.MFASecurityNotice{Occurrence: occurrence, Delivery: delivery, Job: job}
}
