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
	"errors"
	"slices"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestUserIdentityRecoveryCanonicalIDConstraints(t *testing.T) {
	ctx := context.Background()
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	at := model.NowUTC()
	wantConstraints := []string{
		"external_identities_id_canonical_check",
		"external_identities_user_id_canonical_check",
		"external_login_states_id_canonical_check",
		"password_credentials_id_canonical_check",
		"password_credentials_user_id_canonical_check",
		"user_tokens_id_canonical_check",
		"user_tokens_user_id_canonical_check",
		"users_custom_profile_picture_file_id_canonical_check",
		"users_default_profile_picture_file_id_canonical_check",
		"users_id_canonical_check",
	}
	var constraints []string
	if err := persistence.GetMaster().Select(ctx, &constraints, `
		SELECT conname FROM pg_constraint
		 WHERE conname = ANY(?)
		 ORDER BY conname`, pq.Array(wantConstraints)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(constraints, wantConstraints) {
		t.Fatalf("identity canonical constraints = %v, want %v", constraints, wantConstraints)
	}

	_, err := persistence.GetMaster().Exec(ctx, `
		INSERT INTO users (
			id, created_at, updated_at, username, email, locale, timezone,
			default_profile_picture_seed
		) VALUES ('bad', ?, ?, 'bad-id', 'bad-id@example.edu', 'en', 'UTC', ?)`,
		at, at, model.HashToken(model.NewCredentialToken()),
	)
	assertIdentityCanonicalViolation(t, err, "users_id_canonical_check")
}

func assertIdentityCanonicalViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var postgresErr *pq.Error
	if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != constraint {
		t.Fatalf("constraint violation = %v, want PostgreSQL check %s", err, constraint)
	}
}
