//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestMFAPersonalAccessTokenCanonicalIDConstraints(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "credential-constraints", Email: "credential-constraints@example.edu", DisplayName: "Credential Constraints",
	})
	credential, err := persistence.MFA().SavePending(ctx, &model.MFACredential{
		UserID: user.ID, State: model.MFAStatePending, EncryptedSecret: "ciphertext",
		EncryptionKeyID: "0123456789abcdef", PendingExpiresAt: model.OptionalTimeFromMillis(model.GetMillis() + 60_000),
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
	token, err := persistence.PersonalAccessToken().Save(ctx, &model.PersonalAccessToken{
		UserID: user.ID, Description: "constraint token", TokenHash: model.HashToken(model.NewCredentialToken()),
		Scopes: []string{string(model.ActionClassView)}, ExpiresAt: model.TimeFromMillis(model.GetMillis() + 60_000),
	}, 5)
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
		EncryptionKeyID: "0123456789abcdef", PendingExpiresAt: model.OptionalTimeFrom(now.Add(time.Hour)),
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
	_, err = persistence.MFA().Activate(ctx, pending.ID.String(), user.ID.String(), 1,
		[]*model.MFARecoveryCode{{CodeHash: model.HashToken("mfa-corruption-recovery")}},
		session.ID.String(), activationAt)
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
