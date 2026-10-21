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
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestTranslateErrorClassifiesPostgreSQLRejectedValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "rejected-values", Email: "rejected-values@example.edu", DisplayName: "Rejected Values",
	})
	for _, test := range []struct {
		name  string
		query string
		args  []any
		field string
	}{
		{name: "check violation", query: `UPDATE users SET revision = 0 WHERE id = ?`, args: []any{user.ID.String()}, field: "users_revision_check"},
		{name: "not-null violation", query: `UPDATE users SET locale = NULL WHERE id = ?`, args: []any{user.ID.String()}, field: "locale"},
		{name: "string too long", query: `UPDATE users SET username = ? WHERE id = ?`, args: []any{strings.Repeat("u", 65), user.ID.String()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query, test.args...)
			require.Error(t, err, "PostgreSQL accepted the rejected value")
			var invalid *store.ErrInvalidInput
			got := translateError("user", user.ID.String(), err)
			require.ErrorAs(t, got, &invalid, "translateError(%v) = %#v; want invalid input on", err, got)
			require.Equal(t, test.field, invalid.Field, "translateError(%v) = %#v; want invalid input on", err, got)
		})
	}
}

// TestExecutorsTranslateIntegrityViolations covers writes that never call
// translateError themselves: the executors still report a Store error naming
// the violated table and constraint, and keep the driver error wrapped.
func TestExecutorsTranslateIntegrityViolations(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	first := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "executor-first", Email: "executor-first@example.edu", DisplayName: "Executor First",
	})
	second := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "executor-second", Email: "executor-second@example.edu", DisplayName: "Executor Second",
	})
	_, err := persistence.GetMaster().Exec(ctx, `UPDATE users SET username = ? WHERE id = ?`, first.Username, second.ID.String())
	var conflict *store.ErrConflict
	require.ErrorAs(t, err, &conflict, "unique violation through Exec")
	require.Equal(t, "users", conflict.Resource)
	require.Equal(t, "users_username_key", conflict.Constraint)

	tx, err := persistence.GetMaster().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var revision int64
	err = tx.Get(ctx, &revision, `UPDATE users SET revision = 0 WHERE id = ? RETURNING revision`, first.ID.String())
	require.True(t, store.IsConstraintViolation(err), "check violation through Get = %v", err)
	var driver *pq.Error
	require.ErrorAs(t, err, &driver, "driver error is wrapped")

	err = persistence.GetMaster().Get(ctx, &revision, `SELECT revision FROM users WHERE id = ?`, model.NewUserID().String())
	require.ErrorIs(t, err, sql.ErrNoRows, "absence passes through untranslated")
}
