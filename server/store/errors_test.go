// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreErrorsPreserveClassificationAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver detail")
	notFound := NewErrNotFound("institution", "id").Wrap(cause)
	require.True(t, IsNotFound(notFound))
	require.ErrorIs(t, notFound, cause)

	conflict := NewErrConflict("institution", "institutions_singleton_key", cause)
	require.True(t, IsConflict(conflict))
	require.ErrorIs(t, conflict, cause)
}

func TestIsUserIdentityConflictIsNarrow(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "username", err: NewErrConflict("user", "users_username_key", nil), want: true},
		{name: "email", err: NewErrConflict("user", "users_email_key", nil), want: true},
		{name: "wrapped email", err: errors.Join(errors.New("save user"), NewErrConflict("user", "users_email_key", nil)), want: true},
		{name: "user id", err: NewErrConflict("user", "users_pkey", nil)},
		{name: "job id", err: NewErrConflict("job", "jobs_pkey", nil)},
		{name: "ordinary error", err: errors.New("database unavailable")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, IsUserIdentityConflict(test.err), "IsUserIdentityConflict(%v)", test.err)
		})
	}
}
