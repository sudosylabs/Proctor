// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimeUTCNormalizesLocation(t *testing.T) {
	t.Parallel()

	local := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("offset", 2*3600))
	got := TimeUTC(local)
	if got.Location() != time.UTC {
		t.Fatalf("location = %v", got.Location())
	}
	if !got.Equal(local.UTC()) {
		t.Fatalf("TimeUTC() = %v, want %v", got, local.UTC())
	}
	if !TimeUTC(time.Time{}).IsZero() {
		t.Fatal("zero time should stay zero")
	}
}

func TestTimeUTCPreservesDurablePrecision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		nanoseconds int
		want        int
	}{
		{name: "exact microsecond", nanoseconds: 113537000, want: 113537000},
		{name: "below half microsecond", nanoseconds: 113537123, want: 113537000},
		{name: "above half microsecond", nanoseconds: 113537613, want: 113537000},
		{name: "end of second", nanoseconds: 999999999, want: 999999000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instant := time.Date(2026, 8, 8, 12, 0, 0, tc.nanoseconds, time.FixedZone("offset", 2*3600))
			want := time.Date(2026, 8, 8, 10, 0, 0, tc.want, time.UTC)
			got := TimeUTC(instant)
			if got != want {
				t.Fatalf("normalized time = %v, want %v", got, want)
			}
			if got.After(instant) || got != TimeUTC(got) {
				t.Fatal("normalization must be idempotent and must not extend an instant")
			}
		})
	}
}

func TestNowUTCUsesDurablePrecision(t *testing.T) {
	t.Parallel()

	before := TimeUTC(time.Now())
	got := NowUTC()
	after := time.Now()
	if got.Location() != time.UTC || got.Nanosecond()%1000 != 0 || got.Before(before) || got.After(after) {
		t.Fatalf("current domain time = %v, outside [%v, %v] or not UTC microseconds", got, before, after)
	}
}

func TestMillisTimeRoundTrip(t *testing.T) {
	t.Parallel()

	if MillisFromTime(time.Time{}) != 0 {
		t.Fatal("zero time must map to 0 millis")
	}
	if !TimeFromMillis(0).IsZero() || !TimeFromMillis(-1).IsZero() {
		t.Fatal("non-positive millis must map to zero time")
	}
	instant := time.Date(2026, 8, 8, 12, 0, 0, 123000000, time.UTC)
	millis := MillisFromTime(instant)
	back := TimeFromMillis(millis)
	if !back.Equal(time.UnixMilli(millis).UTC()) {
		t.Fatalf("round trip = %v", back)
	}
	if back.Location() != time.UTC {
		t.Fatal("TimeFromMillis must return UTC")
	}
}

func TestOptionalTimeAbsentAndPresent(t *testing.T) {
	t.Parallel()

	var absent OptionalTime
	if absent.Valid || absent.Millis() != 0 || absent.Ptr() != nil || absent.FormatRFC3339() != "" {
		t.Fatalf("absent = %#v", absent)
	}

	present := OptionalTimeFromMillis(1_700_000_000_000)
	if !present.Valid || present.Millis() != 1_700_000_000_000 {
		t.Fatalf("present = %#v", present)
	}
	if present.UTC().Time.Location() != time.UTC {
		t.Fatal("UTC() must normalize")
	}
	if OptionalTimeFromMillis(0).Valid {
		t.Fatal("0 millis is absent")
	}
	fromPtr := OptionalTimeFromPtr(present.Ptr())
	if !fromPtr.Valid || fromPtr.Millis() != present.Millis() {
		t.Fatalf("from ptr = %#v", fromPtr)
	}
	if OptionalTimeFromPtr(nil).Valid {
		t.Fatal("nil ptr is absent")
	}
}

func TestOptionalTimeJSONRoundTrip(t *testing.T) {
	t.Parallel()

	present := OptionalTimeFrom(time.Date(2026, 8, 8, 12, 30, 0, 0, time.UTC))
	data, err := json.Marshal(present)
	if err != nil {
		t.Fatal(err)
	}
	var decoded OptionalTime
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Valid || !decoded.Time.Equal(present.Time) {
		t.Fatalf("decoded = %#v", decoded)
	}

	if err := json.Unmarshal([]byte("null"), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Valid {
		t.Fatal("null must decode as absent")
	}
}

func TestOptionalTimeUsesDurablePrecision(t *testing.T) {
	t.Parallel()

	instant := time.Date(2026, 8, 8, 12, 30, 0, 113537613, time.FixedZone("offset", 2*3600))
	want := time.Date(2026, 8, 8, 10, 30, 0, 113537000, time.UTC)
	for _, tc := range []struct {
		name string
		got  OptionalTime
	}{
		{name: "constructor", got: OptionalTimeFrom(instant)},
		{name: "pointer", got: OptionalTimeFromPtr(&instant)},
		{name: "normalization", got: (OptionalTime{Time: instant, Valid: true}).UTC()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.got.Valid || tc.got.Time != want {
				t.Fatalf("optional time = %#v, want present %v", tc.got, want)
			}
		})
	}

	raw := OptionalTime{Time: instant, Valid: true}
	if got := raw.Ptr(); got == nil || *got != want {
		t.Fatalf("optional pointer = %v, want %v", got, want)
	}
	const formatted = "2026-08-08T10:30:00.113537Z"
	if got := raw.FormatRFC3339(); got != formatted {
		t.Fatalf("formatted optional time = %q, want %q", got, formatted)
	}
	data, err := json.Marshal(raw)
	if err != nil || string(data) != `"`+formatted+`"` {
		t.Fatalf("encoded optional time = %s, %v", data, err)
	}
	var decoded OptionalTime
	if err := json.Unmarshal([]byte(`"2026-08-08T12:30:00.113537613+02:00"`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Valid || decoded.Time != want {
		t.Fatalf("decoded optional time = %#v, want present %v", decoded, want)
	}
	zero := OptionalTimeFrom(time.Time{})
	if !zero.Valid || zero.Time != (time.Time{}) {
		t.Fatalf("present zero = %#v", zero)
	}
}
