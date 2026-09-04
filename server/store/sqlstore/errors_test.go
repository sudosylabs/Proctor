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
