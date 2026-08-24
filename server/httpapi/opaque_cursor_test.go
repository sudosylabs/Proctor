// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type opaqueCursorFixture struct {
	Version int    `json:"version,omitempty"`
	Key     string `json:"key"`
}

func opaqueCursorFixtureSpec() opaqueCursorSpec[opaqueCursorFixture] {
	return opaqueCursorSpec[opaqueCursorFixture]{
		label: "fixture", maximumEncodedLength: 256, currentVersion: 1,
		members:        []string{"version", "key"},
		version:        func(cursor opaqueCursorFixture) int { return cursor.Version },
		setVersion:     func(cursor *opaqueCursorFixture, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate: func(cursor opaqueCursorFixture) error {
			if cursor.Key == "" {
				return errors.New("key is required")
			}
			return nil
		},
	}
}

func TestOpaqueCursorCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	want := opaqueCursorFixture{Key: "keyset"}
	first, err := encodeOpaqueCursor(want, spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeOpaqueCursor(want, spec)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || strings.ContainsAny(first, "+/=") {
		t.Fatalf("non-deterministic or non-raw-URL encoding: %q / %q", first, second)
	}
	decoded, err := decodeOpaqueCursor(first, spec)
	if err != nil || decoded != (opaqueCursorFixture{Version: 1, Key: want.Key}) {
		t.Fatalf("round trip = %#v, %v", decoded, err)
	}
	document, err := base64.RawURLEncoding.Strict().DecodeString(first)
	if err != nil || !strings.Contains(string(document), `"version":1`) {
		t.Fatalf("encoded document = %q, %v", document, err)
	}
}

func TestOpaqueCursorRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	encodeDocument := func(document string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(document))
	}
	valid := `{"version":1,"key":"keyset"}`
	tests := map[string]string{
		"empty":              "",
		"over bound":         strings.Repeat("a", spec.maximumEncodedLength+1),
		"at bound invalid":   strings.Repeat("a", spec.maximumEncodedLength),
		"malformed alphabet": "%%%",
		"padded base64":      encodeDocument(valid) + "=",
		"trailing bits":      "Zh",
		"invalid UTF-8":      base64.RawURLEncoding.EncodeToString([]byte{0xff}),
		"array":              encodeDocument(`[]`),
		"scalar":             encodeDocument(`1`),
		"null":               encodeDocument(`null`),
		"duplicate version":  encodeDocument(`{"version":1,"version":1,"key":"keyset"}`),
		"case alias version": encodeDocument(`{"version":2,"Version":1,"key":"keyset"}`),
		"case alias key":     encodeDocument(`{"version":1,"Key":"keyset"}`),
		"duplicate key":      encodeDocument(`{"version":1,"key":"a","key":"b"}`),
		"unknown member":     encodeDocument(`{"version":1,"key":"keyset","unknown":true}`),
		"missing key":        encodeDocument(`{"version":1}`),
		"concatenated":       encodeDocument(valid + `{}`),
		"trailing token":     encodeDocument(valid + ` true`),
		"trailing garbage":   encodeDocument(valid + ` garbage`),
		"unsupported":        encodeDocument(`{"version":2,"key":"keyset"}`),
	}
	for name, raw := range tests {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeOpaqueCursor(raw, spec); err == nil || !strings.HasPrefix(err.Error(), "invalid fixture cursor") {
				t.Fatalf("decode(%q) error = %v", raw, err)
			}
		})
	}
}

func TestOpaqueCursorRejectsEncoderAndSpecFailures(t *testing.T) {
	t.Parallel()
	spec := opaqueCursorFixtureSpec()
	if _, err := encodeOpaqueCursor(opaqueCursorFixture{}, spec); err == nil {
		t.Fatal("resource-invalid cursor encoded")
	}

	type marshalFailureCursor struct {
		Version int `json:"version"`
		Value   any `json:"value"`
	}
	marshalSpec := opaqueCursorSpec[marshalFailureCursor]{
		label: "marshal fixture", maximumEncodedLength: 256, currentVersion: 1,
		members:        []string{"version", "value"},
		version:        func(cursor marshalFailureCursor) int { return cursor.Version },
		setVersion:     func(cursor *marshalFailureCursor, version int) { cursor.Version = version },
		acceptsVersion: func(version int) bool { return version == 1 },
		validate:       func(marshalFailureCursor) error { return nil },
	}
	if _, err := encodeOpaqueCursor(marshalFailureCursor{Value: make(chan struct{})}, marshalSpec); err == nil {
		t.Fatal("JSON marshal failure was ignored")
	}

	invalidSpec := spec
	invalidSpec.validate = nil
	if _, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, invalidSpec); err == nil {
		t.Fatal("invalid encoder specification accepted")
	}
	if _, err := decodeOpaqueCursor("anything", invalidSpec); err == nil {
		t.Fatal("invalid decoder specification accepted")
	}
	duplicateMembers := spec
	duplicateMembers.members = []string{"version", "version"}
	if _, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, duplicateMembers); err == nil {
		t.Fatal("duplicate specification members accepted")
	}
}

func FuzzOpaqueCursorDecode(f *testing.F) {
	spec := opaqueCursorFixtureSpec()
	valid, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "seed"}, spec)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"", valid, "%%%", "Zh", base64.RawURLEncoding.EncodeToString([]byte(`null`))} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = decodeOpaqueCursor(raw, spec)
	})
}

func TestOpaqueCursorDocumentIsJSON(t *testing.T) {
	// Keep json imported in this focused test so the fixture also proves the
	// emitted document is a normal object consumable by standard decoders.
	raw, err := encodeOpaqueCursor(opaqueCursorFixture{Key: "keyset"}, opaqueCursorFixtureSpec())
	if err != nil {
		t.Fatal(err)
	}
	document, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || !json.Valid(document) {
		t.Fatalf("document = %q, %v", document, err)
	}
}
