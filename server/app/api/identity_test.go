// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestParsePathIDs(t *testing.T) {
	t.Parallel()

	raw := model.NewId()
	user, err := ParsePathUserID(raw)
	if err != nil || user.String() != raw {
		t.Fatalf("ParsePathUserID() = %q, %v", user, err)
	}
	if _, err := ParsePathUserID("bad"); err == nil {
		t.Fatal("expected invalid path id error")
	}
	unit, err := ParsePathAcademicUnitID(raw)
	if err != nil || unit.String() != raw {
		t.Fatalf("ParsePathAcademicUnitID() = %q, %v", unit, err)
	}
}

func TestHTTPTimeConversionRoundTrip(t *testing.T) {
	t.Parallel()

	if FormatTimeRFC3339(time.Time{}) != "" {
		t.Fatal("zero time formats empty")
	}
	instant := time.Date(2026, 8, 8, 15, 4, 5, 0, time.UTC)
	wire := FormatTimeRFC3339(instant)
	parsed, err := ParseTimeRFC3339(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(instant) {
		t.Fatalf("parsed = %v, want %v", parsed, instant)
	}

	millis := model.MillisFromTime(instant)
	if LegacyMillisToRFC3339(millis) == "" {
		t.Fatal("legacy millis conversion empty")
	}
	back, err := RFC3339ToLegacyMillis(wire)
	if err != nil || back != millis {
		t.Fatalf("RFC3339ToLegacyMillis() = %d, %v", back, err)
	}
	if FormatOptionalTimeRFC3339(model.OptionalTime{}) != nil {
		t.Fatal("absent optional formats null-compatible nil")
	}
}
