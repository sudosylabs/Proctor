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
		{name: "concrete", got: NullTimeFromTime(instant)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.got.Valid || tc.got.Time != want {
				t.Fatalf("nullable time = %#v, want present %v", tc.got, want)
			}
			if got := TimeFromNullTime(tc.got); got != want {
				t.Fatalf("concrete round trip = %v, want %v", got, want)
			}
			if got := OptionalTimeFromNullTime(tc.got); !got.Valid || got.Time != want {
				t.Fatalf("optional round trip = %#v, want present %v", got, want)
			}
		})
	}
	if NullTimeFromTime(time.Time{}).Valid || NullTimeFromOptional(model.OptionalTime{}).Valid {
		t.Fatal("absent time must remain SQL NULL")
	}
	if got := NullTimeFromOptional(model.OptionalTimeFrom(time.Time{})); !got.Valid || !got.Time.IsZero() {
		t.Fatalf("present zero time lost its presence: %#v", got)
	}
	if !TimeFromNullTime(sql.NullTime{}).IsZero() || OptionalTimeFromNullTime(sql.NullTime{}).Valid {
		t.Fatal("SQL NULL must remain absent")
	}
}
