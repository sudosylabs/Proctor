// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"encoding/json"
	"testing"
)

func TestEntityIDsAreDistinctTypes(t *testing.T) {
	t.Parallel()

	// Compile-time guard: distinct entity IDs must not be freely assignable.
	// The following would fail to compile if uncommented:
	//   var user UserID
	//   var class ClassID = user
	user := NewUserID()
	class := NewClassID()
	if user.String() == class.String() {
		t.Fatal("generated user and class IDs collided")
	}
	if !user.IsValid() || !class.IsValid() {
		t.Fatal("generated IDs must be valid")
	}
	if user.IsZero() || class.IsZero() {
		t.Fatal("generated IDs must not be zero")
	}
}

func TestSubmissionIDUsesCanonicalTypedIDContract(t *testing.T) {
	t.Parallel()

	id := NewSubmissionID()
	if !id.IsValid() || id.IsZero() {
		t.Fatalf("NewSubmissionID() = %q", id)
	}
	parsed, err := ParseSubmissionID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("ParseSubmissionID(%q) = %q, %v", id, parsed, err)
	}
}

func TestParseEntityIDRejectsInvalid(t *testing.T) {
	t.Parallel()

	if _, err := ParseUserID(""); err == nil {
		t.Fatal("empty user id accepted")
	}
	if _, err := ParseUserID("not-a-valid-id"); err == nil {
		t.Fatal("short user id accepted")
	}
	raw := NewId()
	got, err := ParseUserID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != raw {
		t.Fatalf("ParseUserID() = %q, want %q", got, raw)
	}
}

func TestEntityIDJSONRoundTrip(t *testing.T) {
	t.Parallel()

	id := NewSessionID()
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SessionID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("round trip = %q, want %q", decoded, id)
	}

	if err := json.Unmarshal([]byte(`""`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.IsZero() {
		t.Fatalf("empty JSON string should decode to zero, got %q", decoded)
	}
	if err := json.Unmarshal([]byte(`"bad"`), &decoded); err == nil {
		t.Fatal("invalid id must fail UnmarshalJSON")
	}
}

func TestLegacyStringIDStillCompilesAlongsideTypedIDs(t *testing.T) {
	t.Parallel()

	// Expand phase: plain NewId()/IsValidId remain for unmigrated aggregates.
	legacy := NewId()
	if !IsValidId(legacy) {
		t.Fatal(legacy)
	}
	typed := UserID(legacy)
	if !typed.IsValid() {
		t.Fatal(typed)
	}
}
