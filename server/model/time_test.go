// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
