// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

func TestRetryTransientErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "serialization failure", err: &pq.Error{Code: postgresSerializationFailure}, want: true},
		{name: "deadlock", err: fmt.Errorf("query: %w", &pq.Error{Code: postgresDeadlockDetected}), want: true},
		{name: "unique violation", err: &pq.Error{Code: postgresUniqueViolation}, want: false},
		{
			name: "domain conflict wrapping transient driver error",
			err:  store.NewErrConflict("role", "roles_name_key", &pq.Error{Code: postgresSerializationFailure}),
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, IsTransientError(test.err), "IsTransientError()")
		})
	}
}

func TestTranslateErrorClassifiesRejectedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   *pq.Error
		field string
	}{
		{name: "check violation", err: &pq.Error{Code: postgresCheckViolation, Constraint: "users_revision_check"}, field: "users_revision_check"},
		{name: "not-null violation", err: &pq.Error{Code: postgresNotNullViolation, Column: "locale"}, field: "locale"},
		{name: "string too long", err: &pq.Error{Code: postgresStringDataTruncation}, field: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := translateError("user", "id", fmt.Errorf("save: %w", test.err))
			var invalid *store.ErrInvalidInput
			require.ErrorAs(t, got, &invalid, "translateError()")
			require.Equal(t, "user", invalid.Entity, "translateError() = %+v, want entity user field %q without value", invalid, test.field)
			require.Equal(t, test.field, invalid.Field, "translateError() = %+v, want entity user field %q without value", invalid, test.field)
			require.Nil(t, invalid.Value, "translateError() = %+v, want entity user field %q without value", invalid, test.field)
			var driver *pq.Error
			require.ErrorAs(t, got, &driver)
			require.Same(t, test.err, driver)
			require.False(t, IsTransientError(got), "rejected values classified as transient")
			require.True(t, store.IsConstraintViolation(got), "database rejection is not marked as a constraint violation")
		})
	}
	require.False(t, store.IsConstraintViolation(store.NewErrInvalidInput("user", "value", nil)), "Store-refused input marked as a constraint violation")
}
