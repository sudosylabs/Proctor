// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestNullableTimeUsesDurablePrecision(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, 8, 8, 12, 30, 0, 113537613, time.FixedZone("offset", 2*3600))
	want := time.Date(2026, 8, 8, 10, 30, 0, 113537000, time.UTC)
	for _, tc := range []struct {
		name string
		got  sql.NullTime
	}{
		{name: "optional", got: NullTimeFromOptional(model.OptionalTime{Time: instant, Valid: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.got.Valid, "nullable time = %#v, want present", tc.got)
			require.Equal(t, want, tc.got.Time, "nullable time = %#v, want present", tc.got)
			require.Equal(t, want, TimeFromNullTime(tc.got), "concrete round trip")
			got := OptionalTimeFromNullTime(tc.got)
			require.True(t, got.Valid, "optional round trip = %#v, want present", got)
			require.Equal(t, want, got.Time, "optional round trip = %#v, want present", got)
		})
	}
	require.False(t, NullTimeFromOptional(model.OptionalTime{}).Valid, "absent time must remain SQL NULL")
	got := NullTimeFromOptional(model.OptionalTimeFrom(time.Time{}))
	require.True(t, got.Valid)
	require.True(t, got.Time.IsZero())
	require.True(t, TimeFromNullTime(sql.NullTime{}).IsZero(), "SQL NULL must remain absent")
	require.False(t, OptionalTimeFromNullTime(sql.NullTime{}).Valid, "SQL NULL must remain absent")
}
