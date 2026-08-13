//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"testing"

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
