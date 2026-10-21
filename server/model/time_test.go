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

	"github.com/stretchr/testify/require"
)

func TestTimeUTCNormalizesLocation(t *testing.T) {
	t.Parallel()

	local := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("offset", 2*3600))
	got := TimeUTC(local)
	require.Same(t, time.UTC, got.Location())
	require.True(t, got.Equal(local.UTC()), "TimeUTC() = %v", got)
	require.True(t, TimeUTC(time.Time{}).IsZero(), "zero time should stay zero")
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
			require.Equal(t, want, got, "normalized time")
			require.False(t, got.After(instant), "normalization must be idempotent and must not extend an instant")
			require.Equal(t, TimeUTC(got), got, "normalization must be idempotent and must not extend an instant")
		})
	}
}

func TestNowUTCUsesDurablePrecision(t *testing.T) {
	t.Parallel()

	before := TimeUTC(time.Now())
	got := NowUTC()
	after := time.Now()
	require.Same(t, time.UTC, got.Location(), "current domain time = %v, outside [%v, %v] or not UTC microseconds", got, before, after)
	require.Equal(t, 0, got.Nanosecond()%1000, "current domain time = %v, outside [%v, %v] or not UTC microseconds", got, before, after)
	require.False(t, got.Before(before), "current domain time = %v, outside [%v, %v] or not UTC microseconds", got, before, after)
	require.False(t, got.After(after), "current domain time = %v, outside [%v, %v] or not UTC microseconds", got, before, after)
}

func TestMillisTimeRoundTrip(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(0), MillisFromTime(time.Time{}), "zero time must map to 0 millis")
	require.True(t, TimeFromMillis(0).IsZero(), "non-positive millis must map to zero time")
	require.True(t, TimeFromMillis(-1).IsZero(), "non-positive millis must map to zero time")
	instant := time.Date(2026, 8, 8, 12, 0, 0, 123000000, time.UTC)
	millis := MillisFromTime(instant)
	back := TimeFromMillis(millis)
	require.True(t, back.Equal(time.UnixMilli(millis).UTC()))
	require.Same(t, time.UTC, back.Location(), "TimeFromMillis must return UTC")
}

func TestOptionalTimeAbsentAndPresent(t *testing.T) {
	t.Parallel()

	var absent OptionalTime
	require.False(t, absent.Valid)
	require.Equal(t, int64(0), absent.Millis())
	require.Nil(t, absent.Ptr())
	require.Empty(t, absent.FormatRFC3339())

	present := OptionalTimeFromMillis(1_700_000_000_000)
	require.True(t, present.Valid)
	require.Equal(t, int64(1_700_000_000_000), present.Millis())
	require.Same(t, time.UTC, present.UTC().Time.Location(), "UTC() must normalize")
	require.False(t, OptionalTimeFromMillis(0).Valid, "0 millis is absent")
	fromPtr := OptionalTimeFromPtr(present.Ptr())
	require.True(t, fromPtr.Valid)
	require.Equal(t, present.Millis(), fromPtr.Millis())
	require.False(t, OptionalTimeFromPtr(nil).Valid, "nil ptr is absent")
}

func TestOptionalTimeJSONRoundTrip(t *testing.T) {
	t.Parallel()

	present := OptionalTimeFrom(time.Date(2026, 8, 8, 12, 30, 0, 0, time.UTC))
	data, err := json.Marshal(present)
	require.NoError(t, err)
	var decoded OptionalTime
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.True(t, decoded.Valid)
	require.True(t, decoded.Time.Equal(present.Time))

	require.NoError(t, json.Unmarshal([]byte("null"), &decoded))
	require.False(t, decoded.Valid, "null must decode as absent")
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
			require.True(t, tc.got.Valid, "optional time = %#v, want present", tc.got)
			require.Equal(t, want, tc.got.Time, "optional time = %#v, want present", tc.got)
		})
	}

	raw := OptionalTime{Time: instant, Valid: true}
	got := raw.Ptr()
	require.NotNil(t, got, "optional pointer")
	require.Equal(t, want, *got, "optional pointer")
	const formatted = "2026-08-08T10:30:00.113537Z"
	require.Equal(t, formatted, raw.FormatRFC3339(), "formatted optional time")
	data, err := json.Marshal(raw)
	require.NoError(t, err, "encoded optional time")
	require.Equal(t, `"`+formatted+`"`, string(data), "encoded optional time")
	var decoded OptionalTime
	require.NoError(t, json.Unmarshal([]byte(`"2026-08-08T12:30:00.113537613+02:00"`), &decoded))
	require.True(t, decoded.Valid, "decoded optional time = %#v, want present", decoded)
	require.Equal(t, want, decoded.Time, "decoded optional time = %#v, want present", decoded)
	zero := OptionalTimeFrom(time.Time{})
	require.True(t, zero.Valid)
	require.Equal(t, time.Time{}, zero.Time)
}
