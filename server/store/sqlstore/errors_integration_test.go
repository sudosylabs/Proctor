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
	"strings"
	"testing"

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
			if err == nil {
				t.Fatal("PostgreSQL accepted the rejected value")
			}
			var invalid *store.ErrInvalidInput
			if got := translateError("user", user.ID.String(), err); !errors.As(got, &invalid) || invalid.Field != test.field {
				t.Fatalf("translateError(%v) = %#v; want invalid input on %q", err, got, test.field)
			}
		})
	}
}
