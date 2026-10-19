// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"

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
			if got := IsTransientError(test.err); got != test.want {
				t.Fatalf("IsTransientError() = %v, want %v", got, test.want)
			}
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
			if !errors.As(got, &invalid) {
				t.Fatalf("translateError() = %T %v, want *store.ErrInvalidInput", got, got)
			}
			if invalid.Entity != "user" || invalid.Field != test.field || invalid.Value != nil {
				t.Fatalf("translateError() = %+v, want entity user field %q without value", invalid, test.field)
			}
			var driver *pq.Error
			if !errors.As(got, &driver) || driver != test.err {
				t.Fatalf("translateError() does not wrap the driver error: %v", got)
			}
			if IsTransientError(got) {
				t.Fatal("rejected values classified as transient")
			}
		})
	}
}
